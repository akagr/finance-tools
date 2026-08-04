// Package tui is the interactive interface. It is the primary way the tool is
// used, not a wrapper over a command line.
//
// Three rules shape everything here:
//
//   - Discoverable. The keys that work right now are always on screen. Nothing
//     requires remembering a binding or reading documentation first.
//   - Efficient. The common path - updating balances - is the shortest one, and
//     every field accepts arithmetic so a figure can be typed the way it is
//     thought of.
//   - Honest. A number that rests on stale or incomplete data says so next to
//     itself, rather than looking as precise as one that does not.
//
// There is no decoration for its own sake: colour means direction or trust, and
// layout is alignment rather than ornament.
package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type view int

// Screens are ordered by how often they are used, not by any conceptual
// hierarchy: planning happens monthly, balances are updated a couple of times a
// year, and accounts are set up once.
const (
	viewDashboard view = iota
	viewPlan
	viewUpdate
	viewAccounts
	viewHistory
	viewQuery
)

var viewNames = []string{"Dashboard", "Plan", "Update", "Accounts", "History", "Query"}

// focusLevel says how much of the keyboard the current screen owns.
//
// A key that sometimes navigates and sometimes edits is worse than one that
// never navigates, so this is stated once here rather than decided key by key.
// The rule that follows from it: a global binding may only fire at a level where
// it cannot possibly mean something to the screen.
type focusLevel int

const (
	// focusIdle: the screen wants nothing in particular. Every global binding
	// works, including the bare ones - tab, the digits, q, r, ?.
	focusIdle focusLevel = iota
	// focusBusy: something is open - a list, a wizard step, a confirmation - so
	// letters and digits may mean something here. Only chorded keys stay global,
	// which is also what stops q from throwing away an unsaved round.
	focusBusy
	// focusTyping: a text field has the keyboard. As focusBusy, and in addition
	// tab belongs to the form, because tab means "next field" everywhere else in
	// computing and this application is not going to be the exception.
	focusTyping
)

// screen is one tab of the interface.
type screen interface {
	// Update handles a message and may return a command.
	Update(msg tea.Msg) tea.Cmd
	// View renders the body, given the space left after the header and footer.
	View(width, height int) string
	// Hints are the keys currently available, shown in the footer.
	Hints() []hint
	// Focus reports how much of the keyboard the screen currently owns, so that
	// global bindings never fire while they could be meant for the screen.
	Focus() focusLevel
	// Refresh reloads from the database after something changed.
	Refresh() error
}

// Model is the root of the interface.
type Model struct {
	store    *store.Store
	settings db.Settings
	path     string
	firstRun bool

	width, height int
	active        view
	screens       []screen

	showHelp bool
	status   string
	err      error
	quitting bool
}

// statusMsg reports the outcome of an action in the header, briefly.
type statusMsg string

// errMsg carries a failure to the root, which renders it without losing state.
type errMsg struct{ err error }

// refreshMsg asks every screen to reload, after data changed anywhere.
type refreshMsg struct{}

// switchViewMsg lets a screen hand control to another one, so that finishing an
// update can land the user back on the dashboard showing the result.
type switchViewMsg view

// New builds the interface over an open database.
func New(s *store.Store, path string, firstRun bool) (*Model, error) {
	settings, err := s.DB.Settings()
	if err != nil {
		return nil, err
	}
	m := &Model{store: s, settings: settings, path: path, firstRun: firstRun}
	m.screens = []screen{
		newDashboard(m),
		newPlanScreen(m),
		newUpdateScreen(m),
		newAccountsScreen(m),
		newHistoryScreen(m),
		newQueryScreen(m),
	}
	for _, sc := range m.screens {
		if err := sc.Refresh(); err != nil {
			return nil, err
		}
	}
	// A database with no accounts yet has nothing to show on the dashboard, so
	// start where the user has to start anyway.
	if firstRun || m.isEmpty() {
		m.active = viewAccounts
	}
	return m, nil
}

func (m *Model) isEmpty() bool {
	accts, err := m.store.Accounts(true)
	return err != nil || len(accts) == 0
}

// Init satisfies tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// money formatting helpers bound to the database's own preferences, so display
// follows the file rather than the machine it is opened on.
func (m *Model) fmt(a money.Amount) string {
	return fmtMoney(a, m.settings.Grouping, m.settings.Decimals)
}
func (m *Model) fmts(a money.Amount) string {
	return fmtSigned(a, m.settings.Grouping, m.settings.Decimals)
}
func (m *Model) compact(a money.Amount) string {
	return symbolFor(m.settings.Grouping) + a.Compact(m.settings.Grouping)
}

// Update satisfies tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case statusMsg:
		m.status, m.err = string(msg), nil
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case refreshMsg:
		for _, sc := range m.screens {
			if err := sc.Refresh(); err != nil {
				m.err = err
			}
		}
		return m, nil

	case switchViewMsg:
		m.active = view(msg)
		m.showHelp = false
		if err := m.screens[m.active].Refresh(); err != nil {
			m.err = err
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if cmd, handled := m.handleKey(key); handled {
			return m, cmd
		}
	}

	cmd := m.screens[m.active].Update(msg)
	return m, cmd
}

// handleKey deals with the global bindings.
//
// Every binding here is gated on the current focus level, so that a key never
// does one thing on one screen and something else on another. In particular
// tab and shift+tab are a pair: either both move between screens or neither
// does. Splitting them - which is what happens if only the bare keys are
// checked against a "capturing" flag - produces a form where tab moves to the
// next field and shift+tab leaves the screen entirely.
func (m *Model) handleKey(key tea.KeyMsg) (tea.Cmd, bool) {
	// ctrl+c always quits, even mid-edit, because an escape hatch that depends
	// on application state is not an escape hatch.
	if key.Type == tea.KeyCtrlC {
		m.quitting = true
		return tea.Quit, true
	}

	if m.showHelp {
		switch key.String() {
		case "?", "esc", "q", "enter":
			m.showHelp = false
			return nil, true
		}
		return nil, true
	}

	// Clear a stale status line on the next interaction rather than on a timer,
	// so a message is never missed and never lingers.
	m.status, m.err = "", nil

	// Chorded screen switching works at every focus level, so there is always a
	// way out of a form that no screen can shadow. The footer advertises it
	// whenever the bare keys are unavailable.
	switch key.String() {
	case "ctrl+right", "ctrl+n":
		m.nextView()
		return nil, true
	case "ctrl+left", "ctrl+p":
		m.prevView()
		return nil, true
	}

	focus := m.screens[m.active].Focus()

	// Help is safe to open whenever '?' cannot be text.
	if focus != focusTyping && key.String() == "?" {
		m.showHelp = true
		return nil, true
	}

	// Anything else belongs to the screen while it is doing something. esc is
	// never global: it always means "back out one step", which is the screen's
	// business, and a global esc would make it mean two things at once.
	if focus != focusIdle {
		return nil, false
	}

	switch key.String() {
	case "q":
		m.quitting = true
		return tea.Quit, true
	case "tab":
		m.nextView()
		return nil, true
	case "shift+tab":
		m.prevView()
		return nil, true
	case "1", "2", "3", "4", "5", "6":
		n := int(key.String()[0] - '1')
		if n < len(m.screens) {
			m.active = view(n)
			if err := m.screens[m.active].Refresh(); err != nil {
				m.err = err
			}
			return nil, true
		}
	case "r":
		return func() tea.Msg { return refreshMsg{} }, true
	}
	return nil, false
}

func (m *Model) nextView() {
	m.active = view((int(m.active) + 1) % len(m.screens))
	m.screens[m.active].Refresh()
}

func (m *Model) prevView() {
	m.active = view((int(m.active) + len(m.screens) - 1) % len(m.screens))
	m.screens[m.active].Refresh()
}

// View satisfies tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "starting…"
	}
	if m.showHelp {
		return m.helpView()
	}

	header := m.header()
	// The way out is always on screen, in its own column on the right so that a
	// crowded screen truncates its own hints before it truncates the escape.
	// Which keys those are depends on how much of the keyboard the screen owns.
	var globals []hint
	switch m.screens[m.active].Focus() {
	case focusIdle:
		globals = []hint{{"⇥", "screen"}, {"?", "help"}, {"q", "quit"}}
	case focusBusy:
		globals = []hint{{"ctrl+←→", "screen"}, {"?", "help"}, {"ctrl+c", "quit"}}
	default:
		globals = []hint{{"ctrl+←→", "screen"}, {"ctrl+c", "quit"}}
	}
	foot := footer(m.width, m.screens[m.active].Hints(), globals)

	statusLine := ""
	switch {
	case m.err != nil:
		statusLine = styError.Render("✗ " + m.err.Error())
	case m.status != "":
		statusLine = styGain.Render("✓ " + m.status)
	}

	// Reserve: header (2 lines + blank), status (1), blank, footer (1).
	bodyHeight := m.height - lipgloss.Height(header) - 3
	body := m.screens[m.active].View(m.width, bodyHeight)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(body)

	// Push the footer to the bottom so it never moves between screens.
	used := lipgloss.Height(header) + 1 + lipgloss.Height(body)
	for i := used; i < m.height-2; i++ {
		b.WriteString("\n")
	}
	b.WriteString(statusLine + "\n")
	b.WriteString(foot)
	return b.String()
}

// header shows where you are and, on the right, which file you are looking at -
// which matters when the whole point is that the database can be swapped.
func (m *Model) header() string {
	// The digits are only shown while they work. A label that keeps advertising
	// "3 Update" when the screen has the keyboard teaches the wrong lesson the
	// first time it is pressed and ignored.
	live := m.screens[m.active].Focus() == focusIdle
	var tabs []string
	for i, name := range viewNames {
		label := name
		if live {
			label = fmt.Sprintf("%d %s", i+1, name)
		}
		if view(i) == m.active {
			tabs = append(tabs, styTabActive.Render(label))
		} else if live {
			tabs = append(tabs, styTab.Render(label))
		} else {
			tabs = append(tabs, styTabMuted.Render(label))
		}
	}
	left := strings.Join(tabs, "")
	right := styFaint.Render(shortPath(m.path))
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right + "\n" +
		styFaint.Render(strings.Repeat("─", m.width))
}

func shortPath(p string) string {
	if len(p) <= 46 {
		return p
	}
	return "…" + p[len(p)-45:]
}

func (m *Model) helpView() string {
	var b strings.Builder
	b.WriteString(styTitle.Render("networth — keys") + "\n\n")

	sections := []struct {
		name  string
		items []hint
	}{
		{"Anywhere", []hint{
			{"ctrl+← ctrl+→", "previous or next screen — always works"},
			{"ctrl+c", "quit — always works"},
			{"?", "this help"},
		}},
		{"When nothing is open", []hint{
			{"1 – 6", "jump straight to a screen"},
			{"tab / shift+tab", "next or previous screen"},
			{"r", "reload from the database"},
			{"q", "quit"},
		}},
		{"Dashboard", []hint{
			{"↑ ↓", "move through accounts"},
			{"u", "start a balance update"},
			{"p", "change the period being compared"},
		}},
		{"Plan (monthly)", []hint{
			{"type an amount", "the routing updates as you type"},
			{"↑ ↓ then enter", "override one line for this month"},
			{"ctrl+s", "save and record where the money went"},
			{"ctrl+r", "edit the routing rules"},
			{"ctrl+d", "change the month"},
			{"ctrl+z", "undo a month you already recorded"},
		}},
		{"Update", []hint{
			{"enter", "confirm the field and move on"},
			{"tab / shift+tab", "switch between balance and amount added"},
			{"↑ ↓", "previous or next account"},
			{"s", "skip this account, leaving it unchanged"},
			{"ctrl+s", "go to the review screen"},
			{"esc", "leave the round (twice, if anything is entered)"},
		}},
		{"Forms (accounts, rules)", []hint{
			{"tab / shift+tab", "next or previous field"},
			{"↑ ↓", "next or previous field"},
			{"← → / space", "choose, on a field with fixed options"},
			{"type then ↓", "complete, on a field that refers to something"},
			{"ctrl+s", "save"},
			{"esc", "close the list, or cancel the form"},
		}},
		{"Accounts", []hint{
			{"a", "add an account"},
			{"e", "edit the selected account"},
			{"c", "close the selected account"},
			{"d", "delete (only if it has no history)"},
			{"/", "filter"},
		}},
		{"Query", []hint{
			{"enter", "run the selected query"},
			{"e", "edit its SQL"},
			{"n", "write a new query"},
			{"s", "save the query you just edited"},
		}},
	}
	for _, s := range sections {
		b.WriteString(styHeading.Render(s.name) + "\n")
		for _, h := range s.items {
			b.WriteString("  " + pad(styKey.Render(h.key), 26, false) + styKeyDesc.Render(h.desc) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(styHeading.Render("How the keyboard works") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"One rule: while something is open - a form, a list, a field - the screen owns the "+
			"keyboard, and the plain keys mean what the footer says they mean. Tab moves "+
			"between fields, not between screens, and esc backs out one step. The tab bar dims "+
			"and its numbers disappear while this is the case, so you can see it rather than "+
			"discover it. ctrl+← and ctrl+→ change screen at any time, from anywhere.",
		min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Two rhythms") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Plan monthly, update balances a couple of times a year. The monthly plan is what "+
			"makes the rest accurate: recording where income went means contributions are a "+
			"record rather than a recollection, so the split between what you added and what "+
			"you earned can be trusted.", min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Typing amounts") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Any amount field accepts arithmetic, so you can type what you actually know: "+
			"450000+62000, 1200*12, (300000+150000)/2, or 8% of a figure. Commas, spaces and "+
			"currency symbols are ignored, and (1000) means -1000.", min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Where your data lives") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Everything - accounts, balances, settings and this application's own state - is in "+
			"the single SQLite file shown in the top right. Nothing is stored anywhere else, so "+
			"copying that file moves your whole setup, and opening someone else's works with no "+
			"configuration. You can open it in any SQL client at the same time; the Query screen "+
			"exists so you rarely need to.", min(m.width-2, 76))) + "\n\n")

	b.WriteString(styFaint.Render("press any key to go back"))
	return b.String()
}

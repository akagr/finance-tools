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
// Two rules follow from it, and they pull in opposite directions:
//
//   - A global binding may only fire where it cannot possibly mean something to
//     the screen. Otherwise q quits in the middle of a half-finished round.
//   - Getting off a screen must never be harder than getting onto it. A screen
//     that has to be escaped before it can be left is a trap, and the fact that
//     the escape is on screen does not make it less of one.
//
// So the levels are graded by what could be text, not by how busy the screen
// looks: letters stand down first, because a screen full of single-letter
// actions is common; tab and the digits stand down only for an actual text
// field, where they genuinely are input.
type focusLevel int

const (
	// focusIdle: the screen wants nothing in particular. Every global binding
	// works, including the bare letters - q, r, ?.
	focusIdle focusLevel = iota
	// focusBusy: something is open - a list, a wizard step - so letters may mean
	// something here and stand down. Navigation does not: tab and the digits
	// still change screen, because leaving is always safe. Every screen keeps
	// its state when it loses focus, so coming back resumes exactly where you
	// were; only quitting outright would lose anything, and that is precisely
	// the binding this level withholds.
	focusBusy
	// focusTyping: a text field has the keyboard, so tab and the digits are
	// input like any other character and only chords stay global. Every state
	// that reports this must offer esc as a way back to a lower level, or the
	// screen becomes a trap.
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

	showHelp   bool
	helpScroll int
	status     string
	err        error
	quitting   bool
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
		m.showHelp, m.helpScroll = false, 0
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
		case "up", "k":
			m.helpScroll--
		case "down", "j":
			m.helpScroll++
		case "pgup":
			m.helpScroll -= max(1, m.height-4)
		case "pgdown", " ":
			m.helpScroll += max(1, m.height-4)
		default:
			m.showHelp, m.helpScroll = false, 0
		}
		if m.helpScroll < 0 {
			m.helpScroll = 0
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

	// Navigation is not text unless a field is actually taking text, so it keeps
	// working while a screen is merely busy. This is what stops a screen from
	// becoming a trap, and it costs nothing: leaving preserves the screen's
	// state, so coming back resumes where you were.
	if focus != focusTyping {
		switch key.String() {
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
		case "?":
			m.showHelp = true
			return nil, true
		}
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
		globals = []hint{{"⇥", "screen"}, {"?", "help"}, {"ctrl+c", "quit"}}
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
	// The digits are only shown while they work, which is everywhere except in
	// a text field, where they are input. A label that keeps advertising
	// "3 Update" when the digit will be typed into a field teaches the wrong
	// lesson the first time it is pressed.
	live := m.screens[m.active].Focus() != focusTyping
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

// helpView is the only screen allowed to be longer than the terminal, so it is
// the only one that scrolls. A help page whose top has scrolled off is worse
// than no help at all: it is read by someone who is already stuck.
func (m *Model) helpView() string {
	body := strings.Split(m.helpBody(), "\n")
	avail := max(3, m.height-1)
	if len(body) <= avail {
		m.helpScroll = 0
		return strings.Join(body, "\n")
	}
	m.helpScroll = clampInt(m.helpScroll, 0, len(body)-avail)
	window := body[m.helpScroll : m.helpScroll+avail]

	more := ""
	if m.helpScroll+avail < len(body) {
		more = "  ·  more below"
	}
	return strings.Join(window, "\n") + "\n" +
		styFaint.Render("↑↓ pgup pgdn scroll"+more+"  ·  any other key to go back")
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func (m *Model) helpBody() string {
	var b strings.Builder
	b.WriteString(styTitle.Render("networth — keys") + "\n\n")

	sections := []struct {
		name  string
		items []hint
	}{
		{"Anywhere at all", []hint{
			{"ctrl+← ctrl+→", "previous or next screen"},
			{"ctrl+c", "quit"},
		}},
		{"Unless a field is taking text", []hint{
			{"1 – 6", "jump straight to a screen"},
			{"tab / shift+tab", "next or previous screen"},
			{"?", "this help"},
		}},
		{"When nothing at all is open", []hint{
			{"r", "reload from the database"},
			{"q", "quit"},
		}},
		{"Dashboard", []hint{
			{"↑ ↓", "move through accounts"},
			{"u", "start a balance update"},
			{"p", "change the comparison period"},
		}},
		{"Plan (monthly)", []hint{
			{"type an amount", "the routing updates as you type"},
			{"↑ ↓ then enter", "override a line this month"},
			{"ctrl+s", "save and record the money moved"},
			{"ctrl+r", "edit the routing rules"},
			{"ctrl+d", "change the month"},
			{"ctrl+z", "undo a recorded month"},
		}},
		{"Update", []hint{
			{"enter", "confirm the field and move on"},
			{"tab / shift+tab", "balance or amount added"},
			{"↑ ↓", "previous or next account"},
			{"ctrl+s", "go to the review screen"},
			{"esc", "step out; again to leave the round"},
			{"enter", "step back into them"},
		}},
		{"Forms (accounts, rules)", []hint{
			{"tab / shift+tab", "next or previous field"},
			{"↑ ↓", "next or previous field"},
			{"← → / space", "choose, on a field with fixed options"},
			{"type then ↓", "complete a reference to something"},
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
	// Two columns when there is room for them. Nine sections down one column is
	// two screenfuls, and a key list you have to scroll to read is a key list
	// that gets skimmed.
	render := func(sel []struct {
		name  string
		items []hint
	}) string {
		var c strings.Builder
		for _, s := range sel {
			c.WriteString(styHeading.Render(s.name) + "\n")
			for _, h := range s.items {
				c.WriteString("  " + pad(styKey.Render(h.key), 17, false) +
					styKeyDesc.Render(h.desc) + "\n")
			}
			c.WriteString("\n")
		}
		return c.String()
	}
	twoUp := false
	if m.width >= 100 {
		// Split where the two columns come out closest to the same height,
		// rather than at the middle section: the sections are wildly different
		// lengths, and an even split leaves one column three times the other.
		total := 0
		for _, sec := range sections {
			total += len(sec.items) + 2
		}
		split, running := 1, 0
		for i, sec := range sections {
			if running+len(sec.items)+2 > total/2 {
				split = max(1, i)
				break
			}
			running += len(sec.items) + 2
		}
		right := render(sections[split:])
		// Size the gutter to the content rather than the window, so the two
		// columns stay close enough to read as a pair.
		leftCol := lipgloss.Width(render(sections[:split])) + 3
		if leftCol+lipgloss.Width(right) <= m.width {
			left := lipgloss.NewStyle().Width(leftCol).Render(render(sections[:split]))
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
			b.WriteString("\n")
			twoUp = true
		}
	}
	if !twoUp {
		b.WriteString(render(sections))
	}

	b.WriteString(styHeading.Render("How the keyboard works") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"While something is open - a form, a list, a wizard step - the plain letters belong "+
			"to the screen. The numbers and tab still change screen, because leaving is never "+
			"destructive: every screen keeps its state. Only a text field takes the numbers and "+
			"tab too, and the tab bar dims to say so; esc steps back out of it, and never "+
			"leaves a screen in the same press that it stops typing.",
		min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Two rhythms") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Plan monthly, update balances a couple of times a year. Recording where income went "+
			"is what makes contributions a record rather than a recollection, so the split "+
			"between what you added and what you earned can be trusted.",
		min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Typing amounts") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Any amount field accepts arithmetic: 450000+62000, 1200*12, (300000+150000)/2. "+
			"Commas, spaces and currency symbols are ignored, and (1000) means -1000.",
		min(m.width-2, 76))) + "\n\n")

	b.WriteString(styHeading.Render("Where your data lives") + "\n")
	b.WriteString(styMuted.Render(wrap(
		"Everything - accounts, balances, settings and this application's own state - is in "+
			"the single SQLite file named in the top right. Copying it moves your whole setup, "+
			"and opening someone else's works with no configuration.",
		min(m.width-2, 76))) + "\n\n")

	return strings.TrimRight(b.String(), "\n")
}

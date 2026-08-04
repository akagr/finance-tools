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

const (
	viewDashboard view = iota
	viewUpdate
	viewAccounts
	viewHistory
	viewQuery
)

var viewNames = []string{"Dashboard", "Update", "Accounts", "History", "Query"}

// screen is one tab of the interface.
type screen interface {
	// Update handles a message and may return a command.
	Update(msg tea.Msg) tea.Cmd
	// View renders the body, given the space left after the header and footer.
	View(width, height int) string
	// Hints are the keys currently available, shown in the footer.
	Hints() []hint
	// Capturing reports whether the screen is taking raw text input, in which
	// case global single-letter shortcuts must not steal keystrokes.
	Capturing() bool
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

// handleKey deals with the global bindings, and is careful never to swallow a
// keystroke meant for a text field.
func (m *Model) handleKey(key tea.KeyMsg) (tea.Cmd, bool) {
	capturing := m.screens[m.active].Capturing()

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

	if capturing {
		// While typing, only unambiguous non-text keys are global.
		switch key.String() {
		case "ctrl+left", "shift+tab":
			m.prevView()
			return nil, true
		case "ctrl+right":
			m.nextView()
			return nil, true
		}
		return nil, false
	}

	switch key.String() {
	case "q":
		m.quitting = true
		return tea.Quit, true
	case "?":
		m.showHelp = true
		return nil, true
	case "tab", "ctrl+right":
		m.nextView()
		return nil, true
	case "shift+tab", "ctrl+left":
		m.prevView()
		return nil, true
	case "1", "2", "3", "4", "5":
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
	hints := append(m.screens[m.active].Hints(), hint{"?", "help"}, hint{"q", "quit"})
	foot := footer(m.width, hints)

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
	var tabs []string
	for i, name := range viewNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if view(i) == m.active {
			tabs = append(tabs, styTabActive.Render(label))
		} else {
			tabs = append(tabs, styTab.Render(label))
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
			{"1 – 5", "jump straight to a screen"},
			{"tab / shift+tab", "next or previous screen"},
			{"r", "reload from the database"},
			{"?", "this help"},
			{"q / ctrl+c", "quit"},
		}},
		{"Dashboard", []hint{
			{"↑ ↓", "move through accounts"},
			{"u", "start a balance update"},
			{"p", "change the period being compared"},
		}},
		{"Update", []hint{
			{"enter", "confirm the field and move on"},
			{"tab", "switch between balance and amount added"},
			{"↑ ↓", "previous or next account"},
			{"s", "skip this account, leaving it unchanged"},
			{"ctrl+s", "go to the review screen"},
			{"esc", "abandon the round"},
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

package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// accountsScreen manages the accounts themselves, and is where the one concept
// that must be understood is taught: whether an account counts towards net
// worth. Everything else - what is a contribution, what is a transfer, what is
// spending - follows from that flag, so the screen explains it in place rather
// than assuming it was read somewhere.
type accountsScreen struct {
	m *Model

	accounts     []store.Account
	last         map[int64]store.LastSnapshot
	cursor       int
	showClosed   bool
	filter       string
	filterInput  textinput.Model
	filtering    bool
	form         *accountForm
	confirmClose bool
}

func newAccountsScreen(m *Model) *accountsScreen {
	f := textinput.New()
	f.Prompt = "/"
	f.CharLimit = 40
	return &accountsScreen{m: m, filterInput: f}
}

func (s *accountsScreen) Refresh() error {
	accts, err := s.m.store.Accounts(s.showClosed)
	if err != nil {
		return err
	}
	s.accounts = accts
	if s.last, err = s.m.store.LastSnapshots(); err != nil {
		return err
	}
	if s.cursor >= len(s.visible()) {
		s.cursor = max(0, len(s.visible())-1)
	}
	return nil
}

func (s *accountsScreen) visible() []store.Account {
	if s.filter == "" {
		return s.accounts
	}
	needle := strings.ToLower(s.filter)
	var out []store.Account
	for _, a := range s.accounts {
		hay := strings.ToLower(a.Name + " " + a.Institution + " " + a.Kind)
		for _, v := range a.Tags {
			hay += " " + strings.ToLower(v)
		}
		if strings.Contains(hay, needle) {
			out = append(out, a)
		}
	}
	return out
}

func (s *accountsScreen) Capturing() bool {
	return s.filtering || s.form != nil
}

func (s *accountsScreen) Hints() []hint {
	if s.form != nil {
		return s.form.Hints()
	}
	if s.filtering {
		return []hint{{"↵", "apply"}, {"esc", "clear"}}
	}
	return []hint{
		{"a", "add"}, {"e", "edit"}, {"c", "close"}, {"d", "delete"},
		{"/", "filter"}, {"h", "show closed"},
	}
}

func (s *accountsScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if s.form != nil {
		done, cmd := s.form.Update(key)
		if done {
			s.form = nil
			if err := s.Refresh(); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
		}
		return cmd
	}

	if s.filtering {
		switch key.String() {
		case "enter":
			s.filter = s.filterInput.Value()
			s.filtering = false
			s.filterInput.Blur()
			s.cursor = 0
			return nil
		case "esc":
			s.filter, s.filtering = "", false
			s.filterInput.SetValue("")
			s.filterInput.Blur()
			return nil
		}
		var cmd tea.Cmd
		s.filterInput, cmd = s.filterInput.Update(key)
		s.filter = s.filterInput.Value()
		return cmd
	}

	rows := s.visible()
	switch key.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		s.confirmClose = false
	case "down", "j":
		if s.cursor < len(rows)-1 {
			s.cursor++
		}
		s.confirmClose = false
	case "/":
		s.filtering = true
		s.filterInput.Focus()
		return nil
	case "h":
		s.showClosed = !s.showClosed
		return func() tea.Msg { return refreshMsg{} }
	case "a":
		s.form = newAccountForm(s.m, store.Account{
			Kind: "asset", Currency: s.m.settings.BaseCurrency,
			InNetworth: true, Tracking: "snapshots",
		}, false)
		return nil
	case "e":
		if s.cursor < len(rows) {
			s.form = newAccountForm(s.m, rows[s.cursor], true)
		}
		return nil
	case "c":
		if s.cursor < len(rows) {
			a := rows[s.cursor]
			if a.Closed() {
				a.ClosedOn = ""
			} else {
				a.ClosedOn = today()
			}
			if err := s.m.store.UpdateAccount(a); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return tea.Batch(
				func() tea.Msg { return refreshMsg{} },
				func() tea.Msg { return statusMsg(a.Name + " updated") })
		}
	case "d":
		if s.cursor < len(rows) {
			a := rows[s.cursor]
			if err := s.m.store.DeleteAccount(a.ID); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return tea.Batch(
				func() tea.Msg { return refreshMsg{} },
				func() tea.Msg { return statusMsg(a.Name + " deleted") })
		}
	}
	return nil
}

func (s *accountsScreen) View(width, height int) string {
	if s.form != nil {
		return s.form.View(width, height)
	}

	rows := s.visible()
	var b strings.Builder
	head := "  " + styHeading.Render("Accounts")
	if s.filtering || s.filter != "" {
		head += "   " + s.filterInput.View()
	}
	b.WriteString(head + "\n")

	if len(rows) == 0 {
		b.WriteString("\n" + styMuted.Render(wrapIndent(
			"No accounts yet. Press a to add one.\n\n"+
				"Add everything you own and owe: bank accounts, funds, provident fund, "+
				"property, loans. Anything you would have put in a row of the spreadsheet.",
			min(width-4, 76), "  ")) + "\n")
		return b.String()
	}

	nameW := max(16, min(30, width-62))
	cols := []column{
		{title: "Account", width: nameW},
		{title: "Kind", width: 10},
		{title: "Cur", width: 4},
		{title: "In net worth", width: 12},
		{title: "Latest", width: 14, right: true},
		{title: "Valued", width: 11},
	}
	var cells [][]string
	var styles [][]lipgloss.Style
	for _, a := range rows {
		inNW, inNWStyle := "yes", styGain
		if !a.InNetworth {
			inNW, inNWStyle = "no", styFaint
		}
		latest, valued := "—", "never"
		valuedStyle := styWarn
		if ls, ok := s.last[a.ID]; ok {
			latest, valued = s.m.fmt(ls.Balance), ls.AsOf
			valuedStyle = styFaint
			if daysSince(ls.AsOf) > 200 {
				valuedStyle = styWarn
			}
		}
		name := a.Name
		nameStyle := lipgloss.NewStyle()
		if a.Closed() {
			name += " (closed)"
			nameStyle = styFaint
		}
		cells = append(cells, []string{name, a.Kind, a.Currency, inNW, latest, valued})
		styles = append(styles, []lipgloss.Style{
			nameStyle, styMuted, styMuted, inNWStyle, lipgloss.NewStyle(), valuedStyle,
		})
	}
	b.WriteString(renderTable(cols, cells, styles, s.cursor, true))

	if s.cursor < len(rows) {
		b.WriteString("\n" + s.detail(rows[s.cursor], width))
	}
	return b.String()
}

func (s *accountsScreen) detail(a store.Account, width int) string {
	var b strings.Builder
	var bits []string
	if a.Institution != "" {
		bits = append(bits, a.Institution)
	}
	bits = append(bits, "tracked by "+a.Tracking)
	for k, v := range a.Tags {
		bits = append(bits, k+"="+v)
	}
	b.WriteString("  " + styFaint.Render(strings.Join(bits, "  ·  ")) + "\n")
	if a.Notes != "" {
		b.WriteString("  " + styMuted.Render(wrap(a.Notes, min(width-4, 76))) + "\n")
	}
	return b.String()
}

// accountForm edits one account. Each field carries a one-line explanation,
// because these choices decide how every later number is computed and are not
// obvious from the field name alone.
type accountForm struct {
	m       *Model
	account store.Account
	editing bool
	field   int
	inputs  []textinput.Model
	err     string
}

var accountFields = []struct {
	label string
	help  string
}{
	{"Name", "what you call it — must be unique"},
	{"Kind", "asset, liability, income, expense or equity"},
	{"Currency", "the currency the balance is held in"},
	{"Counts towards net worth", "yes for things you own or owe; no for salary, spending, or an employer pool you do not control yet"},
	{"Institution", "optional — the bank, broker or provider"},
	{"Asset class", "optional — equity, debt, cash, property, gold…"},
	{"Notes", "optional"},
}

func newAccountForm(m *Model, a store.Account, editing bool) *accountForm {
	f := &accountForm{m: m, account: a, editing: editing}
	values := []string{
		a.Name, a.Kind, a.Currency, boolWord(a.InNetworth),
		a.Institution, a.Tags["asset_class"], a.Notes,
	}
	for i, v := range values {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 80
		in.SetValue(v)
		if i == 0 {
			in.Focus()
		}
		f.inputs = append(f.inputs, in)
	}
	return f
}

func (f *accountForm) Hints() []hint {
	return []hint{{"↑↓ / ⇥", "field"}, {"ctrl+s", "save"}, {"esc", "cancel"}}
}

func (f *accountForm) Update(key tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch key.String() {
	case "esc":
		return true, nil
	case "ctrl+s", "enter":
		if key.String() == "enter" && f.field < len(f.inputs)-1 {
			f.focus(f.field + 1)
			return false, nil
		}
		if err := f.save(); err != nil {
			f.err = err.Error()
			return false, nil
		}
		name := f.inputs[0].Value()
		return true, tea.Batch(
			func() tea.Msg { return refreshMsg{} },
			func() tea.Msg { return statusMsg(name + " saved") })
	case "tab", "down":
		f.focus((f.field + 1) % len(f.inputs))
		return false, nil
	case "shift+tab", "up":
		f.focus((f.field + len(f.inputs) - 1) % len(f.inputs))
		return false, nil
	}
	var c tea.Cmd
	f.inputs[f.field], c = f.inputs[f.field].Update(key)
	f.err = ""
	return false, c
}

func (f *accountForm) focus(i int) {
	f.inputs[f.field].Blur()
	f.field = i
	f.inputs[f.field].Focus()
	f.inputs[f.field].CursorEnd()
}

func (f *accountForm) save() error {
	a := f.account
	a.Name = strings.TrimSpace(f.inputs[0].Value())
	if a.Name == "" {
		return fmt.Errorf("an account needs a name")
	}
	a.Kind = strings.ToLower(strings.TrimSpace(f.inputs[1].Value()))
	switch a.Kind {
	case "asset", "liability", "income", "expense", "equity":
	default:
		return fmt.Errorf("kind must be asset, liability, income, expense or equity")
	}
	a.Currency = strings.ToUpper(strings.TrimSpace(f.inputs[2].Value()))
	if a.Currency == "" {
		a.Currency = f.m.settings.BaseCurrency
	}
	a.InNetworth = parseBoolWord(f.inputs[3].Value())
	a.Institution = strings.TrimSpace(f.inputs[4].Value())
	if a.Tags == nil {
		a.Tags = map[string]string{}
	}
	if v := strings.TrimSpace(f.inputs[5].Value()); v != "" {
		a.Tags["asset_class"] = v
	} else {
		delete(a.Tags, "asset_class")
	}
	a.Notes = strings.TrimSpace(f.inputs[6].Value())

	if f.editing {
		return f.m.store.UpdateAccount(a)
	}
	_, err := f.m.store.AddAccount(a)
	return err
}

func (f *accountForm) View(width, height int) string {
	var b strings.Builder
	title := "New account"
	if f.editing {
		title = "Edit " + f.account.Name
	}
	b.WriteString("\n  " + styHeading.Render(title) + "\n\n")

	for i, in := range f.inputs {
		style, marker := styFieldLabel, "  "
		if i == f.field {
			style, marker = styFieldActive, styKey.Render("▸ ")
		}
		b.WriteString(marker + style.Render(pad(accountFields[i].label, 26, false)) + in.View() + "\n")
		if i == f.field {
			b.WriteString("    " + pad("", 26, false) +
				styFaint.Render(wrap(accountFields[i].help, min(width-34, 62))) + "\n")
		}
	}
	if f.err != "" {
		b.WriteString("\n  " + styError.Render(f.err) + "\n")
	}
	return b.String()
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func parseBoolWord(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1":
		return true
	}
	return false
}

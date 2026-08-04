package tui

import (
	"fmt"
	"sort"
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

// accountForm edits one account.
//
// Fields whose valid values are a fixed set are choices rather than text, and
// the currency completes against what is already in use. Typing a value that
// only fails on save is a decision turned into a spelling test.
type accountForm struct {
	m       *Model
	account store.Account
	editing bool
	field   int
	fields  []*field
	err     string
}

func newAccountForm(m *Model, a store.Account, editing bool) *accountForm {
	f := &accountForm{m: m, account: a, editing: editing}

	f.fields = []*field{
		newTextField("Name", "what you call it — must be unique"),
		newChoiceField("Kind", "asset and liability count towards net worth; "+
			"income, expense and equity are where money comes from and goes to",
			[]string{"asset", "liability", "income", "expense", "equity"}),
		newSuggestField("Currency", "the currency the balance is held in",
			m.currencies(), false),
		newChoiceField("Counts towards net worth",
			"yes for things you own or owe; no for salary, spending, or an "+
				"employer pool you do not control yet",
			[]string{"yes", "no"}),
		newChoiceField("Tracked by",
			"snapshots: you state the balance now and then. transactions: it is "+
				"the sum of what went in and out. units: quantity times price — "+
				"prices are not fetched yet, so this still asks for a balance.",
			[]string{"snapshots", "transactions", "units"}),
		newTextField("Institution", "optional — the bank, broker or provider"),
		newSuggestField("Tags", "key=value, comma separated. Any dimension you want to "+
			"slice or select by. Formulas select accounts with tag:key=value.",
			m.tagSuggestions(), false),
		newTextField("Notes", "optional"),
	}

	f.fields[0].SetValue(a.Name)
	f.fields[1].SetValue(a.Kind)
	f.fields[2].SetValue(a.Currency)
	f.fields[3].SetValue(boolWord(a.InNetworth))
	f.fields[4].SetValue(a.Tracking)
	f.fields[5].SetValue(a.Institution)
	f.fields[6].SetValue(formatTags(a.Tags))
	f.fields[7].SetValue(a.Notes)
	f.fields[0].Focus()
	return f
}

func (f *accountForm) Hints() []hint {
	cur := f.fields[f.field]
	h := []hint{{"↑↓ / ⇥", "field"}}
	switch cur.kind {
	case fieldChoice:
		h = []hint{{"←→ / space", "choose"}, {"↑↓", "field"}}
	case fieldSuggest:
		if cur.open {
			h = []hint{{"↑↓", "pick"}, {"⇥ / ↵", "accept"}, {"esc", "close"}}
		}
	}
	return append(h, hint{"ctrl+s", "save"}, hint{"esc", "cancel"})
}

func (f *accountForm) Update(key tea.KeyMsg) (done bool, cmd tea.Cmd) {
	// Save and cancel come first so they always work, whatever a field is doing.
	switch key.String() {
	case "ctrl+s":
		if err := f.save(); err != nil {
			f.err = err.Error()
			return false, nil
		}
		name := f.fields[0].Value()
		return true, tea.Batch(
			func() tea.Msg { return refreshMsg{} },
			func() tea.Msg { return statusMsg(name + " saved") })
	}

	if handled, c := f.fields[f.field].Update(key); handled {
		f.err = ""
		return false, c
	}

	switch key.String() {
	case "esc":
		return true, nil
	case "enter":
		if f.field < len(f.fields)-1 {
			f.focus(f.field + 1)
			return false, nil
		}
		if err := f.save(); err != nil {
			f.err = err.Error()
			return false, nil
		}
		return true, tea.Batch(
			func() tea.Msg { return refreshMsg{} },
			func() tea.Msg { return statusMsg(f.fields[0].Value() + " saved") })
	case "tab", "down":
		f.focus((f.field + 1) % len(f.fields))
	case "shift+tab", "up":
		f.focus((f.field + len(f.fields) - 1) % len(f.fields))
	}
	return false, nil
}

func (f *accountForm) focus(i int) {
	f.fields[f.field].Blur()
	f.field = i
	f.fields[f.field].Focus()
}

func (f *accountForm) save() error {
	a := f.account
	a.Name = strings.TrimSpace(f.fields[0].Value())
	if a.Name == "" {
		return fmt.Errorf("an account needs a name")
	}
	a.Kind = f.fields[1].Value()
	a.Currency = strings.ToUpper(strings.TrimSpace(f.fields[2].Value()))
	if a.Currency == "" {
		a.Currency = f.m.settings.BaseCurrency
	}
	a.InNetworth = f.fields[3].Value() == "yes"
	a.Tracking = f.fields[4].Value()
	a.Institution = strings.TrimSpace(f.fields[5].Value())

	tags, err := parseTagField(f.fields[6].Value())
	if err != nil {
		return err
	}
	a.Tags = tags
	a.Notes = strings.TrimSpace(f.fields[7].Value())

	if f.editing {
		return f.m.store.UpdateAccount(a)
	}
	_, err = f.m.store.AddAccount(a)
	return err
}

func (f *accountForm) View(width, height int) string {
	var b strings.Builder
	title := "New account"
	if f.editing {
		title = "Edit " + f.account.Name
	}
	b.WriteString("\n  " + styHeading.Render(title) + "\n\n")

	const labelW = 26
	for i, fl := range f.fields {
		b.WriteString(fl.View(labelW, i == f.field))
		b.WriteString(fl.Extra(labelW+2, 6))
		if i == f.field {
			b.WriteString(fl.Help(labelW+2, width))
		}
	}
	if f.err != "" {
		b.WriteString("\n  " + styError.Render(f.err) + "\n")
	}
	return b.String()
}

// currencies lists the currencies already in use, so the common case is one
// keystroke and a new one is still possible.
func (m *Model) currencies() []string {
	seen := map[string]bool{m.settings.BaseCurrency: true}
	if accts, err := m.store.Accounts(true); err == nil {
		for _, a := range accts {
			seen[a.Currency] = true
		}
	}
	var out []string
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// tagSuggestions offers the tags already in use, which is how a convention
// spreads without having to be remembered or written down.
func (m *Model) tagSuggestions() []string {
	seen := map[string]bool{}
	if accts, err := m.store.Accounts(true); err == nil {
		for _, a := range accts {
			for k, v := range a.Tags {
				seen[k+"="+v] = true
			}
		}
	}
	for _, common := range []string{
		"asset_class=equity", "asset_class=debt", "asset_class=cash",
		"asset_class=property", "type=credit_card", "liquidity=low", "owner=self",
	} {
		seen[common] = true
	}
	var out []string
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
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

// formatTags renders an account's tags back into the single field they are
// edited through.
func formatTags(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ", ")
}

// parseTagField reads "key=value, key=value".
//
// Tags are free-form on purpose: they are what a formula selects accounts by
// (tag:type=credit_card), and what every future way of slicing the portfolio
// will hang off, so the interface must not restrict them to a fixed list.
func parseTagField(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("tags look like key=value; %q has no =", part)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			return nil, fmt.Errorf("tags look like key=value; %q is incomplete", part)
		}
		out[k] = v
	}
	return out, nil
}

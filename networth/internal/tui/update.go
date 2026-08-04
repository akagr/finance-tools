package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// updateScreen is the round of balance entry that this whole tool is built
// around, and the one screen whose ergonomics decide whether the tool survives.
//
// Two things are asked for per account rather than one. The balance is obvious.
// The second - how much you paid in or took out since last time - is the small
// extra effort that buys the separation of contributions from returns. It is
// optional on every account, and skipping it degrades one figure rather than
// breaking the round.
type updateScreen struct {
	m *Model

	phase   updatePhase
	date    string
	entries map[int64]*draft

	accounts []store.Account
	last     map[int64]store.LastSnapshot
	idx      int
	field    int // 0 = balance, 1 = amount added

	balanceInput textinput.Model
	addedInput   textinput.Model
	dateInput    textinput.Model

	// recorded is what monthly plans have already logged as arriving in each
	// account since it was last valued. Showing it is what stops the same
	// contribution being entered twice, which would inflate contributions and
	// understate returns by the same amount.
	recorded map[int64]money.Amount

	confirmCancel bool
	fieldErr      string
}

type updatePhase int

const (
	phaseEntry updatePhase = iota
	phaseDate
	phaseReview
)

// draft is one account's pending entry, held until the whole round is saved.
type draft struct {
	balance    money.Amount
	hasBalance bool
	added      money.Amount
}

func newUpdateScreen(m *Model) *updateScreen {
	bal := textinput.New()
	bal.Prompt = ""
	bal.CharLimit = 40

	added := textinput.New()
	added.Prompt = ""
	added.CharLimit = 40
	added.Placeholder = "0"

	dt := textinput.New()
	dt.Prompt = ""
	dt.CharLimit = 10

	return &updateScreen{
		m:            m,
		date:         today(),
		entries:      map[int64]*draft{},
		balanceInput: bal,
		addedInput:   added,
		dateInput:    dt,
	}
}

func (u *updateScreen) Refresh() error {
	accts, err := u.m.store.Accounts(false)
	if err != nil {
		return err
	}
	// A unit-tracked account is valued from quantity times price, so it only
	// drops out of the round once it actually holds something. Until then it is
	// entered by hand like anything else, because an account that silently
	// disappears is worse than one that asks a redundant question.
	holdings, err := u.m.store.AccountsWithHoldings()
	if err != nil {
		return err
	}
	var tracked []store.Account
	for _, a := range accts {
		if a.Name == store.ExternalAccount || a.Name == store.IncomeAccount ||
			a.Name == store.SpendAccount {
			continue
		}
		if a.Tracking == "units" && holdings[a.ID] {
			continue
		}
		tracked = append(tracked, a)
	}
	u.accounts = tracked

	if u.last, err = u.m.store.LastSnapshots(); err != nil {
		return err
	}
	if u.recorded, err = u.m.store.RecordedContributions(); err != nil {
		return err
	}
	if u.idx >= len(u.accounts) {
		u.idx = 0
	}
	u.syncInputs()
	return nil
}

// syncInputs loads the current account's draft into the visible fields.
func (u *updateScreen) syncInputs() {
	if u.idx >= len(u.accounts) {
		return
	}
	a := u.accounts[u.idx]
	d := u.entries[a.ID]
	if d != nil && d.hasBalance {
		u.balanceInput.SetValue(d.balance.Format(0, money.GroupNone))
	} else {
		u.balanceInput.SetValue("")
	}
	if d != nil && d.added != 0 {
		u.addedInput.SetValue(d.added.Format(0, money.GroupNone))
	} else {
		u.addedInput.SetValue("")
	}
	u.focusField()
}

func (u *updateScreen) focusField() {
	u.balanceInput.Blur()
	u.addedInput.Blur()
	if u.field == 0 {
		u.balanceInput.Focus()
		u.balanceInput.CursorEnd()
	} else {
		u.addedInput.Focus()
		u.addedInput.CursorEnd()
	}
}

func (u *updateScreen) Focus() focusLevel {
	if len(u.accounts) == 0 {
		return focusIdle
	}
	switch u.phase {
	case phaseReview:
		// Not typing, but an unsaved round is on screen: a bare q here would
		// quit and throw it away.
		return focusBusy
	default:
		return focusTyping
	}
}

func (u *updateScreen) Hints() []hint {
	switch u.phase {
	case phaseDate:
		return []hint{{"↵", "confirm date"}, {"esc", "back"}}
	case phaseReview:
		return []hint{{"↵", "save"}, {"e", "back to editing"}, {"esc", "back to editing"}}
	default:
		// esc is the only bare key that leaves this screen, so it says what it
		// will do rather than the ambiguous "cancel": the outcome should be
		// legible before the key is pressed, not after.
		leave := hint{"esc", "leave this round"}
		if u.dirty() || u.pendingInput() {
			leave = hint{"esc", "discard this round"}
		}
		return []hint{
			{"↵", "next"},
			{"⇥", "balance / added"},
			{"↑↓", "account"},
			{"ctrl+s", "save"},
			{"ctrl+d", "date"},
			leave,
		}
	}
}

func (u *updateScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch u.phase {
	case phaseDate:
		return u.updateDate(key)
	case phaseReview:
		return u.updateReview(key)
	default:
		return u.updateEntry(key)
	}
}

func (u *updateScreen) updateDate(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter":
		v := strings.TrimSpace(u.dateInput.Value())
		if _, err := parseDate(v); err != nil {
			u.fieldErr = "dates look like 2026-08-04"
			return nil
		}
		u.date, u.fieldErr = v, ""
		u.phase = phaseEntry
		u.focusField()
		return nil
	case "esc":
		u.phase, u.fieldErr = phaseEntry, ""
		u.focusField()
		return nil
	}
	var cmd tea.Cmd
	u.dateInput, cmd = u.dateInput.Update(key)
	return cmd
}

func (u *updateScreen) updateEntry(key tea.KeyMsg) tea.Cmd {
	if len(u.accounts) == 0 {
		if key.String() == "esc" {
			return func() tea.Msg { return switchViewMsg(viewAccounts) }
		}
		return nil
	}

	switch key.String() {
	case "esc":
		// A figure that has been typed counts as work even before it is
		// confirmed with enter. Without this, esc looks like it discards
		// nothing and quietly throws away what is on screen - which is exactly
		// what makes leaving the round feel like the screen jumped on its own.
		if (u.dirty() || u.pendingInput()) && !u.confirmCancel {
			u.confirmCancel = true
			u.fieldErr = "esc again to discard this round"
			return nil
		}
		u.reset()
		return func() tea.Msg { return switchViewMsg(viewDashboard) }

	case "ctrl+d":
		u.dateInput.SetValue(u.date)
		u.dateInput.Focus()
		u.dateInput.CursorEnd()
		u.phase = phaseDate
		return nil

	case "ctrl+s":
		if err := u.commitField(); err != nil {
			u.fieldErr = err.Error()
			return nil
		}
		if !u.dirty() {
			u.fieldErr = "nothing entered yet"
			return nil
		}
		u.phase = phaseReview
		u.balanceInput.Blur()
		u.addedInput.Blur()
		return nil

	case "tab", "shift+tab":
		if err := u.commitField(); err != nil {
			u.fieldErr = err.Error()
			return nil
		}
		u.field = 1 - u.field
		u.focusField()
		return nil

	case "enter":
		if err := u.commitField(); err != nil {
			u.fieldErr = err.Error()
			return nil
		}
		// Enter walks the natural path: balance, then how much was added, then
		// on to the next account. The last account rolls into the review rather
		// than dead-ending.
		if u.field == 0 {
			u.field = 1
			u.focusField()
			return nil
		}
		u.field = 0
		if u.idx == len(u.accounts)-1 {
			if u.dirty() {
				u.phase = phaseReview
				u.balanceInput.Blur()
				u.addedInput.Blur()
				return nil
			}
			u.fieldErr = "nothing entered yet"
			return nil
		}
		u.idx++
		u.syncInputs()
		return nil

	case "up":
		if err := u.commitField(); err != nil {
			u.fieldErr = err.Error()
			return nil
		}
		if u.idx > 0 {
			u.idx--
			u.field = 0
			u.syncInputs()
		}
		return nil

	case "down":
		if err := u.commitField(); err != nil {
			u.fieldErr = err.Error()
			return nil
		}
		if u.idx < len(u.accounts)-1 {
			u.idx++
			u.field = 0
			u.syncInputs()
		}
		return nil
	}

	u.confirmCancel, u.fieldErr = false, ""
	var cmd tea.Cmd
	if u.field == 0 {
		u.balanceInput, cmd = u.balanceInput.Update(key)
	} else {
		u.addedInput, cmd = u.addedInput.Update(key)
	}
	return cmd
}

// commitField parses whatever is in the focused field into the draft. An empty
// field is not an error: leaving a balance blank means "unchanged", which is the
// natural way to skip an account that has not moved.
func (u *updateScreen) commitField() error {
	if u.idx >= len(u.accounts) {
		return nil
	}
	a := u.accounts[u.idx]
	d := u.entries[a.ID]
	if d == nil {
		d = &draft{}
	}

	balText := strings.TrimSpace(u.balanceInput.Value())
	if balText == "" {
		d.hasBalance = false
	} else {
		v, err := money.Parse(balText)
		if err != nil {
			return fmt.Errorf("%s: %v", strings.ToLower(balanceLabel(a)), err)
		}
		d.balance, d.hasBalance = normaliseForKind(a, v), true
	}

	addedText := strings.TrimSpace(u.addedInput.Value())
	if addedText == "" {
		d.added = 0
	} else {
		v, err := money.Parse(addedText)
		if err != nil {
			return fmt.Errorf("amount added: %v", err)
		}
		d.added = v
	}

	if d.hasBalance || d.added != 0 {
		u.entries[a.ID] = d
	} else {
		delete(u.entries, a.ID)
	}
	u.fieldErr = ""
	return nil
}

func (u *updateScreen) dirty() bool {
	for _, d := range u.entries {
		if d.hasBalance || d.added != 0 {
			return true
		}
	}
	return false
}

// pendingInput reports whether something has been typed into the focused field
// but not yet confirmed. It is deliberately separate from dirty: saving must
// only ever act on committed figures, while leaving must account for uncommitted
// ones too.
func (u *updateScreen) pendingInput() bool {
	return strings.TrimSpace(u.balanceInput.Value()) != "" ||
		strings.TrimSpace(u.addedInput.Value()) != ""
}

func (u *updateScreen) reset() {
	u.entries = map[int64]*draft{}
	u.idx, u.field, u.phase = 0, 0, phaseEntry
	u.confirmCancel, u.fieldErr = false, ""
	u.date = today()
	u.balanceInput.SetValue("")
	u.addedInput.SetValue("")
}

func (u *updateScreen) updateReview(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter":
		if err := u.save(); err != nil {
			return func() tea.Msg { return errMsg{err} }
		}
		n := len(u.entries)
		date := u.date
		u.reset()
		return tea.Batch(
			func() tea.Msg { return refreshMsg{} },
			func() tea.Msg {
				return statusMsg(fmt.Sprintf("saved %d balance(s) as of %s", n, date))
			},
			func() tea.Msg { return switchViewMsg(viewDashboard) },
		)
	case "e", "esc":
		u.phase = phaseEntry
		u.focusField()
		return nil
	}
	return nil
}

func (u *updateScreen) save() error {
	var entries []store.Entry
	for _, a := range u.accounts {
		d := u.entries[a.ID]
		if d == nil {
			continue
		}
		bal := d.balance
		if !d.hasBalance {
			// A contribution was recorded without a new balance. Carry the last
			// known balance forward so the flow still has a valuation to sit
			// between, rather than silently dropping it.
			if ls, ok := u.last[a.ID]; ok {
				bal = ls.Balance
			}
		}
		entries = append(entries, store.Entry{
			AccountID:    a.ID,
			Balance:      bal,
			Currency:     a.Currency,
			Contribution: d.added,
		})
	}
	return u.m.store.RecordUpdate(u.date, entries)
}

func (u *updateScreen) View(width, height int) string {
	if len(u.accounts) == 0 {
		return "\n  " + styTitle.Render("No accounts to update yet.") + "\n\n" +
			styMuted.Render("  Press 3 to add some, then come back here.") + "\n"
	}
	switch u.phase {
	case phaseDate:
		return u.viewDate(width)
	case phaseReview:
		return u.viewReview(width, height)
	default:
		return u.viewEntry(width, height)
	}
}

func (u *updateScreen) viewDate(width int) string {
	var b strings.Builder
	b.WriteString("\n  " + styHeading.Render("Balances as of") + "\n\n")
	b.WriteString("  " + u.dateInput.View() + "\n\n")
	b.WriteString(styMuted.Render(wrapIndent(
		"Use the date the balances were true, not today's date, if you are catching up "+
			"on a statement from a while back.", min(width-4, 74), "  ")) + "\n")
	if u.fieldErr != "" {
		b.WriteString("\n  " + styError.Render(u.fieldErr) + "\n")
	}
	return b.String()
}

func (u *updateScreen) viewEntry(width, height int) string {
	a := u.accounts[u.idx]
	last := u.last[a.ID]
	var b strings.Builder

	title := styHeading.Render("Update balances")
	progress := styFaint.Render(fmt.Sprintf("%d of %d  ·  as of %s", u.idx+1, len(u.accounts), u.date))
	gap := max(1, width-lipgloss.Width(title)-lipgloss.Width(progress)-4)
	b.WriteString("  " + title + strings.Repeat(" ", gap) + progress + "\n\n")

	meta := []string{}
	if a.Institution != "" {
		meta = append(meta, a.Institution)
	}
	meta = append(meta, a.Kind, a.Currency)
	b.WriteString("  " + styTitle.Render(a.Name) + "   " +
		styFaint.Render(strings.Join(meta, " · ")) + "\n")

	if last.Found {
		b.WriteString("  " + styMuted.Render(fmt.Sprintf("last %s on %s (%s ago)",
			u.m.fmt(last.Balance), last.AsOf, humanDays(float64(daysSince(last.AsOf))))) + "\n")
	} else {
		b.WriteString("  " + styMuted.Render("no balance recorded yet") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(u.fieldRow(balanceLabel(a), u.balanceInput, u.field == 0))
	b.WriteString(u.previewLine(a, last))
	b.WriteString("\n")
	b.WriteString(u.fieldRow("of which added", u.addedInput, u.field == 1))
	if rec := u.recorded[a.ID]; rec != 0 {
		b.WriteString("  " + pad("", 16, false) + styGain.Render("✓ ") + styMuted.Render(
			u.m.fmts(rec)+" already recorded by your monthly plans — leave this blank") + "\n")
	} else {
		b.WriteString("  " + pad("", 16, false) + styFaint.Render(addedHelp(a)) + "\n")
	}

	if u.fieldErr != "" {
		b.WriteString("\n  " + styError.Render(u.fieldErr) + "\n")
	}

	b.WriteString("\n" + u.progressList(width, height))
	return b.String()
}

func (u *updateScreen) fieldRow(label string, in textinput.Model, active bool) string {
	style := styFieldLabel
	marker := "  "
	if active {
		style, marker = styFieldActive, styKey.Render("▸ ")
	}
	return marker + style.Render(pad(label, 16, false)) + in.View() + "\n"
}

// previewLine evaluates what has been typed, live. Showing the parsed figure and
// the change it implies turns a typo into something you notice while typing
// rather than three screens later.
func (u *updateScreen) previewLine(a store.Account, last store.LastSnapshot) string {
	indent := "  " + pad("", 16, false)
	text := strings.TrimSpace(u.balanceInput.Value())
	if text == "" {
		if last.Found {
			return indent + styFaint.Render("blank leaves it unchanged") + "\n"
		}
		return "\n"
	}
	v, err := money.Parse(text)
	if err != nil {
		return indent + styError.Render(err.Error()) + "\n"
	}
	v = normaliseForKind(a, v)
	line := indent + styMuted.Render("= "+u.m.fmt(v))
	if last.Found {
		delta := v - last.Balance
		change := u.m.fmts(delta)
		if last.Balance != 0 {
			change += fmt.Sprintf("  (%s)", fmtPctSigned(float64(delta)/float64(last.Balance.Abs())))
		}
		line += "   " + signStyle(int64(delta)).Render(change)
	}
	return line + "\n"
}

// progressList keeps the whole round visible, so it is always obvious what is
// done, what is left, and what can be jumped to.
func (u *updateScreen) progressList(width, height int) string {
	limit := max(3, height-18)
	var b strings.Builder
	b.WriteString("  " + styFaint.Render("This round") + "\n")

	start := 0
	if u.idx >= limit {
		start = u.idx - limit + 1
	}
	end := min(len(u.accounts), start+limit)
	for i := start; i < end; i++ {
		a := u.accounts[i]
		d := u.entries[a.ID]
		mark, markStyle := "·", styFaint
		detail := ""
		if d != nil && (d.hasBalance || d.added != 0) {
			mark, markStyle = "✓", styGain
			if d.hasBalance {
				detail = u.m.fmt(d.balance)
			}
			if d.added != 0 {
				detail += styFaint.Render("  added " + u.m.fmts(d.added))
			}
		}
		line := markStyle.Render(mark) + " " + pad(a.Name, max(14, min(28, width-40)), false) +
			styMuted.Render(detail)
		if i == u.idx {
			b.WriteString("  " + stySelected.Render("▸ ") + line + "\n")
		} else {
			b.WriteString("    " + line + "\n")
		}
	}
	if end < len(u.accounts) {
		b.WriteString("   " + styFaint.Render(fmt.Sprintf("… %d more", len(u.accounts)-end)) + "\n")
	}
	return b.String()
}

// viewReview shows the consequences before anything is written, including the
// contribution/return split the round produces. Seeing that split at the moment
// of entry is what makes the extra "of which added" field feel worth filling in.
func (u *updateScreen) viewReview(width, height int) string {
	var b strings.Builder
	b.WriteString("\n  " + styHeading.Render("Review") + styFaint.Render("  ·  as of "+u.date) + "\n\n")

	nameW := max(16, min(28, width-56))
	cols := []column{
		{title: "Account", width: nameW},
		{title: "Was", width: 14, right: true},
		{title: "Now", width: 14, right: true},
		{title: "Change", width: 14, right: true},
		{title: "Added", width: 12, right: true},
	}
	var cells [][]string
	var styles [][]lipgloss.Style

	var totalOld, totalNew, totalAdded money.Amount
	for _, a := range u.accounts {
		d := u.entries[a.ID]
		if d == nil {
			continue
		}
		last := u.last[a.ID]
		newBal := d.balance
		if !d.hasBalance {
			newBal = last.Balance
		}
		change := newBal - last.Balance
		totalOld += last.Balance
		totalNew += newBal
		totalAdded += d.added

		was := "—"
		if last.Found {
			was = u.m.fmt(last.Balance)
		}
		added := "—"
		if d.added != 0 {
			added = u.m.fmts(d.added)
		}
		cells = append(cells, []string{a.Name, was, u.m.fmt(newBal), u.m.fmts(change), added})
		styles = append(styles, []lipgloss.Style{
			lipgloss.NewStyle(), styMuted, lipgloss.NewStyle(),
			signStyle(int64(change)), styMuted,
		})
	}
	b.WriteString(renderTable(cols, cells, styles, -1, false))

	change := totalNew - totalOld
	earned := change - totalAdded
	b.WriteString("\n  " + styHeading.Render("What this round says") + "\n")
	row := func(label string, amt money.Amount, style lipgloss.Style, note string) {
		b.WriteString("    " + styFieldLabel.Render(pad(label, 20, false)) +
			style.Render(pad(u.m.fmts(amt), 16, true)))
		if note != "" {
			b.WriteString("   " + styFaint.Render(note))
		}
		b.WriteString("\n")
	}
	row("You put in", totalAdded, signStyle(int64(totalAdded)), "")
	row("Markets gave", earned, signStyle(int64(earned)), "what you earned")
	row("Change", change, signStyle(int64(change)), "")

	if totalAdded == 0 && change != 0 {
		b.WriteString("\n" + styMuted.Render(wrapIndent(
			"Nothing was recorded as added, so all of this change counts as growth. If you "+
				"did pay money in, press e and fill in \"of which added\" — otherwise your "+
				"returns will read higher than they were.", min(width-4, 76), "  ")) + "\n")
	}
	b.WriteString("\n  " + styKey.Render("↵") + styKeyDesc.Render(" save this round") +
		styFaint.Render("   ·   ") + styKey.Render("e") + styKeyDesc.Render(" keep editing") + "\n")
	return b.String()
}

// balanceLabel names the balance field in the terms the account is thought
// about. A loan is carried as a negative number so that net worth is a single
// sum, but nobody thinks of their mortgage as minus forty lakh.
func balanceLabel(a store.Account) string {
	if a.Kind == "liability" {
		return "Amount owed"
	}
	return "Balance"
}

// normaliseForKind turns what was typed into what must be stored.
//
// Entering a debt as a positive number is the natural thing to do and would
// otherwise flip net worth by twice the loan - a silent, enormous error. Rather
// than demand a minus sign that is easy to forget, a positive figure on a
// liability is read as the amount owed.
func normaliseForKind(a store.Account, v money.Amount) money.Amount {
	if a.Kind == "liability" && v > 0 {
		return -v
	}
	return v
}

// addedHelp explains the second field in terms that suit the account, because
// "money you added" means something different for a loan than for a fund.
func addedHelp(a store.Account) string {
	if a.Kind == "liability" {
		return "new borrowing, or repayment from outside your tracked accounts — usually blank"
	}
	return "money you paid in or took out since then — leave blank if none"
}

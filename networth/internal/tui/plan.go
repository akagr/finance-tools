package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/plan"
	"github.com/akagr/finance-tools/networth/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// planScreen is the monthly routine: income arrives, and this says where it
// goes.
//
// It is the most frequently used screen, which is why it sits second. It also
// quietly does the hard work for everything else: because executing a plan
// records where money actually moved, the contribution figure the whole tool
// depends on becomes a record rather than a twice-yearly recollection.
type planScreen struct {
	m *Model

	date      string
	incomeIn  textinput.Model
	amountIn  textinput.Model
	dateIn    textinput.Model
	mode      planMode
	cursor    int
	result    plan.Result
	overrides map[int64]money.Amount
	saved     store.Plan
	hasSaved  bool
	rules     []plan.Rule
	err       string
	editor    *ruleForm
}

type planMode int

const (
	planEntry planMode = iota
	planEditAmount
	planEditDate
	planRules
)

func newPlanScreen(m *Model) *planScreen {
	income := textinput.New()
	income.Prompt = ""
	income.CharLimit = 40

	amount := textinput.New()
	amount.Prompt = ""
	amount.CharLimit = 40

	dt := textinput.New()
	dt.Prompt = ""
	dt.CharLimit = 10

	return &planScreen{
		m: m, date: monthStart(today()),
		incomeIn: income, amountIn: amount, dateIn: dt,
		overrides: map[int64]money.Amount{},
	}
}

// monthStart normalises a date to the first of its month, since a plan covers a
// month rather than a day.
func monthStart(d string) string {
	if len(d) >= 7 {
		return d[:7] + "-01"
	}
	return d
}

func (p *planScreen) Refresh() error {
	rules, err := p.m.store.Rules()
	if err != nil {
		return err
	}
	p.rules = rules

	saved, found, err := p.m.store.PlanFor(p.date)
	if err != nil {
		return err
	}
	p.saved, p.hasSaved = saved, found
	if found && p.incomeIn.Value() == "" {
		p.incomeIn.SetValue(saved.Income.Format(0, money.GroupNone))
	}
	if p.mode == planEntry {
		p.incomeIn.Focus()
	}
	return p.recompute()
}

func (p *planScreen) recompute() error {
	income, err := p.income()
	if err != nil {
		p.result = plan.Result{}
		return nil
	}
	res, err := p.m.store.ComputePlan(income, p.overrides)
	if err != nil {
		return err
	}
	p.result = res
	return nil
}

func (p *planScreen) income() (money.Amount, error) {
	text := strings.TrimSpace(p.incomeIn.Value())
	if text == "" {
		return 0, fmt.Errorf("no income entered")
	}
	return money.Parse(text)
}

// Capturing reports whether keystrokes belong to a text field. It is false once
// the income field is blurred, so that the number keys advertised in the tab bar
// always have a way of working: esc leaves input mode, and navigation resumes.
// A screen that traps the cursor is a screen the user cannot get out of.
func (p *planScreen) Capturing() bool {
	switch p.mode {
	case planRules:
		return p.editor != nil
	case planEditAmount, planEditDate:
		return true
	default:
		return p.incomeIn.Focused()
	}
}

func (p *planScreen) Hints() []hint {
	switch p.mode {
	case planEditAmount:
		return []hint{{"↵", "set amount"}, {"esc", "cancel"}}
	case planEditDate:
		return []hint{{"↵", "set month"}, {"esc", "cancel"}}
	case planRules:
		if p.editor != nil {
			return p.editor.Hints()
		}
		return []hint{{"a", "add rule"}, {"e", "edit"}, {"d", "delete"},
			{"space", "on/off"}, {"esc", "back to the plan"}}
	default:
		h := []hint{{"↑↓", "line"}, {"↵", "override amount"}, {"ctrl+r", "rules"}}
		if p.incomeIn.Focused() {
			h = append(h, hint{"esc", "stop typing"})
		} else {
			h = append(h, hint{"i", "edit income"})
		}
		if p.hasSaved && p.saved.Executed {
			return append(h, hint{"ctrl+z", "undo this month"})
		}
		return append(h, hint{"ctrl+s", "save & record"}, hint{"ctrl+d", "month"})
	}
}

func (p *planScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch p.mode {
	case planEditAmount:
		return p.updateAmount(key)
	case planEditDate:
		return p.updateDate(key)
	case planRules:
		return p.updateRules(key)
	default:
		return p.updateEntry(key)
	}
}

func (p *planScreen) updateEntry(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		// Leaving the income field is what hands the number keys back to
		// navigation, so this is the way out of the screen.
		p.incomeIn.Blur()
		return nil

	case "i":
		if !p.incomeIn.Focused() {
			p.incomeIn.Focus()
			p.incomeIn.CursorEnd()
			return nil
		}

	case "ctrl+r":
		p.mode = planRules
		p.cursor = 0
		p.incomeIn.Blur()
		return nil

	case "ctrl+d":
		p.dateIn.SetValue(p.date)
		p.dateIn.Focus()
		p.mode = planEditDate
		return nil

	case "up":
		if p.cursor > 0 {
			p.cursor--
		}
		return nil
	case "down":
		if p.cursor < len(p.result.Lines)-1 {
			p.cursor++
		}
		return nil

	case "enter":
		if p.cursor < len(p.result.Lines) {
			p.amountIn.SetValue(p.result.Lines[p.cursor].Amount.Format(0, money.GroupNone))
			p.amountIn.Focus()
			p.amountIn.CursorEnd()
			p.mode = planEditAmount
		}
		return nil

	case "ctrl+z":
		if p.hasSaved && p.saved.Executed {
			if err := p.m.store.RevertPlan(p.saved.ID); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return tea.Batch(
				func() tea.Msg { return refreshMsg{} },
				func() tea.Msg { return statusMsg("undid " + p.date) })
		}
		return nil

	case "ctrl+s":
		return p.save()
	}

	if !p.incomeIn.Focused() {
		return nil
	}
	var cmd tea.Cmd
	p.incomeIn, cmd = p.incomeIn.Update(key)
	p.err = ""
	// Recomputing on every keystroke is what makes this feel like a spreadsheet:
	// the routing updates as the income is typed.
	if err := p.recompute(); err != nil {
		p.err = err.Error()
	}
	return cmd
}

func (p *planScreen) save() tea.Cmd {
	income, err := p.income()
	if err != nil {
		p.err = "enter the income for this month first"
		return nil
	}
	if len(p.result.Lines) == 0 {
		p.err = "no rules yet — press ctrl+r to add some"
		return nil
	}
	planID, err := p.m.store.SavePlan(p.date, income, p.result, "")
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if err := p.m.store.ExecutePlan(planID); err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	return tea.Batch(
		func() tea.Msg { return refreshMsg{} },
		func() tea.Msg { return statusMsg("recorded " + p.date) })
}

func (p *planScreen) updateAmount(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		p.mode = planEntry
		p.incomeIn.Focus()
		return nil
	case "enter":
		v, err := money.Parse(strings.TrimSpace(p.amountIn.Value()))
		if err != nil {
			p.err = err.Error()
			return nil
		}
		if p.cursor < len(p.result.Lines) {
			p.overrides[p.result.Lines[p.cursor].RuleID] = v
		}
		p.mode = planEntry
		p.incomeIn.Focus()
		p.err = ""
		if err := p.recompute(); err != nil {
			p.err = err.Error()
		}
		return nil
	}
	var cmd tea.Cmd
	p.amountIn, cmd = p.amountIn.Update(key)
	return cmd
}

func (p *planScreen) updateDate(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		p.mode = planEntry
		p.incomeIn.Focus()
		return nil
	case "enter":
		v := monthStart(strings.TrimSpace(p.dateIn.Value()))
		if _, err := parseDate(v); err != nil {
			p.err = "months look like 2026-08"
			return nil
		}
		p.date = v
		p.mode = planEntry
		p.incomeIn.SetValue("")
		p.overrides = map[int64]money.Amount{}
		p.incomeIn.Focus()
		p.err = ""
		return func() tea.Msg { return refreshMsg{} }
	}
	var cmd tea.Cmd
	p.dateIn, cmd = p.dateIn.Update(key)
	return cmd
}

func (p *planScreen) updateRules(key tea.KeyMsg) tea.Cmd {
	if p.editor != nil {
		done, cmd := p.editor.Update(key)
		if done {
			p.editor = nil
			if err := p.Refresh(); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
		}
		return cmd
	}
	switch key.String() {
	case "esc":
		p.mode = planEntry
		p.cursor = 0
		p.incomeIn.Focus()
		return nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.rules)-1 {
			p.cursor++
		}
	case "a":
		p.editor = newRuleForm(p.m, plan.Rule{
			Stage: plan.StageInvest, Kind: plan.KindPercent,
			Basis: plan.BasisRemaining, Active: true, Priority: len(p.rules) + 1,
		}, false)
	case "e":
		if p.cursor < len(p.rules) {
			p.editor = newRuleForm(p.m, p.rules[p.cursor], true)
		}
	case " ":
		if p.cursor < len(p.rules) {
			r := p.rules[p.cursor]
			r.Active = !r.Active
			if _, err := p.m.store.SaveRule(r); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return func() tea.Msg { return refreshMsg{} }
		}
	case "d":
		if p.cursor < len(p.rules) {
			if err := p.m.store.DeleteRule(p.rules[p.cursor].ID); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return func() tea.Msg { return refreshMsg{} }
		}
	}
	return nil
}

func (p *planScreen) View(width, height int) string {
	if p.mode == planRules {
		if p.editor != nil {
			return p.editor.View(width, height)
		}
		return p.rulesView(width, height)
	}
	return p.planView(width, height)
}

func (p *planScreen) planView(width, height int) string {
	var b strings.Builder

	title := styHeading.Render("Monthly plan")
	right := styFaint.Render(p.date)
	if p.hasSaved && p.saved.Executed {
		right = styGain.Render("recorded") + styFaint.Render("  ·  "+p.date)
	}
	gap := max(1, width-lipgloss.Width(title)-lipgloss.Width(right)-4)
	b.WriteString("  " + title + strings.Repeat(" ", gap) + right + "\n\n")

	label := styFieldActive
	if p.mode != planEntry {
		label = styFieldLabel
	}
	b.WriteString("  " + label.Render(pad("Income", 14, false)) + p.incomeIn.View() + "\n")
	if income, err := p.income(); err == nil {
		b.WriteString("  " + pad("", 14, false) + styMuted.Render("= "+p.m.fmt(income)) + "\n")
	} else if strings.TrimSpace(p.incomeIn.Value()) != "" {
		b.WriteString("  " + pad("", 14, false) + styError.Render(err.Error()) + "\n")
	} else {
		b.WriteString("\n")
	}

	if len(p.rules) == 0 {
		b.WriteString("\n" + styMuted.Render(wrapIndent(
			"No routing rules yet. Press ctrl+r to add them: how much you set aside for "+
				"spending, any fixed instalments, and how the rest is split. Once they are "+
				"in place, entering a month's income is the only step.",
			min(width-4, 76), "  ")) + "\n")
		return b.String()
	}

	b.WriteString("\n")
	b.WriteString(p.linesView(width, height))
	b.WriteString(p.totalsView(width))

	if p.mode == planEditAmount {
		b.WriteString("\n  " + styFieldActive.Render(pad("Amount", 14, false)) + p.amountIn.View() + "\n")
	}
	if p.mode == planEditDate {
		b.WriteString("\n  " + styFieldActive.Render(pad("Month", 14, false)) + p.dateIn.View() + "\n")
	}
	if p.err != "" {
		b.WriteString("\n  " + styError.Render(p.err) + "\n")
	}
	for _, w := range p.result.Warnings {
		b.WriteString("  " + styBanner.Render("! ") +
			styMuted.Render(strings.TrimLeft(wrapIndent(w, min(width-8, 74), "    "), " ")) + "\n")
	}
	return b.String()
}

// linesView is the answer the user came for: move this much, to that account.
func (p *planScreen) linesView(width, height int) string {
	nameW := max(16, min(28, width-52))
	cols := []column{
		{title: "Move", width: nameW},
		{title: "To", width: 22},
		{title: "Amount", width: 14, right: true},
		{title: "", width: 10},
	}
	var cells [][]string
	var styles [][]lipgloss.Style
	for _, l := range p.result.Lines {
		note := ""
		noteStyle := styFaint
		if l.Overridden {
			note, noteStyle = "changed", styWarn
		} else if l.Stage == plan.StageSetAside {
			note = "set aside"
		}
		cells = append(cells, []string{l.Label, l.Account, p.m.fmt(l.Amount), note})
		styles = append(styles, []lipgloss.Style{
			lipgloss.NewStyle(), styMuted, lipgloss.NewStyle(), noteStyle,
		})
	}
	return renderTable(cols, cells, styles, p.cursor, p.mode == planEntry)
}

func (p *planScreen) totalsView(width int) string {
	r := p.result
	if r.Income == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	row := func(label string, amt money.Amount, style lipgloss.Style, note string) {
		b.WriteString("  " + styFieldLabel.Render(pad(label, 18, false)) +
			style.Render(pad(p.m.fmt(amt), 15, true)))
		if note != "" {
			b.WriteString("   " + styFaint.Render(note))
		}
		b.WriteString("\n")
	}
	row("Set aside", r.SetAside, styMuted, "")
	row("To invest", r.Investable, lipgloss.NewStyle(), "")
	if r.Unallocated != 0 {
		note := "not routed anywhere"
		style := styWarn
		if r.Unallocated < 0 {
			note = "more than there is"
			style = styLoss
		}
		row("Unallocated", r.Unallocated, style, note)
	}
	// Two different questions, kept apart because conflating them is how a
	// savings rate ends up flattering or maligning itself: how much of the
	// income went into investments, and how much of it actually left.
	b.WriteString("  " + styFaint.Render(fmt.Sprintf(
		"%.0f%% of income routed to investments", r.InvestmentRate()*100)) + "\n")

	net := r.Income - r.Spent()
	b.WriteString("  " + styFaint.Render("net worth this month ") +
		signStyle(int64(net)).Render(p.m.fmts(net)))
	if spent := r.Spent(); spent > 0 {
		b.WriteString(styFaint.Render(fmt.Sprintf("   (%s spent, keeping %.0f%%)",
			p.m.fmt(spent), r.SavingsRate()*100)))
	} else {
		b.WriteString(styFaint.Render("   nothing left your accounts yet"))
	}
	b.WriteString("\n")
	return b.String()
}

func (p *planScreen) rulesView(width, height int) string {
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("Routing rules") + "\n")
	if len(p.rules) == 0 {
		b.WriteString("\n" + styMuted.Render(wrapIndent(
			"Press a to add the first rule.\n\n"+
				"Rules run in priority order, in two stages. Set-asides come off income "+
				"first; whatever survives is what there is to invest, and the invest rules "+
				"divide it.", min(width-4, 76), "  ")) + "\n")
		return b.String()
	}

	nameW := max(14, min(24, width-64))
	cols := []column{
		{title: "#", width: 3, right: true},
		{title: "Rule", width: nameW},
		{title: "To", width: 20},
		{title: "How much", width: 30},
		{title: "Stage", width: 9},
	}
	var cells [][]string
	var styles [][]lipgloss.Style
	for _, r := range p.rules {
		name, nameStyle := r.Name, lipgloss.NewStyle()
		if !r.Active {
			name, nameStyle = r.Name+" (off)", styFaint
		}
		stage := "invest"
		if r.Stage == plan.StageSetAside {
			stage = "set aside"
		}
		cells = append(cells, []string{
			fmt.Sprint(r.Priority), name, r.Account,
			r.Describe(p.m.settings.Grouping), stage,
		})
		styles = append(styles, []lipgloss.Style{
			styFaint, nameStyle, styMuted, styMuted, styFaint,
		})
	}
	b.WriteString(renderTable(cols, cells, styles, p.cursor, true))
	b.WriteString("\n" + styMuted.Render(wrapIndent(
		"Rules run in priority order. A rule can refer to what earlier ones left, so "+
			"\"50,000 to the tax deposit, then split the rest\" works as written.",
		min(width-4, 76), "  ")) + "\n")
	return b.String()
}

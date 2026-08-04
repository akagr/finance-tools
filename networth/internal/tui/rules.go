package tui

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/expr"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/plan"
	tea "github.com/charmbracelet/bubbletea"
)

// ruleForm edits one routing rule.
//
// The "how much" field accepts a plain amount, a percentage, the word remainder,
// or a whole formula. Collapsing four rule types into one field is what keeps the
// simple cases simple while leaving the hard ones expressible - the same reason a
// spreadsheet cell takes either a number or an expression.
type ruleForm struct {
	m       *Model
	rule    plan.Rule
	editing bool
	field   int
	fields  []*field
	err     string
	preview string
}

const (
	ruleFieldName = iota
	ruleFieldAccount
	ruleFieldHowMuch
	ruleFieldStage
	ruleFieldPriority
)

func newRuleForm(m *Model, r plan.Rule, editing bool) *ruleForm {
	f := &ruleForm{m: m, rule: r, editing: editing}
	f.fields = []*field{
		newTextField("Name", "what this transfer is for"),
		// Strict: a rule pointing at an account that does not exist would fail
		// silently every month, so the value has to come from the list.
		newSuggestField("To account", "where the money goes — start typing to filter",
			m.accountNames(), true),
		newTextField("How much",
			"50000 · 40% · 40% of income · remainder · or a formula"),
		newChoiceField("Stage",
			"set aside comes off income first — spending, EMI, a fixed deposit. "+
				"invest divides whatever is left.",
			[]string{"set aside", "invest"}),
		newTextField("Priority", "lower runs first; a rule can use what earlier ones left"),
	}
	f.fields[ruleFieldName].SetValue(r.Name)
	f.fields[ruleFieldAccount].SetValue(r.Account)
	f.fields[ruleFieldHowMuch].SetValue(howMuchText(r, m.settings.Grouping))
	stage := "invest"
	if r.Stage == plan.StageSetAside {
		stage = "set aside"
	}
	f.fields[ruleFieldStage].SetValue(stage)
	f.fields[ruleFieldPriority].SetValue(fmt.Sprint(r.Priority))
	f.fields[ruleFieldName].Focus()
	f.updatePreview()
	return f
}

// accountNames lists every open account, for the destination field.
func (m *Model) accountNames() []string {
	accts, err := m.store.Accounts(false)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(accts))
	for _, a := range accts {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

// howMuchText renders a rule back into the single field it is edited through.
func howMuchText(r plan.Rule, g money.Grouping) string {
	switch r.Kind {
	case plan.KindFixed:
		return r.Value.Format(0, money.GroupNone)
	case plan.KindRemainder:
		return "remainder"
	case plan.KindFormula:
		return r.Formula
	case plan.KindPercent:
		// A brand new rule has no amount yet. Showing "0%" would look like a
		// decision rather than a blank waiting to be filled.
		if r.Value == 0 {
			return ""
		}
		s := fmt.Sprintf("%.4g%%", r.Percent())
		switch r.Basis {
		case plan.BasisIncome:
			return s + " of income"
		case plan.BasisInvestable:
			return s + " of investable"
		}
		return s
	}
	return ""
}

// parseHowMuch works out which kind of rule was written.
//
// The order of the checks matters: "remainder" and a bare percentage are
// recognised first, and anything else falls through to a formula, so the
// expressive case is the default rather than an opt-in.
func parseHowMuch(text string) (plan.Kind, plan.Basis, money.Amount, string, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", "", 0, "", fmt.Errorf("say how much")
	}
	lower := strings.ToLower(t)
	if lower == "remainder" || lower == "rest" || lower == "whatever is left" {
		return plan.KindRemainder, plan.BasisInvestable, 0, "", nil
	}

	basis := plan.BasisRemaining
	body := lower
	if rest, ok := strings.CutSuffix(lower, "of income"); ok {
		basis, body = plan.BasisIncome, strings.TrimSpace(rest)
	} else if rest, ok := strings.CutSuffix(lower, "of investable"); ok {
		basis, body = plan.BasisInvestable, strings.TrimSpace(rest)
	} else if rest, ok := strings.CutSuffix(lower, "of remaining"); ok {
		basis, body = plan.BasisRemaining, strings.TrimSpace(rest)
	} else if rest, ok := strings.CutSuffix(lower, "of the rest"); ok {
		basis, body = plan.BasisRemaining, strings.TrimSpace(rest)
	}

	// A bare percentage, with nothing else in it, is a percentage rule.
	if pctBody, ok := strings.CutSuffix(strings.TrimSpace(body), "%"); ok {
		if v, err := money.Parse(strings.TrimSpace(pctBody)); err == nil {
			return plan.KindPercent, basis, v / 100, "", nil
		}
	}

	// A plain amount, with no names or operators, is a fixed rule.
	if !strings.ContainsAny(t, "()+-*/\"'") && !containsLetter(t) {
		if v, err := money.Parse(t); err == nil {
			return plan.KindFixed, plan.BasisInvestable, v, "", nil
		}
	}

	// Everything else is a formula, and must parse before it is accepted.
	if _, err := expr.Parse(t); err != nil {
		return "", "", 0, "", err
	}
	return plan.KindFormula, plan.BasisInvestable, 0, t, nil
}

func containsLetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func (f *ruleForm) Hints() []hint {
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

func (f *ruleForm) Update(key tea.KeyMsg) (bool, tea.Cmd) {
	if key.String() == "ctrl+s" {
		if err := f.save(); err != nil {
			f.err = err.Error()
			return false, nil
		}
		name := f.fields[ruleFieldName].Value()
		return true, tea.Batch(
			func() tea.Msg { return refreshMsg{} },
			func() tea.Msg { return statusMsg(name + " saved") })
	}

	if handled, cmd := f.fields[f.field].Update(key); handled {
		f.err = ""
		f.updatePreview()
		return false, cmd
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
		return true, func() tea.Msg { return refreshMsg{} }
	case "tab", "down":
		f.focus((f.field + 1) % len(f.fields))
	case "shift+tab", "up":
		f.focus((f.field + len(f.fields) - 1) % len(f.fields))
	}
	return false, nil
}

func (f *ruleForm) focus(i int) {
	f.fields[f.field].Blur()
	f.field = i
	f.fields[f.field].Focus()
}

// updatePreview evaluates the "how much" field as it is typed, so a mistake in a
// formula is caught while writing it rather than when money is about to move.
func (f *ruleForm) updatePreview() {
	f.preview = ""
	text := strings.TrimSpace(f.fields[ruleFieldHowMuch].Value())
	if text == "" {
		return
	}
	kind, basis, value, formula, err := parseHowMuch(text)
	if err != nil {
		f.preview = styError.Render(err.Error())
		return
	}
	switch kind {
	case plan.KindFixed:
		f.preview = styMuted.Render("a fixed " + f.m.fmt(value))
	case plan.KindRemainder:
		f.preview = styMuted.Render("whatever the other rules leave")
	case plan.KindPercent:
		of := "of what is still unallocated"
		switch basis {
		case plan.BasisIncome:
			of = "of income"
		case plan.BasisInvestable:
			of = "of the investable amount"
		}
		f.preview = styMuted.Render(fmt.Sprintf("%.4g%% %s", value.Float()*100, of))
	case plan.KindFormula:
		f.preview = f.evalPreview(formula)
	}
}

// evalPreview shows what a formula comes to right now, using the current
// balances. Seeing the number is the only reliable way to know the formula says
// what was meant.
func (f *ruleForm) evalPreview(formula string) string {
	bals, err := f.m.store.Balances()
	if err != nil {
		return styFaint.Render("a formula")
	}
	env := &expr.BaseEnv{Accounts: bals, Vars: map[string]*big.Rat{}}
	v, err := expr.Eval(formula, env)
	if err != nil {
		// Names that only exist while a plan is running are not an error here.
		if strings.Contains(err.Error(), "unknown name") {
			return styFaint.Render("depends on the month's income")
		}
		return styError.Render(err.Error())
	}
	return styMuted.Render("right now that is " + f.m.fmt(money.FromRat(v)))
}

func (f *ruleForm) save() error {
	r := f.rule
	r.Name = strings.TrimSpace(f.fields[ruleFieldName].Value())
	if r.Name == "" {
		return fmt.Errorf("a rule needs a name")
	}

	if ok, why := f.fields[ruleFieldAccount].Valid(); !ok {
		return fmt.Errorf("%s", why)
	}
	acctName := strings.TrimSpace(f.fields[ruleFieldAccount].Value())
	acct, found, err := f.m.store.AccountByName(acctName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no account called %q — add it on the Accounts screen first", acctName)
	}
	r.AccountID, r.Account = acct.ID, acct.Name

	kind, basis, value, formula, err := parseHowMuch(f.fields[ruleFieldHowMuch].Value())
	if err != nil {
		return err
	}
	r.Kind, r.Basis, r.Value, r.Formula = kind, basis, value, formula

	r.Stage = plan.StageInvest
	if f.fields[ruleFieldStage].Value() == "set aside" {
		r.Stage = plan.StageSetAside
	}
	fmt.Sscanf(strings.TrimSpace(f.fields[ruleFieldPriority].Value()), "%d", &r.Priority)
	r.Active = true

	_, err = f.m.store.SaveRule(r)
	return err
}

func (f *ruleForm) View(width, height int) string {
	var b strings.Builder
	title := "New rule"
	if f.editing {
		title = "Edit " + f.rule.Name
	}
	b.WriteString("\n  " + styHeading.Render(title) + "\n\n")

	const labelW = 14
	for i, fl := range f.fields {
		b.WriteString(fl.View(labelW, i == f.field))
		b.WriteString(fl.Extra(labelW+2, 6))
		if i == ruleFieldHowMuch && f.preview != "" {
			b.WriteString("    " + pad("", labelW, false) + f.preview + "\n")
		}
		if i == f.field {
			b.WriteString(fl.Help(labelW+2, width))
		}
	}
	if f.err != "" {
		b.WriteString("\n  " + styError.Render(f.err) + "\n")
	}
	if f.field == ruleFieldHowMuch {
		b.WriteString("\n" + f.formulaHelp(width))
	}
	return b.String()
}

// formulaHelp lists what a formula can refer to. Discoverability matters most
// here: a language nobody can see the vocabulary of is a language nobody uses.
func (f *ruleForm) formulaHelp(width int) string {
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("In a formula you can use") + "\n")
	for _, v := range expr.Variables {
		b.WriteString("    " + styKey.Render(pad(v.Name, 12, false)) +
			styFaint.Render(v.Help) + "\n")
	}
	for _, fn := range expr.Functions[:3] {
		b.WriteString("    " + styKey.Render(pad(fn.Name+"("+fn.Args+")", 26, false)) +
			styFaint.Render(fn.Help) + "\n")
	}
	b.WriteString("    " + styFaint.Render("also min, max, abs, round, floor, ceil, clamp") + "\n")
	b.WriteString("\n  " + styFaint.Render(`e.g. 100000 + owed("tag:type=credit_card") - balance("Spending")`) + "\n")

	if bals, err := f.m.store.Balances(); err == nil {
		sels := bals.Selectors()
		if len(sels) > 8 {
			sels = sels[:8]
		}
		b.WriteString("  " + styFaint.Render("selectors: "+strings.Join(sels, ", ")) + "\n")
	}
	return b.String()
}

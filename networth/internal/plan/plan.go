// Package plan turns a month's income into a routing decision: how much goes to
// spending, and how the rest is split across investments.
//
// This is the other half of tracking net worth honestly. A balance round can say
// that money was added; only the routing decision knows how much, and when.
// Capturing it monthly turns the contribution figure from something recalled
// twice a year into something recorded twelve times, which is what makes the
// split between contributions and returns trustworthy.
//
// The computation is here rather than in SQL because it is a waterfall with
// ordering, a remainder and rounding rules - logic that is far clearer as code
// and needs to be testable in isolation.
package plan

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/akagr/finance-tools/networth/internal/expr"
	"github.com/akagr/finance-tools/networth/internal/money"
)

// Stage orders the waterfall.
type Stage string

const (
	// StageSetAside comes off income first: spending, EMI, insurance. What
	// survives is what there is to invest.
	StageSetAside Stage = "setaside"
	// StageInvest divides whatever is left.
	StageInvest Stage = "invest"
)

// Kind is how a rule's amount is arrived at.
type Kind string

const (
	// KindPercent is a share of its basis.
	KindPercent Kind = "percent"
	// KindFixed is a flat amount, for things like a PPF instalment.
	KindFixed Kind = "fixed"
	// KindRemainder sweeps up whatever the other rules did not claim, so that
	// rounding and odd amounts always land somewhere deliberate.
	KindRemainder Kind = "remainder"
	// KindFormula is an arithmetic expression. It exists because real routing
	// decisions refer to other accounts, and no fixed set of rule types covers
	// "top the spending account up to a lakh plus the credit cards, less what is
	// already sitting in it".
	KindFormula Kind = "formula"
)

// Basis is what a percentage is a percentage of.
type Basis string

const (
	// BasisIncome is a share of gross income.
	BasisIncome Basis = "income"
	// BasisInvestable is a share of what is left after the set-asides, which is
	// how most people actually think about splitting savings.
	BasisInvestable Basis = "investable"
	// BasisRemaining is a share of what is still unclaimed at the point the rule
	// runs. It is what "a fixed instalment first, then split the rest" needs:
	// a percentage of investable would wrongly include the instalment.
	BasisRemaining Basis = "remaining"
)

// Rule is one standing instruction about where money goes.
type Rule struct {
	ID        int64
	Name      string
	AccountID int64
	Account   string
	Stage     Stage
	Kind      Kind
	Basis     Basis
	// Value is a currency amount for KindFixed, or a fraction for KindPercent
	// (40% is 0.40). Unused for KindRemainder.
	Value money.Amount
	// Formula is the expression evaluated for KindFormula.
	Formula string
	// Priority orders evaluation within a stage. It matters: a rule referring to
	// "remaining" sees only what the rules before it have left.
	Priority int
	Active   bool
	Notes    string
	// Outside records whether the target account sits outside net worth. It is
	// what tells spending apart from a transfer: routing money to an account you
	// own moves it, routing it outside spends it.
	Outside bool
}

// Percent renders a percentage rule's value for display.
func (r Rule) Percent() float64 { return r.Value.Float() * 100 }

// Describe states the rule in the words it would be explained in, which is what
// the interface shows instead of three separate columns of jargon.
func (r Rule) Describe(g money.Grouping) string {
	switch r.Kind {
	case KindFixed:
		return r.Value.Format(0, g) + " fixed"
	case KindRemainder:
		return "whatever is left"
	case KindFormula:
		return r.Formula
	default:
		basis := "of what is left to invest"
		switch r.Basis {
		case BasisIncome:
			basis = "of income"
		case BasisRemaining:
			basis = "of what is still unallocated"
		}
		return fmt.Sprintf("%.4g%% %s", r.Percent(), basis)
	}
}

// Line is one routing instruction produced for a particular month.
type Line struct {
	RuleID     int64
	AccountID  int64
	Account    string
	Label      string
	Stage      Stage
	Amount     money.Amount
	Overridden bool
	// Outside means this line takes money out of net worth.
	Outside bool
}

// Result is a whole month's plan.
type Result struct {
	Income      money.Amount
	SetAside    money.Amount
	Investable  money.Amount
	Invested    money.Amount
	Unallocated money.Amount
	Lines       []Line
	// Warnings describe anything that makes the plan not add up, in plain words.
	// They are surfaced rather than silently corrected, because a plan that does
	// not balance usually means a rule is wrong, not that the arithmetic needs
	// nudging.
	Warnings []string
}

// Spent is the part of the month's income that leaves net worth.
//
// Only lines pointing at an account outside net worth count. Topping up a
// spending account you own is a transfer: the money is still yours until it is
// actually spent, and counting it as spending would understate every savings
// figure by the size of the float you keep.
func (r Result) Spent() money.Amount {
	var total money.Amount
	for _, l := range r.Lines {
		if l.Outside {
			total += l.Amount
		}
	}
	return total
}

// SavingsRate is the share of income that did not leave net worth.
func (r Result) SavingsRate() float64 {
	if r.Income <= 0 {
		return 0
	}
	return float64(r.Income-r.Spent()) / float64(r.Income)
}

// InvestmentRate is the share of income routed into investments, which is a
// different question from the savings rate and usually a smaller number.
func (r Result) InvestmentRate() float64 {
	if r.Income <= 0 {
		return 0
	}
	return float64(r.Investable) / float64(r.Income)
}

// Compute applies the rules to a month's income.
//
// Rules are evaluated one at a time in priority order, not in a single pass, so
// that each can see what the ones before it have already claimed. That is what
// makes "50,000 to the tax deposit, then split what is left" mean what it says:
// a percentage of `investable` would wrongly include the 50,000, while a
// percentage of `remaining` does not.
//
// Amounts are rounded to whole currency units, because nobody transfers 37 paise
// and a plan you cannot execute exactly is a plan you will quietly deviate from.
// The rounding difference is absorbed by the remainder rule, or reported as
// unallocated if there is none.
func Compute(income money.Amount, rules []Rule, overrides map[int64]money.Amount, accounts expr.Balances) Result {
	res := Result{Income: income}
	if income < 0 {
		res.Warnings = append(res.Warnings, "income is negative; check the figure you entered")
	}

	active := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.Active {
			active = append(active, r)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Stage != active[j].Stage {
			return active[i].Stage == StageSetAside
		}
		if active[i].Priority != active[j].Priority {
			return active[i].Priority < active[j].Priority
		}
		return active[i].Name < active[j].Name
	})

	ev := &evaluator{res: &res, accounts: accounts, byLabel: map[string]money.Amount{}}

	// Pass one: everything that comes off income before there is anything to
	// invest.
	for _, r := range active {
		if r.Stage != StageSetAside {
			continue
		}
		amt, ok := ev.amountFor(r)
		if !ok {
			continue
		}
		amt = applyOverride(r, amt, overrides)
		res.SetAside += amt
		ev.record(r, amt, overrides)
	}

	res.Investable = income - res.SetAside
	if res.Investable < 0 {
		res.Warnings = append(res.Warnings,
			"set-asides come to more than the income, so there is nothing left to invest")
		res.Investable = 0
	}

	// Pass two: divide what is left, holding the remainder rules back so they
	// can absorb whatever the others leave.
	var remainders []Rule
	for _, r := range active {
		if r.Stage != StageInvest {
			continue
		}
		if r.Kind == KindRemainder {
			remainders = append(remainders, r)
			continue
		}
		amt, ok := ev.amountFor(r)
		if !ok {
			continue
		}
		amt = applyOverride(r, amt, overrides)
		res.Invested += amt
		ev.record(r, amt, overrides)
	}

	left := res.Investable - res.Invested
	// A remainder rule must never be handed a negative amount: "transfer minus
	// three lakh to arbitrage" is not an instruction, and letting it silently
	// balance the books would hide the fact that the other rules over-committed.
	if left < 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"the investment rules come to %s more than there is to invest",
			(-left).Format(0, money.GroupIndian)))
		for _, r := range remainders {
			ev.record(r, applyOverride(r, 0, overrides), overrides)
		}
		res.Unallocated = left
		return res
	}

	switch {
	case len(remainders) == 0:
		res.Unallocated = left
	case len(remainders) == 1:
		amt := applyOverride(remainders[0], left, overrides)
		res.Invested += amt
		ev.record(remainders[0], amt, overrides)
		res.Unallocated = res.Investable - res.Invested
	default:
		// Splitting a remainder several ways is ambiguous, so it is split evenly
		// and said out loud rather than guessed at silently.
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%d rules claim the remainder, so it is split evenly between them", len(remainders)))
		share := roundToUnit(divide(left, len(remainders)))
		for i, r := range remainders {
			amt := share
			if i == len(remainders)-1 {
				amt = left - share*money.Amount(len(remainders)-1)
			}
			amt = applyOverride(r, amt, overrides)
			res.Invested += amt
			ev.record(r, amt, overrides)
		}
		res.Unallocated = res.Investable - res.Invested
	}
	return res
}

// evaluator carries the state a rule can see: the totals so far, and the amounts
// earlier rules came to.
type evaluator struct {
	res      *Result
	accounts expr.Balances
	byLabel  map[string]money.Amount
}

func (e *evaluator) record(r Rule, amt money.Amount, overrides map[int64]money.Amount) {
	e.byLabel[r.Name] = amt
	e.res.Lines = append(e.res.Lines, lineFor(r, amt, overrides))
}

// env exposes the running totals to a formula.
func (e *evaluator) env() *expr.BaseEnv {
	allocated := e.res.SetAside
	remaining := e.res.Income - e.res.SetAside
	if e.res.Investable > 0 || e.res.Invested > 0 {
		allocated = e.res.Invested
		remaining = e.res.Investable - e.res.Invested
	}
	return &expr.BaseEnv{
		Vars: map[string]*big.Rat{
			"income":     e.res.Income.Rat(),
			"setaside":   e.res.SetAside.Rat(),
			"investable": e.res.Investable.Rat(),
			"allocated":  allocated.Rat(),
			"remaining":  remaining.Rat(),
		},
		Accounts: e.accounts,
		Lines: func(label string) (*big.Rat, bool) {
			v, ok := e.byLabel[label]
			if !ok {
				return nil, false
			}
			return v.Rat(), true
		},
	}
}

// amountFor evaluates a single rule. The second return is false for a rule that
// could not be evaluated, in which case a warning has already been recorded.
func (e *evaluator) amountFor(r Rule) (money.Amount, bool) {
	switch r.Kind {
	case KindFixed:
		return r.Value, true

	case KindFormula:
		v, err := expr.Eval(r.Formula, e.env())
		if err != nil {
			// A broken formula must not quietly become zero: that would route
			// nothing to an account and look like a deliberate decision.
			e.res.Warnings = append(e.res.Warnings,
				fmt.Sprintf("%s: %v", r.Name, err))
			return 0, false
		}
		return roundToUnit(money.FromRat(v)), true

	case KindPercent:
		var base money.Amount
		switch {
		// A set-aside happens before "investable" exists, so it can only ever be
		// a share of income.
		case r.Stage == StageSetAside, r.Basis == BasisIncome:
			base = e.res.Income
		case r.Basis == BasisRemaining:
			base = e.res.Investable - e.res.Invested
		default:
			base = e.res.Investable
		}
		return roundToUnit(multiply(base, r.Value)), true

	case KindRemainder:
		if r.Stage == StageSetAside {
			e.res.Warnings = append(e.res.Warnings, fmt.Sprintf(
				"%q claims the remainder but runs before there is one; ignored", r.Name))
			return 0, false
		}
		return 0, true
	}
	return 0, true
}

func applyOverride(r Rule, amt money.Amount, overrides map[int64]money.Amount) money.Amount {
	if v, ok := overrides[r.ID]; ok {
		return v
	}
	return amt
}

func lineFor(r Rule, amt money.Amount, overrides map[int64]money.Amount) Line {
	_, overridden := overrides[r.ID]
	return Line{
		RuleID: r.ID, AccountID: r.AccountID, Account: r.Account,
		Label: r.Name, Stage: r.Stage, Amount: amt, Overridden: overridden,
		Outside: r.Outside,
	}
}

// multiply keeps the intermediate exact: two scaled integers multiplied directly
// would overflow int64 well inside the range of a real salary.
func multiply(a, b money.Amount) money.Amount {
	return money.FromRat(new(big.Rat).Mul(a.Rat(), b.Rat()))
}

func divide(a money.Amount, n int) money.Amount {
	if n == 0 {
		return 0
	}
	return money.FromRat(new(big.Rat).Quo(a.Rat(), new(big.Rat).SetInt64(int64(n))))
}

// roundToUnit rounds to a whole currency unit, so every figure in a plan is one
// that can actually be transferred.
func roundToUnit(a money.Amount) money.Amount {
	rem := int64(a) % money.Scale
	if rem == 0 {
		return a
	}
	base := int64(a) - rem
	if rem < 0 {
		if -rem*2 >= money.Scale {
			return money.Amount(base - money.Scale)
		}
		return money.Amount(base)
	}
	if rem*2 >= money.Scale {
		return money.Amount(base + money.Scale)
	}
	return money.Amount(base)
}

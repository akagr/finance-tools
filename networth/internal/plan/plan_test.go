package plan

import (
	"math/big"
	"testing"

	"github.com/akagr/finance-tools/networth/internal/money"
)

func pct(f float64) money.Amount { return money.FromFloat(f / 100) }

// realistic mirrors the shape of an actual monthly routing sheet: spending comes
// off the top, a fixed PPF instalment is carved out, and the rest is split by
// percentage with a sweep account catching the odd rupees.
func realistic() []Rule {
	return []Rule{
		{ID: 1, Name: "Monthly spends", AccountID: 10, Stage: StageSetAside,
			Kind: KindPercent, Basis: BasisIncome, Value: pct(40), Priority: 1, Active: true},
		{ID: 2, Name: "PPF", AccountID: 11, Stage: StageInvest,
			Kind: KindFixed, Value: money.FromMajor(12500), Priority: 1, Active: true},
		{ID: 3, Name: "Domestic equity", AccountID: 12, Stage: StageInvest,
			Kind: KindPercent, Basis: BasisInvestable, Value: pct(40), Priority: 2, Active: true},
		{ID: 4, Name: "Global equity", AccountID: 13, Stage: StageInvest,
			Kind: KindPercent, Basis: BasisInvestable, Value: pct(20), Priority: 3, Active: true},
		{ID: 5, Name: "Debt", AccountID: 14, Stage: StageInvest,
			Kind: KindPercent, Basis: BasisInvestable, Value: pct(15), Priority: 4, Active: true},
		{ID: 6, Name: "Arbitrage", AccountID: 15, Stage: StageInvest,
			Kind: KindRemainder, Priority: 9, Active: true},
	}
}

func lineFor2(t *testing.T, res Result, name string) Line {
	t.Helper()
	for _, l := range res.Lines {
		if l.Label == name {
			return l
		}
	}
	t.Fatalf("no line named %q in %+v", name, res.Lines)
	return Line{}
}

func TestWaterfallSplitsIncome(t *testing.T) {
	res := Compute(money.FromMajor(200000), realistic(), nil, nil)

	if got, want := res.SetAside, money.FromMajor(80000); got != want {
		t.Errorf("set aside = %s, want %s", got, want)
	}
	if got, want := res.Investable, money.FromMajor(120000); got != want {
		t.Errorf("investable = %s, want %s", got, want)
	}
	// Percentages apply to what is left after spending, not to gross income.
	if got, want := lineFor2(t, res, "Domestic equity").Amount, money.FromMajor(48000); got != want {
		t.Errorf("domestic equity = %s, want %s", got, want)
	}
	if got, want := lineFor2(t, res, "PPF").Amount, money.FromMajor(12500); got != want {
		t.Errorf("PPF = %s, want %s", got, want)
	}
	// Arbitrage sweeps up: 120000 - 12500 - 48000 - 24000 - 18000.
	if got, want := lineFor2(t, res, "Arbitrage").Amount, money.FromMajor(17500); got != want {
		t.Errorf("arbitrage = %s, want %s", got, want)
	}
}

// The plan must account for every rupee of income. If it does not, money is
// going somewhere unrecorded and the contribution figure will be wrong.
func TestEveryRupeeIsAccountedFor(t *testing.T) {
	for _, income := range []int64{200000, 137777, 91234, 1000000} {
		res := Compute(money.FromMajor(income), realistic(), nil, nil)
		var total money.Amount
		for _, l := range res.Lines {
			total += l.Amount
		}
		total += res.Unallocated
		if total != res.Income {
			t.Errorf("income %d: lines plus unallocated = %s, want %s",
				income, total, res.Income)
		}
	}
}

// The whole point of a remainder rule is that rounding never leaves a stray
// amount stranded.
func TestRemainderAbsorbsRounding(t *testing.T) {
	res := Compute(money.FromMajor(137777), realistic(), nil, nil)
	if res.Unallocated != 0 {
		t.Errorf("unallocated = %s, want 0 when a remainder rule exists", res.Unallocated)
	}
	if res.Invested != res.Investable {
		t.Errorf("invested %s != investable %s", res.Invested, res.Investable)
	}
}

func TestWithoutARemainderTheShortfallIsReported(t *testing.T) {
	rules := realistic()[:5] // drop the arbitrage sweep
	res := Compute(money.FromMajor(200000), rules, nil, nil)
	if res.Unallocated != money.FromMajor(17500) {
		t.Errorf("unallocated = %s, want 17,500 shown as unallocated", res.Unallocated)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("leftover money is not an error, just a fact: %v", res.Warnings)
	}
}

// Amounts must be whole currency units. A plan telling you to transfer 47,999.64
// is one you will round yourself, and then the record no longer matches reality.
func TestAmountsAreWholeUnits(t *testing.T) {
	res := Compute(money.FromMajor(137777), realistic(), nil, nil)
	for _, l := range res.Lines {
		if int64(l.Amount)%money.Scale != 0 {
			t.Errorf("%s = %s, which is not a whole amount", l.Label, l.Amount)
		}
	}
}

func TestOverrideReplacesAComputedLine(t *testing.T) {
	res := Compute(money.FromMajor(200000), realistic(),
		map[int64]money.Amount{3: money.FromMajor(60000)}, nil)

	dom := lineFor2(t, res, "Domestic equity")
	if dom.Amount != money.FromMajor(60000) {
		t.Errorf("domestic equity = %s, want the override 60,000", dom.Amount)
	}
	if !dom.Overridden {
		t.Error("an overridden line should be marked as such")
	}
	// The remainder must shrink to keep the plan balanced.
	if got, want := lineFor2(t, res, "Arbitrage").Amount, money.FromMajor(5500); got != want {
		t.Errorf("arbitrage = %s, want %s", got, want)
	}
}

// Skipping a rule for one month - "PPF, if applicable" - is done by overriding
// it to zero, and must not disturb the rest.
func TestSkippingARuleForOneMonth(t *testing.T) {
	res := Compute(money.FromMajor(200000), realistic(), map[int64]money.Amount{2: 0}, nil)
	if got := lineFor2(t, res, "PPF").Amount; got != 0 {
		t.Errorf("PPF = %s, want 0", got)
	}
	if got, want := lineFor2(t, res, "Arbitrage").Amount, money.FromMajor(30000); got != want {
		t.Errorf("arbitrage = %s, want %s", got, want)
	}
	if res.Unallocated != 0 {
		t.Errorf("unallocated = %s, want 0", res.Unallocated)
	}
}

func TestOverAllocationIsReported(t *testing.T) {
	rules := realistic()
	rules[1].Value = money.FromMajor(500000) // an absurd PPF instalment
	res := Compute(money.FromMajor(200000), rules, nil, nil)
	if len(res.Warnings) == 0 {
		t.Error("allocating more than there is to invest should produce a warning")
	}
}

func TestSetAsidesExceedingIncomeAreReported(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Rent", AccountID: 10, Stage: StageSetAside,
			Kind: KindFixed, Value: money.FromMajor(90000), Priority: 1, Active: true},
		{ID: 2, Name: "EMI", AccountID: 11, Stage: StageSetAside,
			Kind: KindFixed, Value: money.FromMajor(60000), Priority: 2, Active: true},
	}
	res := Compute(money.FromMajor(100000), rules, nil, nil)
	if res.Investable != 0 {
		t.Errorf("investable = %s, want 0", res.Investable)
	}
	if len(res.Warnings) == 0 {
		t.Error("spending more than you earn should be said out loud")
	}
}

// A percentage set-aside can only be a share of income, since "investable" does
// not exist until the set-asides are done.
func TestSetAsidePercentAlwaysUsesIncome(t *testing.T) {
	rules := []Rule{{ID: 1, Name: "Spends", AccountID: 10, Stage: StageSetAside,
		Kind: KindPercent, Basis: BasisInvestable, Value: pct(50), Priority: 1, Active: true}}
	res := Compute(money.FromMajor(100000), rules, nil, nil)
	if got, want := res.SetAside, money.FromMajor(50000); got != want {
		t.Errorf("set aside = %s, want %s", got, want)
	}
}

func TestPercentOfIncomeAtTheInvestStage(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Spends", AccountID: 10, Stage: StageSetAside,
			Kind: KindPercent, Basis: BasisIncome, Value: pct(50), Priority: 1, Active: true},
		{ID: 2, Name: "Equity", AccountID: 11, Stage: StageInvest,
			Kind: KindPercent, Basis: BasisIncome, Value: pct(30), Priority: 1, Active: true},
	}
	res := Compute(money.FromMajor(100000), rules, nil, nil)
	// 30% of income, not 30% of the 50,000 left over.
	if got, want := lineFor2(t, res, "Equity").Amount, money.FromMajor(30000); got != want {
		t.Errorf("equity = %s, want %s", got, want)
	}
}

func TestInactiveRulesAreIgnored(t *testing.T) {
	rules := realistic()
	rules[1].Active = false
	res := Compute(money.FromMajor(200000), rules, nil, nil)
	for _, l := range res.Lines {
		if l.Label == "PPF" {
			t.Error("an inactive rule should not appear in the plan")
		}
	}
}

// Only money leaving net worth counts as spent. Topping up a spending account
// you own is a transfer, and treating it as spending would understate the
// savings rate by the size of the float you keep.
func TestSavingsRateCountsOnlyMoneyThatLeaves(t *testing.T) {
	rules := realistic()
	// The spends rule points at an account still inside net worth.
	res := Compute(money.FromMajor(200000), rules, nil, nil)
	if got := res.SavingsRate(); got != 1 {
		t.Errorf("savings rate = %.4f, want 1.00: nothing left your accounts", got)
	}
	if got := res.InvestmentRate(); got < 0.599 || got > 0.601 {
		t.Errorf("investment rate = %.4f, want 0.60", got)
	}

	// Now the same rule pointing outside net worth: that is real spending.
	rules[0].Outside = true
	res = Compute(money.FromMajor(200000), rules, nil, nil)
	if got := res.SavingsRate(); got < 0.599 || got > 0.601 {
		t.Errorf("savings rate = %.4f, want 0.60 once the money actually leaves", got)
	}
	if got, want := res.Spent(), money.FromMajor(80000); got != want {
		t.Errorf("spent = %s, want %s", got, want)
	}
}

func TestNoRulesIsNotAnError(t *testing.T) {
	res := Compute(money.FromMajor(100000), nil, nil, nil)
	if res.Unallocated != money.FromMajor(100000) {
		t.Errorf("unallocated = %s, want the whole income", res.Unallocated)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("having no rules yet is a normal first run: %v", res.Warnings)
	}
}

func TestTwoRemainderRulesSplitEvenlyAndWarn(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "A", AccountID: 10, Stage: StageInvest, Kind: KindRemainder, Active: true},
		{ID: 2, Name: "B", AccountID: 11, Stage: StageInvest, Kind: KindRemainder, Active: true},
	}
	res := Compute(money.FromMajor(100000), rules, nil, nil)
	if len(res.Warnings) == 0 {
		t.Error("an ambiguous remainder should be called out")
	}
	if got, want := lineFor2(t, res, "A").Amount, money.FromMajor(50000); got != want {
		t.Errorf("A = %s, want %s", got, want)
	}
	if res.Unallocated != 0 {
		t.Errorf("unallocated = %s, want 0", res.Unallocated)
	}
}

func TestRoundToUnit(t *testing.T) {
	cases := []struct{ in, want money.Amount }{
		{money.FromFloat(100.4), money.FromMajor(100)},
		{money.FromFloat(100.5), money.FromMajor(101)},
		{money.FromFloat(-100.4), money.FromMajor(-100)},
		{money.FromFloat(-100.5), money.FromMajor(-101)},
		{money.FromMajor(100), money.FromMajor(100)},
	}
	for _, c := range cases {
		if got := roundToUnit(c.in); got != c.want {
			t.Errorf("roundToUnit(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

// A remainder rule must never be told to transfer a negative amount. Letting it
// quietly balance an over-committed plan would hide the mistake.
func TestRemainderNeverGoesNegative(t *testing.T) {
	rules := realistic()
	rules[1].Value = money.FromMajor(500000)
	res := Compute(money.FromMajor(200000), rules, nil, nil)

	arb := lineFor2(t, res, "Arbitrage")
	if arb.Amount < 0 {
		t.Errorf("arbitrage = %s, want no negative instruction", arb.Amount)
	}
	if res.Unallocated >= 0 {
		t.Errorf("unallocated = %s, want a negative figure showing the shortfall", res.Unallocated)
	}
}

// balances stands in for the database when testing formula rules.
type balances map[string]int64

func (b balances) Balance(sel string) (*big.Rat, bool) {
	v, ok := b[sel]
	if !ok {
		return nil, false
	}
	return new(big.Rat).SetInt64(v), true
}

// A fixed instalment taken before the split must not be included in the base the
// percentages are applied to. "50,000 to the tax deposit, then the rest gets
// split" has to mean exactly that.
func TestFixedInstalmentThenSplitTheRest(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Spends", AccountID: 10, Stage: StageSetAside,
			Kind: KindPercent, Basis: BasisIncome, Value: pct(40), Priority: 1, Active: true},
		{ID: 2, Name: "Tax FD", AccountID: 11, Stage: StageInvest,
			Kind: KindFixed, Value: money.FromMajor(50000), Priority: 1, Active: true},
		{ID: 3, Name: "Domestic equity", AccountID: 12, Stage: StageInvest,
			Kind: KindPercent, Basis: BasisRemaining, Value: pct(50), Priority: 2, Active: true},
		{ID: 4, Name: "Global equity", AccountID: 13, Stage: StageInvest,
			Kind: KindRemainder, Priority: 9, Active: true},
	}
	// Income 2,00,000; spends 80,000; investable 1,20,000; FD 50,000 leaves
	// 70,000, so domestic equity is half of *that*, not half of 1,20,000.
	res := Compute(money.FromMajor(200000), rules, nil, nil)

	if got, want := lineFor2(t, res, "Tax FD").Amount, money.FromMajor(50000); got != want {
		t.Errorf("tax FD = %s, want %s", got, want)
	}
	if got, want := lineFor2(t, res, "Domestic equity").Amount, money.FromMajor(35000); got != want {
		t.Errorf("domestic equity = %s, want %s (half of what is left after the FD)", got, want)
	}
	if got, want := lineFor2(t, res, "Global equity").Amount, money.FromMajor(35000); got != want {
		t.Errorf("global equity = %s, want %s", got, want)
	}
	if res.Unallocated != 0 {
		t.Errorf("unallocated = %s, want 0", res.Unallocated)
	}
}

// The formula that motivated the language: income lands in one account but
// spending happens from another, so the transfer has to cover a month's spending
// plus what is owed on the cards, less whatever is already in the account.
func TestSpendingTopUpAcrossAccounts(t *testing.T) {
	accts := balances{
		"Spending":             12000,
		"tag:type=credit_card": -43000,
	}
	rules := []Rule{
		{ID: 1, Name: "Top up spending", AccountID: 20, Stage: StageSetAside,
			Kind: KindFormula, Priority: 1, Active: true,
			Formula: `100000 + owed("tag:type=credit_card") - balance("Spending")`},
		{ID: 2, Name: "Tax FD", AccountID: 21, Stage: StageInvest,
			Kind: KindFixed, Value: money.FromMajor(50000), Priority: 1, Active: true},
		{ID: 3, Name: "Equity", AccountID: 22, Stage: StageInvest,
			Kind: KindRemainder, Priority: 9, Active: true},
	}
	res := Compute(money.FromMajor(300000), rules, nil, accts)

	if got, want := lineFor2(t, res, "Top up spending").Amount, money.FromMajor(131000); got != want {
		t.Errorf("spending top-up = %s, want %s", got, want)
	}
	if got, want := res.Investable, money.FromMajor(169000); got != want {
		t.Errorf("investable = %s, want %s", got, want)
	}
	if got, want := lineFor2(t, res, "Equity").Amount, money.FromMajor(119000); got != want {
		t.Errorf("equity = %s, want %s", got, want)
	}
}

// A formula may refer to what is still unallocated at the point it runs.
func TestFormulaSeesRunningTotals(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Spends", AccountID: 10, Stage: StageSetAside,
			Kind: KindFixed, Value: money.FromMajor(80000), Priority: 1, Active: true},
		{ID: 2, Name: "Tax FD", AccountID: 11, Stage: StageInvest,
			Kind: KindFixed, Value: money.FromMajor(50000), Priority: 1, Active: true},
		{ID: 3, Name: "Half of the rest", AccountID: 12, Stage: StageInvest,
			Kind: KindFormula, Formula: "remaining / 2", Priority: 2, Active: true},
	}
	res := Compute(money.FromMajor(200000), rules, nil, nil)
	if got, want := lineFor2(t, res, "Half of the rest").Amount, money.FromMajor(35000); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A formula may refer to an earlier rule by name, which is how "the same again
// into global equity" is written.
func TestFormulaCanReferToAnEarlierLine(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Domestic equity", AccountID: 10, Stage: StageInvest,
			Kind: KindFixed, Value: money.FromMajor(40000), Priority: 1, Active: true},
		{ID: 2, Name: "Global equity", AccountID: 11, Stage: StageInvest,
			Kind: KindFormula, Formula: `line("Domestic equity") / 2`, Priority: 2, Active: true},
	}
	res := Compute(money.FromMajor(100000), rules, nil, nil)
	if got, want := lineFor2(t, res, "Global equity").Amount, money.FromMajor(20000); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A broken formula must be reported, and must not silently route zero - which
// would look exactly like a deliberate decision to skip that account.
func TestBrokenFormulaIsReportedNotZeroed(t *testing.T) {
	rules := []Rule{
		{ID: 1, Name: "Top up", AccountID: 10, Stage: StageSetAside,
			Kind: KindFormula, Formula: `balance("No Such Account")`, Priority: 1, Active: true},
	}
	res := Compute(money.FromMajor(100000), rules, nil, balances{})
	if len(res.Warnings) == 0 {
		t.Fatal("a formula that cannot be evaluated must produce a warning")
	}
	for _, l := range res.Lines {
		if l.Label == "Top up" {
			t.Error("a rule that failed to evaluate must not appear as a routing instruction")
		}
	}
}

// A top-up must never ask for a negative transfer when the account is already
// funded; clamping is the user's job, and the language supports it.
func TestTopUpCanBeClampedToZero(t *testing.T) {
	accts := balances{"Spending": 250000}
	rules := []Rule{
		{ID: 1, Name: "Top up", AccountID: 10, Stage: StageSetAside, Kind: KindFormula,
			Formula: `max(0, 100000 - balance("Spending"))`, Priority: 1, Active: true},
	}
	res := Compute(money.FromMajor(200000), rules, nil, accts)
	if got := lineFor2(t, res, "Top up").Amount; got != 0 {
		t.Errorf("top up = %s, want 0 when the account is already funded", got)
	}
}

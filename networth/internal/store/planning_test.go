package store

import (
	"testing"

	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/plan"
)

// setupRouting builds the shape of a real monthly sheet: salary lands in one
// bank account, spending happens from another, and what is left is split.
func setupRouting(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	accts := []struct {
		name, kind string
		inNW       bool
		tags       map[string]string
	}{
		{"Salary Account", "asset", true, nil},
		{"Spending Account", "asset", true, nil},
		{"HDFC Card", "liability", true, map[string]string{"type": "credit_card"}},
		{"Amex", "liability", true, map[string]string{"type": "credit_card"}},
		{"Tax FD", "asset", true, nil},
		{"Domestic Equity", "asset", true, nil},
		{"Global Equity", "asset", true, nil},
	}
	for _, a := range accts {
		id, err := s.AddAccount(Account{
			Name: a.name, Kind: a.kind, Currency: "INR", InNetworth: a.inNW, Tags: a.tags})
		if err != nil {
			t.Fatalf("adding %s: %v", a.name, err)
		}
		ids[a.name] = id
	}
	if err := s.SetIncomeAccount(ids["Salary Account"]); err != nil {
		t.Fatal(err)
	}
	// Opening balances: something in the spending account, and money owed on the
	// cards.
	if err := s.RecordUpdate("2026-01-01", []Entry{
		{AccountID: ids["Salary Account"], Balance: money.FromMajor(50000), Currency: "INR"},
		{AccountID: ids["Spending Account"], Balance: money.FromMajor(12000), Currency: "INR"},
		{AccountID: ids["HDFC Card"], Balance: money.FromMajor(-35000), Currency: "INR"},
		{AccountID: ids["Amex"], Balance: money.FromMajor(-8000), Currency: "INR"},
		{AccountID: ids["Tax FD"], Balance: 0, Currency: "INR"},
		{AccountID: ids["Domestic Equity"], Balance: money.FromMajor(500000), Currency: "INR"},
		{AccountID: ids["Global Equity"], Balance: money.FromMajor(300000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func addRules(t *testing.T, s *Store, ids map[string]int64) {
	t.Helper()
	rules := []plan.Rule{
		{Name: "Top up spending", AccountID: ids["Spending Account"], Stage: plan.StageSetAside,
			Kind: plan.KindFormula, Priority: 1, Active: true,
			Formula: `100000 + owed("tag:type=credit_card") - balance("Spending Account")`},
		{Name: "Tax FD", AccountID: ids["Tax FD"], Stage: plan.StageInvest,
			Kind: plan.KindFixed, Value: money.FromMajor(50000), Priority: 1, Active: true},
		{Name: "Domestic Equity", AccountID: ids["Domestic Equity"], Stage: plan.StageInvest,
			Kind: plan.KindPercent, Basis: plan.BasisRemaining, Value: money.FromFloat(0.6),
			Priority: 2, Active: true},
		{Name: "Global Equity", AccountID: ids["Global Equity"], Stage: plan.StageInvest,
			Kind: plan.KindRemainder, Priority: 9, Active: true},
	}
	for _, r := range rules {
		if _, err := s.SaveRule(r); err != nil {
			t.Fatalf("saving rule %s: %v", r.Name, err)
		}
	}
}

func TestComputePlanResolvesFormulasAgainstRealBalances(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, err := s.ComputePlan(money.FromMajor(300000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	// 1,00,000 of spending + 43,000 owed on cards - 12,000 already there.
	var topUp money.Amount
	for _, l := range res.Lines {
		if l.Label == "Top up spending" {
			topUp = l.Amount
		}
	}
	if want := money.FromMajor(131000); topUp != want {
		t.Errorf("spending top-up = %s, want %s", topUp, want)
	}
	if got, want := res.Investable, money.FromMajor(169000); got != want {
		t.Errorf("investable = %s, want %s", got, want)
	}
}

// Routing money into a spending account you own is a transfer, not spending:
// the money is still yours until it is actually spent. So the whole income
// counts as having entered net worth, and the top-up cancels out.
//
// This is the correct reading and worth being explicit about, because the
// intuitive expectation is the opposite one.
func TestTopUpToAnOwnedAccountIsATransferNotSpending(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, err := s.ComputePlan(money.FromMajor(300000), nil)
	if err != nil {
		t.Fatal(err)
	}
	planID, err := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}

	flows, err := s.Flows("2026-01-01", "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	var net money.Amount
	for _, f := range flows {
		net += f.Amount
	}
	// The spending account is one of yours, so only the salary crossed the
	// boundary. Money is not spent by being moved.
	if want := money.FromMajor(300000); net != want {
		t.Errorf("net contribution = %s, want %s: moving money between your own "+
			"accounts does not spend it", net, want)
	}
}

// Pointing a set-aside at an account outside net worth records it as spending,
// which is how the plan captures money that really is gone. The target account's
// side of the boundary is what decides the meaning, and nothing else.
func TestSetAsideToAnOutsideAccountIsSpending(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)

	expenses, err := s.AddAccount(Account{Name: "Household", Kind: "expense",
		Currency: "INR", InNetworth: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRule(plan.Rule{Name: "Spending", AccountID: expenses,
		Stage: plan.StageSetAside, Kind: plan.KindFixed,
		Value: money.FromMajor(100000), Priority: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRule(plan.Rule{Name: "Equity", AccountID: ids["Domestic Equity"],
		Stage: plan.StageInvest, Kind: plan.KindRemainder, Priority: 9, Active: true}); err != nil {
		t.Fatal(err)
	}

	res, err := s.ComputePlan(money.FromMajor(300000), nil)
	if err != nil {
		t.Fatal(err)
	}
	planID, err := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}

	flows, err := s.Flows("2026-01-01", "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	var net money.Amount
	for _, f := range flows {
		net += f.Amount
	}
	if want := money.FromMajor(200000); net != want {
		t.Errorf("net contribution = %s, want %s (income less what was spent)", net, want)
	}
}

// Investment routing must not register as new money. If it did, every rupee
// would be counted twice - once arriving as salary, once again on the way to a
// fund - and returns would collapse.
func TestRoutingBetweenOwnAccountsIsNotAContribution(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, err := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}

	// The equity transfers moved real money but changed nothing overall.
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM v_flows
		WHERE description LIKE '%Equity%' OR description LIKE '%Tax FD%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d investment transfers were counted as flows across the boundary", n)
	}
}

// Balances must follow the money: after executing, the salary account is down by
// what left it and each target is up by what arrived.
func TestExecutedPlanMovesBalances(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, _ := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}

	bals, err := s.BalancesAt("2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]money.Amount{}
	for _, b := range bals {
		got[b.Account] = b.Balance
	}
	// Spending: 12,000 + 1,31,000. Tax FD: 0 + 50,000.
	if want := money.FromMajor(143000); got["Spending Account"] != want {
		t.Errorf("spending account = %s, want %s", got["Spending Account"], want)
	}
	if want := money.FromMajor(50000); got["Tax FD"] != want {
		t.Errorf("tax FD = %s, want %s", got["Tax FD"], want)
	}
	// Salary account: 50,000 opening + 3,00,000 in - everything routed out.
	if want := money.FromMajor(50000); got["Salary Account"] != want {
		t.Errorf("salary account = %s, want %s (everything routed out)", got["Salary Account"], want)
	}
}

// Executing twice would double a month's income. It must be refused.
func TestExecutingTwiceIsRefused(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, _ := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecutePlan(planID); err == nil {
		t.Error("executing a plan twice should be refused")
	}
}

// Getting the income wrong and being unable to take it back would make the tool
// something to be afraid of.
func TestRevertUndoesAnExecution(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, _ := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevertPlan(planID); err != nil {
		t.Fatal(err)
	}

	flows, err := s.Flows("2026-01-01", "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 0 {
		t.Errorf("%d flows survived the revert", len(flows))
	}
	p, found, err := s.PlanFor("2026-02-01")
	if err != nil || !found {
		t.Fatal("the plan itself should survive, as a draft")
	}
	if p.Executed {
		t.Error("the plan should be back to draft")
	}
	// And it must be executable again, not stuck.
	if err := s.ExecutePlan(planID); err != nil {
		t.Errorf("re-executing after a revert failed: %v", err)
	}
}

func TestExecutingWithoutAnIncomeAccountIsRefused(t *testing.T) {
	s := newStore(t)
	id := addAccount(t, s, "Fund", true)
	if _, err := s.SaveRule(plan.Rule{Name: "All of it", AccountID: id,
		Stage: plan.StageInvest, Kind: plan.KindRemainder, Active: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.ComputePlan(money.FromMajor(100000), nil)
	planID, err := s.SavePlan("2026-02-01", money.FromMajor(100000), res, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ExecutePlan(planID); err == nil {
		t.Error("executing without knowing where income lands should be refused")
	}
}

// A formula is validated when it is written, not when it silently routes nothing
// months later.
func TestBadFormulaIsRejectedOnSave(t *testing.T) {
	s := newStore(t)
	id := addAccount(t, s, "Spending", true)
	if _, err := s.SaveRule(plan.Rule{Name: "Broken", AccountID: id,
		Stage: plan.StageSetAside, Kind: plan.KindFormula,
		Formula: "100000 + (", Active: true}); err == nil {
		t.Error("an unparseable formula should be refused")
	}
}

// The monthly plan is what makes the twice-yearly balance round easy: by then
// the contributions are already recorded, so the "of which added" field can be
// left blank. The interface needs this figure to say so.
func TestRecordedContributionsAreVisibleToTheUpdateScreen(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, _ := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}

	rec, err := s.RecordedContributions()
	if err != nil {
		t.Fatal(err)
	}
	// The tax FD received 50,000 since it was last valued.
	if got, want := rec[ids["Tax FD"]], money.FromMajor(50000); got != want {
		t.Errorf("recorded into the tax FD = %s, want %s", got, want)
	}
}

func TestSavePlanRefusesToRewriteAnExecutedMonth(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	res, _ := s.ComputePlan(money.FromMajor(300000), nil)
	planID, _ := s.SavePlan("2026-02-01", money.FromMajor(300000), res, "")
	if err := s.ExecutePlan(planID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePlan("2026-02-01", money.FromMajor(400000), res, ""); err == nil {
		t.Error("an executed month is history and must not be silently rewritten")
	}
}

// A new credit card should be covered by the existing formula the moment it is
// tagged, without editing the rule.
func TestTagSelectorPicksUpNewAccounts(t *testing.T) {
	s := newStore(t)
	ids := setupRouting(t, s)
	addRules(t, s, ids)

	newCard, err := s.AddAccount(Account{Name: "ICICI Card", Kind: "liability",
		Currency: "INR", InNetworth: true, Tags: map[string]string{"type": "credit_card"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUpdate("2026-01-15", []Entry{
		{AccountID: newCard, Balance: money.FromMajor(-7000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := s.ComputePlan(money.FromMajor(300000), nil)
	if err != nil {
		t.Fatal(err)
	}
	var topUp money.Amount
	for _, l := range res.Lines {
		if l.Label == "Top up spending" {
			topUp = l.Amount
		}
	}
	// Now 50,000 owed across three cards, less the 12,000 already in hand.
	if want := money.FromMajor(138000); topUp != want {
		t.Errorf("spending top-up = %s, want %s after tagging a third card", topUp, want)
	}
}

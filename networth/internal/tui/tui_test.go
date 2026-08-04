package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/plan"
	"github.com/akagr/finance-tools/networth/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

// newModel builds the interface over a database seeded with two rounds of
// balances, which is the smallest history that produces a real answer.
func newModel(t *testing.T, seeded bool) *Model {
	t.Helper()
	d, created, err := db.Open(filepath.Join(t.TempDir(), "nw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s := store.New(d)

	if seeded {
		bank, err := s.AddAccount(store.Account{
			Name: "ICICI Savings", Kind: "asset", Currency: "INR", InNetworth: true})
		if err != nil {
			t.Fatal(err)
		}
		fund, err := s.AddAccount(store.Account{
			Name: "Flexi Cap Fund", Kind: "asset", Currency: "INR", InNetworth: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordUpdate("2025-01-01", []store.Entry{
			{AccountID: bank, Balance: money.FromMajor(400000), Currency: "INR"},
			{AccountID: fund, Balance: money.FromMajor(1600000), Currency: "INR"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordUpdate("2025-07-01", []store.Entry{
			{AccountID: bank, Balance: money.FromMajor(450000), Currency: "INR"},
			{AccountID: fund, Balance: money.FromMajor(2000000), Currency: "INR",
				Contribution: money.FromMajor(250000)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	m, err := New(s, d.Path, created && !seeded)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return m
}

func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "ctrl+s":
			msg = tea.KeyMsg{Type: tea.KeyCtrlS}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		model, cmd := m.Update(msg)
		m = model.(*Model)
		// Commands here are synchronous state changes (switch view, refresh,
		// status); running them keeps the test faithful to the real loop.
		drain(m, cmd)
	}
}

func drain(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 10; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				drain(m, c)
			}
			return
		}
		_, cmd = m.Update(msg)
	}
}

func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(*Model)
		drain(m, cmd)
	}
}

// The footer must always show the keys that work right now. That is the whole
// discoverability mechanism, so it is asserted rather than assumed.
func TestFooterAlwaysShowsAvailableKeys(t *testing.T) {
	m := newModel(t, true)
	for i, name := range viewNames {
		m.active = view(i)
		out := m.View()
		if !strings.Contains(out, "help") || !strings.Contains(out, "quit") {
			t.Errorf("%s: footer is missing the global keys", name)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: rendered nothing", name)
		}
	}
}

func TestDashboardShowsTheContributionSplit(t *testing.T) {
	m := newModel(t, true)
	m.active = viewDashboard
	out := m.View()

	for _, want := range []string{"Net worth", "You put in", "Markets gave", "Change"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard is missing %q:\n%s", want, out)
		}
	}
	// 20,00,000 became 24,50,000, of which 2,50,000 was paid in: 2,00,000 earned.
	if !strings.Contains(out, "2,00,000") {
		t.Errorf("dashboard does not show the earned amount:\n%s", out)
	}
}

func TestDashboardExplainsItselfWhenEmpty(t *testing.T) {
	m := newModel(t, false)
	m.active = viewDashboard
	out := m.View()
	if !strings.Contains(out, "Nothing recorded yet") {
		t.Errorf("an empty database should say what to do next:\n%s", out)
	}
	if !strings.Contains(out, "Accounts") {
		t.Error("the empty state should point at the next step")
	}
}

// A first run must land somewhere the user can act, not on an empty dashboard.
func TestFirstRunStartsOnAccounts(t *testing.T) {
	m := newModel(t, false)
	if m.active != viewAccounts {
		t.Errorf("first run opened %s, want Accounts", viewNames[m.active])
	}
}

func TestNumberKeysSwitchScreens(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "4")
	if m.active != viewAccounts {
		t.Errorf("pressing 4 went to %s, want Accounts", viewNames[m.active])
	}
	press(t, m, "1")
	if m.active != viewDashboard {
		t.Errorf("pressing 1 went to %s, want Dashboard", viewNames[m.active])
	}
}

func TestHelpOverlayOpensAndCloses(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open help")
	}
	if !strings.Contains(m.View(), "Typing amounts") {
		t.Error("help should explain that amount fields accept arithmetic")
	}
	press(t, m, "esc")
	if m.showHelp {
		t.Error("esc did not close help")
	}
}

// While a text field has focus, single-letter shortcuts must not be stolen from
// the field. Typing "quit" into a balance should not quit.
func TestTypingDoesNotTriggerGlobalShortcuts(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	if m.active != viewUpdate {
		t.Fatalf("expected the update screen, got %s", viewNames[m.active])
	}
	typeText(t, m, "q")
	if m.quitting {
		t.Fatal("typing q into a balance field quit the application")
	}
	u := m.screens[viewUpdate].(*updateScreen)
	if got := u.balanceInput.Value(); got != "q" {
		t.Errorf("field value = %q, want the typed character", got)
	}
}

// The update round is the path that has to be effortless: type, enter, type,
// enter, review, save.
func TestUpdateRoundSavesBalancesAndContributions(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)
	u.date = "2026-01-01"

	// First account: a new balance, no contribution.
	typeText(t, m, "500000")
	press(t, m, "enter") // move to "of which added"
	press(t, m, "enter") // next account

	// Second account: a balance and the money paid in.
	typeText(t, m, "2400000")
	press(t, m, "enter")
	typeText(t, m, "200000")
	press(t, m, "enter") // last account rolls into the review

	if u.phase != phaseReview {
		t.Fatalf("expected the review screen, got phase %v", u.phase)
	}
	review := m.View()
	if !strings.Contains(review, "Review") {
		t.Errorf("review screen did not render:\n%s", review)
	}
	if !strings.Contains(review, "Markets gave") {
		t.Error("the review should show the contribution/return split before saving")
	}

	press(t, m, "enter") // save

	sum, err := m.store.Summarise("2025-07-01")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(2900000); got != want {
		t.Errorf("net worth = %s, want %s", got, want)
	}
	if got, want := sum.Contribution, money.FromMajor(200000); got != want {
		t.Errorf("contribution = %s, want %s", got, want)
	}
	// 24,50,000 -> 29,00,000 is +4,50,000, of which 2,00,000 was paid in.
	if got, want := sum.MarketReturn, money.FromMajor(250000); got != want {
		t.Errorf("market return = %s, want %s", got, want)
	}
}

// Amount fields accept arithmetic, so a figure can be typed the way it is
// thought of rather than pre-computed elsewhere.
func TestUpdateFieldEvaluatesArithmetic(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)
	u.date = "2026-01-01"

	typeText(t, m, "450000+62000")
	if !strings.Contains(m.View(), "5,12,000") {
		t.Errorf("the live preview should evaluate the expression:\n%s", m.View())
	}
	press(t, m, "enter")
	press(t, m, "enter")

	if got := u.entries[u.accounts[0].ID].balance; got != money.FromMajor(512000) {
		t.Errorf("stored balance = %s, want 5,12,000", got)
	}
}

// Leaving a balance blank means "unchanged", which is how an account that has
// not moved is skipped without ceremony.
func TestBlankBalanceLeavesAnAccountAlone(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)

	press(t, m, "enter") // blank balance
	press(t, m, "enter") // blank contribution, moves on

	if _, recorded := u.entries[u.accounts[0].ID]; recorded {
		t.Error("a blank entry should not be recorded")
	}
	if u.idx != 1 {
		t.Errorf("cursor is on account %d, want it moved on to 1", u.idx)
	}
}

// A mistyped amount must be caught while typing, not swallowed and saved.
func TestInvalidAmountIsReportedNotSaved(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)

	typeText(t, m, "abc")
	press(t, m, "enter")
	if u.fieldErr == "" {
		t.Error("an unparseable amount should produce a visible error")
	}
	if !strings.Contains(m.View(), u.fieldErr) {
		t.Error("the error should be shown on screen")
	}
	if _, recorded := u.entries[u.accounts[0].ID]; recorded {
		t.Error("an invalid amount must not be recorded")
	}
}

// Abandoning a half-finished round should take a deliberate second keystroke,
// because losing entered data to a stray key is unforgivable in a tool used
// twice a year.
func TestCancellingAPartialRoundAsksTwice(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)

	typeText(t, m, "500000")
	press(t, m, "enter")
	press(t, m, "esc")

	if m.active != viewUpdate {
		t.Fatal("the first esc should not have left the screen")
	}
	if u.fieldErr == "" {
		t.Error("the first esc should warn that the round will be discarded")
	}
	press(t, m, "esc")
	if m.active != viewDashboard {
		t.Error("the second esc should abandon the round")
	}
}

func TestAccountsScreenAddsAnAccount(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "4")
	press(t, m, "a")

	s := m.screens[viewAccounts].(*accountsScreen)
	if s.form == nil {
		t.Fatal("a did not open the new-account form")
	}
	if !strings.Contains(m.View(), "Counts towards net worth") {
		t.Error("the form should explain the net worth boundary in place")
	}

	typeText(t, m, "NPS")
	press(t, m, "ctrl+s")

	acct, found, err := m.store.AccountByName("NPS")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("the account was not saved")
	}
	if !acct.InNetworth || acct.Currency != "INR" {
		t.Errorf("saved account = %+v, want sensible defaults", acct)
	}
}

func TestHistoryShowsPerPeriodAttribution(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "5")
	out := m.View()
	for _, want := range []string{"From", "You added", "Earned"} {
		if !strings.Contains(out, want) {
			t.Errorf("history is missing the %q column:\n%s", want, out)
		}
	}
}

func TestQueryScreenRunsASavedQuery(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "6")
	q := m.screens[viewQuery].(*queryScreen)
	if len(q.queries) == 0 {
		t.Fatal("no saved queries were listed")
	}
	press(t, m, "enter")
	if q.resErr != nil {
		t.Fatalf("running the first saved query failed: %v", q.resErr)
	}
	if q.result == nil {
		t.Fatal("no result was produced")
	}
	if !strings.Contains(m.View(), "Result") {
		t.Error("the result table did not render")
	}
}

// Every screen must survive a narrow terminal without panicking or producing
// nothing.
func TestScreensRenderAtSmallSizes(t *testing.T) {
	for _, size := range []struct{ w, h int }{{60, 20}, {80, 24}, {200, 60}} {
		m := newModel(t, true)
		m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for i := range viewNames {
			m.active = view(i)
			if strings.TrimSpace(m.View()) == "" {
				t.Errorf("%s rendered nothing at %dx%d", viewNames[i], size.w, size.h)
			}
		}
	}
}

// A debt typed as a positive number must be stored as a negative balance.
// Getting this wrong moves net worth by twice the loan, silently, so it is
// handled for the user rather than left as a rule to remember.
func TestLiabilityEnteredPositiveIsStoredAsDebt(t *testing.T) {
	m := newModel(t, true)
	if _, err := m.store.AddAccount(store.Account{
		Name: "Home loan", Kind: "liability", Currency: "INR", InNetworth: true}); err != nil {
		t.Fatal(err)
	}
	press(t, m, "3")
	u := m.screens[viewUpdate].(*updateScreen)
	u.date = "2026-01-01"

	// Walk to the loan.
	for u.accounts[u.idx].Kind != "liability" {
		press(t, m, "down")
		if u.idx == len(u.accounts)-1 && u.accounts[u.idx].Kind != "liability" {
			t.Fatal("the liability account was not offered in the round")
		}
	}
	if got := balanceLabel(u.accounts[u.idx]); got != "Amount owed" {
		t.Errorf("field label = %q, want %q", got, "Amount owed")
	}

	typeText(t, m, "4200000")
	press(t, m, "enter")
	press(t, m, "enter")

	var loanID int64
	for _, a := range u.accounts {
		if a.Kind == "liability" {
			loanID = a.ID
		}
	}
	got := u.entries[loanID].balance
	if got != money.FromMajor(-4200000) {
		t.Errorf("stored balance = %s, want -42,00,000: a debt typed as a positive "+
			"number must reduce net worth", got)
	}
}

// An explicit minus sign must still be honoured, so the convenience above never
// blocks someone who knows exactly what they mean.
func TestLiabilityEnteredNegativeIsLeftAlone(t *testing.T) {
	a := store.Account{Kind: "liability"}
	if got := normaliseForKind(a, money.FromMajor(-500)); got != money.FromMajor(-500) {
		t.Errorf("got %s, want -500", got)
	}
	asset := store.Account{Kind: "asset"}
	if got := normaliseForKind(asset, money.FromMajor(500)); got != money.FromMajor(500) {
		t.Errorf("an asset must never be negated: got %s", got)
	}
}

// setupPlanRules gives the model the shape of a real routing sheet.
func setupPlanRules(t *testing.T, m *Model) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, a := range []struct {
		name, kind string
		tags       map[string]string
	}{
		{"Salary Account", "asset", nil},
		{"Spending Account", "asset", nil},
		{"HDFC Card", "liability", map[string]string{"type": "credit_card"}},
		{"Tax FD", "asset", nil},
	} {
		id, err := m.store.AddAccount(store.Account{Name: a.name, Kind: a.kind,
			Currency: "INR", InNetworth: true, Tags: a.tags})
		if err != nil {
			t.Fatal(err)
		}
		ids[a.name] = id
	}
	if err := m.store.SetIncomeAccount(ids["Salary Account"]); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordUpdate("2026-01-01", []store.Entry{
		{AccountID: ids["Spending Account"], Balance: money.FromMajor(12000), Currency: "INR"},
		{AccountID: ids["HDFC Card"], Balance: money.FromMajor(-43000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []plan.Rule{
		{Name: "Top up spending", AccountID: ids["Spending Account"],
			Stage: plan.StageSetAside, Kind: plan.KindFormula, Priority: 1, Active: true,
			Formula: `100000 + owed("tag:type=credit_card") - balance("Spending Account")`},
		{Name: "Tax FD", AccountID: ids["Tax FD"], Stage: plan.StageInvest,
			Kind: plan.KindFixed, Value: money.FromMajor(50000), Priority: 2, Active: true},
		{Name: "Flexi Cap Fund", AccountID: 0, Stage: plan.StageInvest,
			Kind: plan.KindRemainder, Priority: 9, Active: true},
	} {
		if r.AccountID == 0 {
			acct, found, err := m.store.AccountByName("Flexi Cap Fund")
			if err != nil || !found {
				t.Fatal("expected the seeded fund account")
			}
			r.AccountID = acct.ID
		}
		if _, err := m.store.SaveRule(r); err != nil {
			t.Fatalf("saving %s: %v", r.Name, err)
		}
	}
	for _, sc := range m.screens {
		if err := sc.Refresh(); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// The routing must update as the income is typed. That immediacy is what makes
// this feel like the spreadsheet it replaces.
func TestPlanRecomputesAsIncomeIsTyped(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")
	if m.active != viewPlan {
		t.Fatalf("pressing 2 went to %s, want Plan", viewNames[m.active])
	}

	typeText(t, m, "300000")
	out := m.View()
	// 1,00,000 spending + 43,000 owed - 12,000 already there.
	if !strings.Contains(out, "1,31,000") {
		t.Errorf("the formula rule did not resolve against real balances:\n%s", out)
	}
	if !strings.Contains(out, "50,000") {
		t.Errorf("the fixed tax deposit is missing:\n%s", out)
	}
	// 3,00,000 - 1,31,000 - 50,000 sweeps into the fund.
	if !strings.Contains(out, "1,19,000") {
		t.Errorf("the remainder did not sweep up:\n%s", out)
	}
}

func TestPlanSavesAndRecordsTheMonth(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")

	p := m.screens[viewPlan].(*planScreen)
	p.date = "2026-02-01"
	typeText(t, m, "300000")
	press(t, m, "ctrl+s")

	saved, found, err := m.store.PlanFor("2026-02-01")
	if err != nil || !found {
		t.Fatalf("the plan was not saved: %v", err)
	}
	if !saved.Executed {
		t.Error("saving should also record where the money went")
	}

	// The contributions are now on record, which is the whole point.
	rec, err := m.store.RecordedContributions()
	if err != nil {
		t.Fatal(err)
	}
	var total money.Amount
	for _, v := range rec {
		total += v
	}
	if total == 0 {
		t.Error("executing a plan should record contributions against accounts")
	}
}

// Recording a month twice would double that month's income, so it must be
// undoable rather than merely refused.
func TestPlanCanBeUndone(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")

	p := m.screens[viewPlan].(*planScreen)
	p.date = "2026-02-01"
	typeText(t, m, "300000")
	press(t, m, "ctrl+s")
	press(t, m, "ctrl+z")

	saved, found, err := m.store.PlanFor("2026-02-01")
	if err != nil || !found {
		t.Fatal("the plan itself should survive an undo")
	}
	if saved.Executed {
		t.Error("ctrl+z should return the month to a draft")
	}
}

// Overriding one line for a single month - "no PPF this month" - must not
// require editing the standing rule.
func TestPlanLineCanBeOverriddenForOneMonth(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")

	p := m.screens[viewPlan].(*planScreen)
	typeText(t, m, "300000")

	// Move to the tax deposit line and set it to zero for this month.
	for i, l := range p.result.Lines {
		if l.Label == "Tax FD" {
			p.cursor = i
		}
	}
	press(t, m, "enter")
	press(t, m, "ctrl+u")
	typeText(t, m, "0")
	press(t, m, "enter")

	for _, l := range p.result.Lines {
		if l.Label == "Tax FD" && l.Amount != 0 {
			t.Errorf("tax FD = %s, want the override of 0", l.Amount)
		}
		// The remainder must grow to keep the plan balanced.
		if l.Label == "Flexi Cap Fund" && l.Amount != money.FromMajor(169000) {
			t.Errorf("remainder = %s, want 1,69,000 after skipping the deposit", l.Amount)
		}
	}
}

// The rule editor must accept a formula and show what it currently evaluates to,
// because seeing the number is the only reliable check that it says what was
// meant.
func TestRuleEditorPreviewsAFormula(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")
	press(t, m, "ctrl+r")

	p := m.screens[viewPlan].(*planScreen)
	if p.mode != planRules {
		t.Fatal("ctrl+r did not open the rules")
	}
	if !strings.Contains(m.View(), "Top up spending") {
		t.Errorf("the rules list did not render:\n%s", m.View())
	}

	press(t, m, "e")
	if p.editor == nil {
		t.Fatal("e did not open the rule editor")
	}
	// The "how much" field holds the formula, and its help must be visible.
	p.editor.focus(ruleFieldHowMuch)
	out := m.View()
	if !strings.Contains(out, "owed(") {
		t.Errorf("the formula vocabulary should be on screen:\n%s", out)
	}
	if !strings.Contains(out, "1,31,000") {
		t.Errorf("the formula preview should show what it comes to now:\n%s", out)
	}
}

func TestParseHowMuchCoversEveryRuleShape(t *testing.T) {
	cases := []struct {
		in    string
		kind  plan.Kind
		basis plan.Basis
	}{
		{"50000", plan.KindFixed, plan.BasisInvestable},
		{"12,500", plan.KindFixed, plan.BasisInvestable},
		{"40%", plan.KindPercent, plan.BasisRemaining},
		{"40% of income", plan.KindPercent, plan.BasisIncome},
		{"40% of investable", plan.KindPercent, plan.BasisInvestable},
		{"40% of the rest", plan.KindPercent, plan.BasisRemaining},
		{"remainder", plan.KindRemainder, plan.BasisInvestable},
		{`100000 - balance("Spending Account")`, plan.KindFormula, plan.BasisInvestable},
		{"remaining / 2", plan.KindFormula, plan.BasisInvestable},
	}
	for _, c := range cases {
		kind, basis, _, _, err := parseHowMuch(c.in)
		if err != nil {
			t.Errorf("parseHowMuch(%q): %v", c.in, err)
			continue
		}
		if kind != c.kind {
			t.Errorf("parseHowMuch(%q) kind = %s, want %s", c.in, kind, c.kind)
		}
		if kind == plan.KindPercent && basis != c.basis {
			t.Errorf("parseHowMuch(%q) basis = %s, want %s", c.in, basis, c.basis)
		}
	}
	if _, _, _, _, err := parseHowMuch("100000 + ("); err == nil {
		t.Error("a broken formula should be refused as it is typed")
	}
}

// Once a monthly plan has recorded a contribution, the balance round must say so
// rather than inviting the same figure to be entered again.
func TestUpdateScreenShowsAlreadyRecordedContributions(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")
	p := m.screens[viewPlan].(*planScreen)
	p.date = "2026-02-01"
	typeText(t, m, "300000")
	press(t, m, "ctrl+s")

	press(t, m, "esc", "3")
	u := m.screens[viewUpdate].(*updateScreen)
	for i, a := range u.accounts {
		if a.Name == "Tax FD" {
			u.idx = i
			u.syncInputs()
		}
	}
	out := m.View()
	if !strings.Contains(out, "already recorded") {
		t.Errorf("the update screen should say the contribution is already on record:\n%s", out)
	}
}

// A text field must never trap the cursor: esc always hands the number keys back
// to navigation, or there is no way off the screen.
func TestEscapeLeavesInputModeOnThePlanScreen(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")

	p := m.screens[viewPlan].(*planScreen)
	if !p.Capturing() {
		t.Fatal("the income field should have focus on arrival")
	}
	typeText(t, m, "300000")

	press(t, m, "esc")
	if p.Capturing() {
		t.Fatal("esc should have left input mode")
	}
	if !strings.Contains(m.View(), "i") {
		t.Error("the footer should offer a way back into the field")
	}
	press(t, m, "4")
	if m.active != viewAccounts {
		t.Errorf("after esc, 4 should navigate; went to %s", viewNames[m.active])
	}

	press(t, m, "2", "i")
	if !p.Capturing() {
		t.Error("i should put the cursor back in the income field")
	}
}

// Arbitrary tags must be settable from the Accounts screen. Formulas select
// accounts by tag, so a tag that can only be added with raw SQL makes the
// formula language unusable from the interface.
func TestAccountFormSetsArbitraryTags(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "4")
	press(t, m, "a")

	s := m.screens[viewAccounts].(*accountsScreen)
	if s.form == nil {
		t.Fatal("a did not open the form")
	}
	typeText(t, m, "HDFC Card")
	s.form.focus(1)
	s.form.fields[1].SetValue("liability")
	s.form.focus(6)
	typeText(t, m, "type=credit_card, asset_class=debt")
	press(t, m, "ctrl+s")

	acct, found, err := m.store.AccountByName("HDFC Card")
	if err != nil || !found {
		t.Fatalf("account not saved: %v", err)
	}
	if acct.Tags["type"] != "credit_card" {
		t.Errorf("tags = %v, want type=credit_card", acct.Tags)
	}
	if acct.Tags["asset_class"] != "debt" {
		t.Errorf("tags = %v, want asset_class=debt", acct.Tags)
	}
	if acct.Kind != "liability" {
		t.Errorf("kind = %q, want liability", acct.Kind)
	}
}

// Once a card is tagged, the selector a formula uses must resolve.
func TestTaggedCardMakesTheSelectorResolve(t *testing.T) {
	m := newModel(t, true)
	card, err := m.store.AddAccount(store.Account{Name: "HDFC Card", Kind: "liability",
		Currency: "INR", InNetworth: true, Tags: map[string]string{"type": "credit_card"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordUpdate("2026-01-01", []store.Entry{
		{AccountID: card, Balance: money.FromMajor(-35000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	bals, err := m.store.Balances()
	if err != nil {
		t.Fatal(err)
	}
	v, ok := bals.Balance("tag:type=credit_card")
	if !ok {
		t.Fatalf("tag:type=credit_card did not resolve; selectors are %v", bals.Selectors())
	}
	if v.RatString() != "-35000" {
		t.Errorf("balance = %s, want -35000", v.RatString())
	}
}

func TestTagFieldRejectsMalformedInput(t *testing.T) {
	if _, err := parseTagField("type credit_card"); err == nil {
		t.Error("a tag without = should be refused")
	}
	if _, err := parseTagField("type="); err == nil {
		t.Error("a tag with no value should be refused")
	}
	got, err := parseTagField(" type=credit_card ,  owner=self ")
	if err != nil {
		t.Fatal(err)
	}
	if got["type"] != "credit_card" || got["owner"] != "self" {
		t.Errorf("parsed = %v", got)
	}
	if formatTags(got) != "owner=self, type=credit_card" {
		t.Errorf("formatTags = %q", formatTags(got))
	}
}

func TestChoiceFieldCyclesAndShowsEveryOption(t *testing.T) {
	f := newChoiceField("Kind", "", []string{"asset", "liability", "income"})
	if f.Value() != "asset" {
		t.Errorf("initial value = %q, want asset", f.Value())
	}
	// Every valid value is on screen, so there is nothing to look up.
	out := f.View(10, true)
	for _, want := range []string{"asset", "liability", "income"} {
		if !strings.Contains(out, want) {
			t.Errorf("choice field should show %q:\n%s", want, out)
		}
	}
	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if f.Value() != "liability" {
		t.Errorf("after right = %q, want liability", f.Value())
	}
	f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if f.Value() != "asset" {
		t.Errorf("after left = %q, want asset", f.Value())
	}
	// Typing the first letter jumps, which is faster than cycling.
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if f.Value() != "income" {
		t.Errorf("after typing i = %q, want income", f.Value())
	}
	// Cycling must wrap rather than dead-end.
	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if f.Value() != "asset" {
		t.Errorf("cycling past the end = %q, want it to wrap to asset", f.Value())
	}
	// Tab belongs to the form: a choice field must not steal it, or moving
	// through a form would behave differently depending on the field.
	if handled, _ := f.Update(tea.KeyMsg{Type: tea.KeyTab}); handled {
		t.Error("tab should pass through to the form, not cycle the choice")
	}
}

// A text field must not swallow the keys that move between fields, or the form
// traps the cursor on whichever field it started on.
func TestTextFieldPassesNavigationKeysThrough(t *testing.T) {
	f := newTextField("Name", "")
	f.Focus()
	for _, k := range []tea.KeyType{tea.KeyTab, tea.KeyDown, tea.KeyUp, tea.KeyEnter, tea.KeyEsc} {
		if handled, _ := f.Update(tea.KeyMsg{Type: k}); handled {
			t.Errorf("%v should pass through to the form", k)
		}
	}
	if handled, _ := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); !handled {
		t.Error("ordinary typing should be consumed by the field")
	}
}

// Moving through every field and back must be possible with tab alone.
func TestFormTabsThroughEveryField(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "4")
	press(t, m, "a")
	s := m.screens[viewAccounts].(*accountsScreen)

	n := len(s.form.fields)
	for i := 1; i < n; i++ {
		press(t, m, "tab")
		if s.form.field != i {
			t.Fatalf("after %d tabs the cursor is on field %d, want %d", i, s.form.field, i)
		}
	}
	press(t, m, "tab")
	if s.form.field != 0 {
		t.Errorf("tab past the last field = %d, want it to wrap to 0", s.form.field)
	}
}

func TestSuggestFieldFiltersAndAccepts(t *testing.T) {
	f := newSuggestField("To account", "",
		[]string{"HDFC Card", "Salary Account", "Spending Account", "Tax FD"}, true)
	f.Focus()

	// Matching anywhere, not just at the start, is what makes "card" find
	// "HDFC Card".
	for _, r := range "card" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(f.matches) != 1 || f.matches[0] != "HDFC Card" {
		t.Errorf("matches = %v, want [HDFC Card]", f.matches)
	}
	if !f.open {
		t.Error("the list should be open while typing")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.Value() != "HDFC Card" {
		t.Errorf("after tab = %q, want the completed name", f.Value())
	}
	if f.open {
		t.Error("accepting should close the list")
	}
}

func TestSuggestFieldArrowsPickFromTheList(t *testing.T) {
	f := newSuggestField("To account", "",
		[]string{"Salary Account", "Spending Account"}, true)
	f.Focus()
	for _, r := range "acc" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.Value() != "Spending Account" {
		t.Errorf("value = %q, want the second match", f.Value())
	}
}

// A strict field must refuse a value that is not on the list, because a rule
// pointing at an account that does not exist would fail silently every month.
func TestStrictSuggestFieldRejectsUnknownValues(t *testing.T) {
	f := newSuggestField("To account", "", []string{"Tax FD"}, true)
	f.SetValue("Tax FDD")
	if ok, why := f.Valid(); ok {
		t.Error("a value not on the list should be refused")
	} else if why == "" {
		t.Error("the refusal should explain itself")
	}
	f.SetValue("Tax FD")
	if ok, _ := f.Valid(); !ok {
		t.Error("an exact match should be accepted")
	}
}

// A non-strict field is a suggestion, not a constraint: a new currency has to be
// possible.
func TestNonStrictSuggestFieldAcceptsNewValues(t *testing.T) {
	f := newSuggestField("Currency", "", []string{"INR", "USD"}, false)
	f.SetValue("SGD")
	if ok, _ := f.Valid(); !ok {
		t.Error("a suggestion field should not block a new value")
	}
}

// The rule editor must offer the accounts rather than asking for an exact name.
func TestRuleFormCompletesTheDestinationAccount(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")
	press(t, m, "ctrl+r")
	press(t, m, "a")

	p := m.screens[viewPlan].(*planScreen)
	if p.editor == nil {
		t.Fatal("a did not open the rule editor")
	}
	p.editor.focus(ruleFieldAccount)
	typeText(t, m, "tax")

	out := m.View()
	if !strings.Contains(out, "Tax FD") {
		t.Errorf("the destination field should complete against real accounts:\n%s", out)
	}
	press(t, m, "tab")
	if got := p.editor.fields[ruleFieldAccount].Value(); got != "Tax FD" {
		t.Errorf("accepted value = %q, want Tax FD", got)
	}
}

// The account kind must be a choice, not a spelling test.
func TestAccountFormKindIsAChoice(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "4")
	press(t, m, "a")
	s := m.screens[viewAccounts].(*accountsScreen)

	s.form.focus(1)
	out := m.View()
	for _, want := range []string{"asset", "liability", "income", "expense", "equity"} {
		if !strings.Contains(out, want) {
			t.Errorf("kind field should show %q as an option:\n%s", want, out)
		}
	}
	press(t, m, "right")
	if got := s.form.fields[1].Value(); got != "liability" {
		t.Errorf("after right, kind = %q, want liability", got)
	}
}

// Tags already in use should be offered, so a convention spreads without having
// to be remembered.
func TestTagFieldSuggestsExistingTags(t *testing.T) {
	m := newModel(t, true)
	if _, err := m.store.AddAccount(store.Account{Name: "Amex", Kind: "liability",
		Currency: "INR", InNetworth: true,
		Tags: map[string]string{"type": "credit_card"}}); err != nil {
		t.Fatal(err)
	}
	press(t, m, "4")
	press(t, m, "a")
	s := m.screens[viewAccounts].(*accountsScreen)

	found := false
	for _, o := range s.form.fields[6].options {
		if o == "type=credit_card" {
			found = true
		}
	}
	if !found {
		t.Errorf("tag suggestions should include tags already in use: %v",
			s.form.fields[6].options)
	}
}

// Changing an account's tracking mode must never make it silently disappear
// from the balance round. A unit-tracked account only drops out once it actually
// holds something that can be valued from a price.
func TestUnitTrackedAccountStaysInTheRoundUntilItHoldsSomething(t *testing.T) {
	m := newModel(t, true)
	id, err := m.store.AddAccount(store.Account{Name: "Vested RSUs", Kind: "asset",
		Currency: "INR", InNetworth: true, Tracking: "units"})
	if err != nil {
		t.Fatal(err)
	}

	u := m.screens[viewUpdate].(*updateScreen)
	if err := u.Refresh(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range u.accounts {
		if a.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("a unit-tracked account holding nothing must still be askable by hand")
	}

	// Once it has holdings, its balance is derivable and asking would be noise.
	inst, err := m.store.DB.Exec(`INSERT INTO instruments (symbol, currency) VALUES ('ACME','INR')`)
	if err != nil {
		t.Fatal(err)
	}
	instID, _ := inst.LastInsertId()
	if _, err := m.store.DB.Exec(`INSERT INTO trades
		(account_id, instrument_id, as_of, quantity, currency)
		VALUES (?,?,?,?,'INR')`, id, instID, "2026-01-01", 100*100000000); err != nil {
		t.Fatal(err)
	}
	if err := u.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, a := range u.accounts {
		if a.ID == id {
			t.Error("once it holds something, its balance comes from the price")
		}
	}
}

// The bookkeeping counter-parties are not things anyone values by hand.
func TestBookkeepingAccountsAreNotAskedFor(t *testing.T) {
	m := newModel(t, true)
	setupPlanRules(t, m)
	press(t, m, "2")
	p := m.screens[viewPlan].(*planScreen)
	p.date = "2026-02-01"
	typeText(t, m, "300000")
	press(t, m, "ctrl+s")

	u := m.screens[viewUpdate].(*updateScreen)
	if err := u.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, a := range u.accounts {
		switch a.Name {
		case store.ExternalAccount, store.IncomeAccount, store.SpendAccount:
			t.Errorf("%s is bookkeeping and should not be asked for", a.Name)
		}
	}
}

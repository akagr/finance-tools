package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
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
	press(t, m, "3")
	if m.active != viewAccounts {
		t.Errorf("pressing 3 went to %s, want Accounts", viewNames[m.active])
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
	press(t, m, "2")
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
	press(t, m, "2")
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
	press(t, m, "2")
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
	press(t, m, "2")
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
	press(t, m, "2")
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
	press(t, m, "2")
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
	press(t, m, "3")
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
	press(t, m, "4")
	out := m.View()
	for _, want := range []string{"From", "You added", "Earned"} {
		if !strings.Contains(out, want) {
			t.Errorf("history is missing the %q column:\n%s", want, out)
		}
	}
}

func TestQueryScreenRunsASavedQuery(t *testing.T) {
	m := newModel(t, true)
	press(t, m, "5")
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
	press(t, m, "2")
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

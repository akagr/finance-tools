package store

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, _, err := db.Open(filepath.Join(t.TempDir(), "nw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func addAccount(t *testing.T, s *Store, name string, inNW bool) int64 {
	t.Helper()
	id, err := s.AddAccount(Account{Name: name, Kind: "asset", Currency: "INR", InNetworth: inNW})
	if err != nil {
		t.Fatalf("adding %s: %v", name, err)
	}
	return id
}

// TestUpdateRoundSeparatesContributionFromReturn is the end-to-end proof of the
// whole design: two balance updates six months apart, with the contribution
// captured alongside the balance, must split the change correctly.
func TestUpdateRoundSeparatesContributionFromReturn(t *testing.T) {
	s := newStore(t)
	bank := addAccount(t, s, "Bank", true)
	equity := addAccount(t, s, "Equity", true)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: bank, Balance: money.FromMajor(100000), Currency: "INR"},
		{AccountID: equity, Balance: money.FromMajor(500000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	// Six months later: 6,00,000 has become 7,50,000, but 1,00,000 of that was
	// money paid in. Only 50,000 was actually earned.
	if err := s.RecordUpdate("2025-07-01", []Entry{
		{AccountID: bank, Balance: money.FromMajor(120000), Currency: "INR"},
		{AccountID: equity, Balance: money.FromMajor(630000), Currency: "INR",
			Contribution: money.FromMajor(100000)},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(750000); got != want {
		t.Errorf("net worth = %s, want %s", got, want)
	}
	if got, want := sum.Change, money.FromMajor(150000); got != want {
		t.Errorf("change = %s, want %s", got, want)
	}
	if got, want := sum.Contribution, money.FromMajor(100000); got != want {
		t.Errorf("contribution = %s, want %s", got, want)
	}
	if got, want := sum.MarketReturn, money.FromMajor(50000); got != want {
		t.Errorf("market return = %s, want %s — this is the number a balance-only sheet cannot produce", got, want)
	}
	if sum.Change != sum.Contribution+sum.MarketReturn {
		t.Error("change must decompose exactly into contribution plus market return")
	}
	if sum.ValuationPoints != 2 {
		t.Errorf("valuation points = %d, want 2", sum.ValuationPoints)
	}
}

// A contribution is dated to the midpoint of the period when the exact date is
// unknown. Dating it at either end would bias every time-weighted return.
func TestContributionDefaultsToPeriodMidpoint(t *testing.T) {
	s := newStore(t)
	acct := addAccount(t, s, "PPF", true)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(100000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUpdate("2025-07-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(160000), Currency: "INR",
			Contribution: money.FromMajor(50000)},
	}); err != nil {
		t.Fatal(err)
	}

	flows, err := s.Flows("2025-01-01", "2025-07-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected one flow, got %d", len(flows))
	}
	if flows[0].Date != "2025-04-01" {
		t.Errorf("contribution dated %s, want the midpoint 2025-04-01", flows[0].Date)
	}
}

// The snapshot already includes the contribution, so the posting recorded
// alongside it must not be added a second time.
func TestContributionIsNotDoubleCounted(t *testing.T) {
	s := newStore(t)
	acct := addAccount(t, s, "Fund", true)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(100000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUpdate("2025-07-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(150000), Currency: "INR",
			Contribution: money.FromMajor(40000)},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(150000); got != want {
		t.Errorf("net worth = %s, want %s (the balance you typed, not balance plus contribution)", got, want)
	}
}

// Moving money between two accounts you own changes nothing, and must not
// register as a contribution.
func TestTransferBetweenOwnAccountsIsNotAContribution(t *testing.T) {
	s := newStore(t)
	bank := addAccount(t, s, "Bank", true)
	equity := addAccount(t, s, "Equity", true)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: bank, Balance: money.FromMajor(200000), Currency: "INR"},
		{AccountID: equity, Balance: money.FromMajor(0), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	// 50,000 moved from the bank into equity; net worth is unchanged.
	if err := s.RecordUpdate("2025-07-01", []Entry{
		{AccountID: bank, Balance: money.FromMajor(150000), Currency: "INR"},
		{AccountID: equity, Balance: money.FromMajor(50000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Contribution != 0 {
		t.Errorf("contribution = %s, want 0: nothing entered net worth", sum.Contribution)
	}
	if sum.MarketReturn != 0 {
		t.Errorf("market return = %s, want 0", sum.MarketReturn)
	}
}

func TestReturnsAreComputedOverTheWholeHistory(t *testing.T) {
	s := newStore(t)
	acct := addAccount(t, s, "Equity", true)

	// Three yearly valuations, 10% growth each, no contributions.
	for _, e := range []struct {
		date string
		bal  int64
	}{{"2023-01-01", 100000}, {"2024-01-01", 110000}, {"2025-01-01", 121000}} {
		if err := s.RecordUpdate(e.date, []Entry{
			{AccountID: acct, Balance: money.FromMajor(e.bal), Currency: "INR"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if !sum.HasTWR {
		t.Fatal("expected a time-weighted return")
	}
	if math.Abs(sum.TWR.Annualised-0.10) > 0.01 {
		t.Errorf("annualised TWR = %.4f, want about 0.10", sum.TWR.Annualised)
	}
	if !sum.HasXIRR {
		t.Fatal("expected a money-weighted return")
	}
	// With no flows the two measures must agree.
	if math.Abs(sum.XIRR-sum.TWR.Annualised) > 0.01 {
		t.Errorf("with no contributions XIRR %.4f and TWR %.4f should agree", sum.XIRR, sum.TWR.Annualised)
	}
}

func TestUpdateIsIdempotentForTheSameDate(t *testing.T) {
	s := newStore(t)
	acct := addAccount(t, s, "Bank", true)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(100000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	// Correcting a typo by re-running the same date must replace, not stack up.
	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(123000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	bals, err := s.BalancesAt("2025-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(bals) != 1 || bals[0].Balance != money.FromMajor(123000) {
		t.Errorf("balance = %v, want a single corrected 123000", bals)
	}
}

func TestAccountsOutsideNetWorthAreExcluded(t *testing.T) {
	s := newStore(t)
	inside := addAccount(t, s, "Bank", true)
	outside := addAccount(t, s, "Employer ESOP pool", false)

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: inside, Balance: money.FromMajor(100000), Currency: "INR"},
		{AccountID: outside, Balance: money.FromMajor(999999), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(100000); got != want {
		t.Errorf("net worth = %s, want %s", got, want)
	}
}

func TestLiabilitiesReduceNetWorth(t *testing.T) {
	s := newStore(t)
	house, err := s.AddAccount(Account{Name: "Flat", Kind: "asset", Currency: "INR", InNetworth: true})
	if err != nil {
		t.Fatal(err)
	}
	loan, err := s.AddAccount(Account{Name: "Home loan", Kind: "liability", Currency: "INR", InNetworth: true})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: house, Balance: money.FromMajor(10000000), Currency: "INR"},
		{AccountID: loan, Balance: money.FromMajor(-4000000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(6000000); got != want {
		t.Errorf("net worth = %s, want %s", got, want)
	}
	if got, want := sum.Liabilities, money.FromMajor(-4000000); got != want {
		t.Errorf("liabilities = %s, want %s", got, want)
	}
}

func TestDeleteAccountRefusesToDiscardHistory(t *testing.T) {
	s := newStore(t)
	acct := addAccount(t, s, "Bank", true)
	if err := s.RecordUpdate("2025-01-01", []Entry{
		{AccountID: acct, Balance: money.FromMajor(1000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(acct); err == nil {
		t.Error("deleting an account with history should be refused")
	}
}

func TestEmptyDatabaseSummarisesWithoutError(t *testing.T) {
	s := newStore(t)
	sum, err := s.Summarise("")
	if err != nil {
		t.Fatalf("an empty database is a normal first run, not an error: %v", err)
	}
	if sum.NetWorth != 0 || sum.ValuationPoints != 0 {
		t.Errorf("expected an empty summary, got %+v", sum)
	}
}

// A first round of balances must report a net worth and nothing else. Showing a
// return derived from one measurement would be a fabricated number on the very
// first screen a new user sees.
func TestSingleValuationReportsNoReturn(t *testing.T) {
	s := newStore(t)
	bank := addAccount(t, s, "Bank", true)
	fund := addAccount(t, s, "Fund", true)

	if err := s.RecordUpdate("2026-08-04", []Entry{
		{AccountID: bank, Balance: money.FromMajor(250000), Currency: "INR"},
		{AccountID: fund, Balance: money.FromMajor(400000), Currency: "INR"},
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summarise("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum.NetWorth, money.FromMajor(650000); got != want {
		t.Errorf("net worth = %s, want %s", got, want)
	}
	if sum.HasXIRR {
		t.Errorf("reported a money-weighted return of %.1f%% from a single valuation", sum.XIRR*100)
	}
	if sum.HasTWR {
		t.Errorf("reported a time-weighted return of %.1f%% from a single valuation",
			sum.TWR.Annualised*100)
	}
}

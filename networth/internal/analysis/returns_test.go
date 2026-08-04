package analysis

import (
	"math"
	"testing"
	"time"

	"github.com/akagr/finance-tools/networth/internal/money"
)

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func closeTo(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, want %.6f (tolerance %.6f)", what, got, want, tol)
	}
}

func TestXIRRSimpleDoubling(t *testing.T) {
	// 100 invested, 200 back after exactly a year: 100%.
	r, err := XIRR([]Cashflow{
		{date("2024-01-01"), money.FromMajor(-100)},
		{date("2024-12-31"), money.FromMajor(200)},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, r, 1.0, 0.01, "XIRR")
}

func TestXIRRFlatIsZero(t *testing.T) {
	r, err := XIRR([]Cashflow{
		{date("2020-01-01"), money.FromMajor(-1000)},
		{date("2025-01-01"), money.FromMajor(1000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, r, 0, 1e-6, "XIRR")
}

func TestXIRRLoss(t *testing.T) {
	r, err := XIRR([]Cashflow{
		{date("2024-01-01"), money.FromMajor(-1000)},
		{date("2025-01-01"), money.FromMajor(900)},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, r, -0.10, 0.005, "XIRR")
}

func TestXIRRWithInterimContributions(t *testing.T) {
	// Regular contributions plus a closing value: the classic SIP shape.
	r, err := XIRR([]Cashflow{
		{date("2023-01-01"), money.FromMajor(-100000)},
		{date("2023-07-01"), money.FromMajor(-50000)},
		{date("2024-01-01"), money.FromMajor(-50000)},
		{date("2025-01-01"), money.FromMajor(230000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r < 0.05 || r > 0.30 {
		t.Errorf("XIRR = %.4f, expected a plausible positive rate", r)
	}
}

func TestXIRRNeedsBothDirections(t *testing.T) {
	_, err := XIRR([]Cashflow{
		{date("2024-01-01"), money.FromMajor(100)},
		{date("2025-01-01"), money.FromMajor(200)},
	})
	if err == nil {
		t.Error("expected an error when all cashflows share a sign")
	}
}

func TestModifiedDietzNoFlows(t *testing.T) {
	r, err := ModifiedDietz(Period{
		Start: date("2024-01-01"), End: date("2025-01-01"),
		StartValue: money.FromMajor(1000), EndValue: money.FromMajor(1100),
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, r, 0.10, 1e-9, "return")
}

// A contribution must not be mistaken for performance. This is the exact error
// the whole tool exists to eliminate, so it is asserted directly.
func TestModifiedDietzExcludesContributions(t *testing.T) {
	r, err := ModifiedDietz(Period{
		Start: date("2024-01-01"), End: date("2025-01-01"),
		StartValue: money.FromMajor(1000), EndValue: money.FromMajor(2000),
		Flows: []Cashflow{{date("2024-12-31"), money.FromMajor(1000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The whole rise was cash added on the last day: the return is ~0, not 100%.
	closeTo(t, r, 0, 0.01, "return")
}

func TestModifiedDietzWeightsByTimeInvested(t *testing.T) {
	// Money present for half the period counts half in the denominator.
	r, err := ModifiedDietz(Period{
		Start: date("2024-01-01"), End: date("2025-01-01"),
		StartValue: money.FromMajor(1000), EndValue: money.FromMajor(2200),
		Flows: []Cashflow{{date("2024-07-02"), money.FromMajor(1000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Gain 200 over an average base of about 1500.
	closeTo(t, r, 200.0/1500.0, 0.01, "return")
}

func TestTWRChainsAndAnnualises(t *testing.T) {
	res, err := TWR([]Period{
		{Start: date("2023-01-01"), End: date("2024-01-01"),
			StartValue: money.FromMajor(100), EndValue: money.FromMajor(110)},
		{Start: date("2024-01-01"), End: date("2024-12-31"),
			StartValue: money.FromMajor(110), EndValue: money.FromMajor(121)},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, res.Total, 0.21, 0.001, "cumulative TWR")
	closeTo(t, res.Annualised, 0.10, 0.005, "annualised TWR")
	if res.Periods != 2 {
		t.Errorf("periods = %d, want 2", res.Periods)
	}
}

// The headline claim of the tool: when a large contribution lands just before a
// bad stretch, the money-weighted return is dragged down even though the
// portfolio's own performance was unchanged. The gap between the two measures is
// the contribution-timing effect, and it is the number worth showing.
func TestContributionTimingSeparatesXIRRFromTWR(t *testing.T) {
	// The portfolio gains 30% in the first year and loses 10% in the second.
	// Most of the money only arrives in time for the loss.
	periods := []Period{
		{Start: date("2023-01-01"), End: date("2024-01-01"),
			StartValue: money.FromMajor(100000), EndValue: money.FromMajor(130000)},
		{Start: date("2024-01-01"), End: date("2025-01-01"),
			StartValue: money.FromMajor(130000), EndValue: money.FromMajor(1017000),
			Flows: []Cashflow{{date("2024-01-02"), money.FromMajor(1000000)}}},
	}
	twr, err := TWR(periods)
	if err != nil {
		t.Fatal(err)
	}
	// Time-weighted: 1.30 * 0.90 - 1 = 17% over two years, about 8% a year.
	closeTo(t, twr.Total, 0.17, 0.01, "cumulative TWR")

	xirr, err := XIRR([]Cashflow{
		{date("2023-01-01"), money.FromMajor(-100000)},
		{date("2024-01-02"), money.FromMajor(-1000000)},
		{date("2025-01-01"), money.FromMajor(1017000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The investor actually lost money despite a portfolio that gained 17%.
	if xirr >= 0 {
		t.Errorf("XIRR = %.4f, expected a loss: most of the money only saw the bad year", xirr)
	}
	if xirr >= twr.Annualised {
		t.Errorf("XIRR %.4f should trail TWR %.4f when contributions are badly timed",
			xirr, twr.Annualised)
	}
}

func TestAnnualiseLeavesShortSpansAlone(t *testing.T) {
	got, ann := Annualise(0.05, 30)
	if ann {
		t.Error("a 30-day span should not be annualised")
	}
	closeTo(t, got, 0.05, 1e-9, "return")

	got, ann = Annualise(0.21, 730)
	if !ann {
		t.Error("a two-year span should be annualised")
	}
	closeTo(t, got, 0.10, 0.005, "annualised")
}

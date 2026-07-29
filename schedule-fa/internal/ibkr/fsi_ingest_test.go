package ibkr

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// The income schedules run on the Apr–Mar financial year, so the parser must
// window on the period rather than a calendar year, and must pull out the data
// Schedule FA never needed: closed lots, interest, and withholding that matches
// no distribution.
func TestParseFlexPeriod_FinancialYear(t *testing.T) {
	st, err := ParseFlexFilePeriod("testdata/sample_flex_fy.xml", FinancialYear(2025))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if got, want := st.From.Format("2006-01-02"), "2025-04-01"; got != want {
		t.Errorf("period start = %s, want %s", got, want)
	}
	if got, want := st.To.Format("2006-01-02"), "2026-03-31"; got != want {
		t.Errorf("period end = %s, want %s", got, want)
	}
	// Year is the calendar year only; an FY statement must not masquerade as one,
	// or Schedule FA code could silently consume it.
	if st.Year != 0 {
		t.Errorf("Year = %d, want 0 for a financial-year statement", st.Year)
	}

	// A CLOSED_LOT row is a breakdown of a closing trade, not a separate
	// execution: counting it as a trade would double the proceeds.
	if got, want := len(st.Trades), 2; got != want {
		t.Errorf("trades = %d, want %d (CLOSED_LOT rows must not be counted as executions)", got, want)
	}
	proceeds := new(big.Rat)
	for _, tr := range st.Trades {
		proceeds.Add(proceeds, tr.Proceeds.Amount)
	}
	if got, want := proceeds.FloatString(2), "3970.00"; got != want {
		t.Errorf("total trade proceeds = %s, want %s", got, want)
	}

	// Three closed lots: two sibling CLOSED_LOT rows and one nested <Lot>.
	if got, want := len(st.RealizedLots), 3; got != want {
		t.Fatalf("realized lots = %d, want %d", got, want)
	}
	byOpen := map[string]int{}
	for _, l := range st.RealizedLots {
		byOpen[l.OpenDate.Format("2006-01-02")]++
	}
	for _, d := range []string{"2022-05-10", "2025-01-15", "2023-11-20"} {
		if byOpen[d] != 1 {
			t.Errorf("no closed lot acquired on %s (got %v)", d, byOpen)
		}
	}
	var nested bool
	for _, l := range st.RealizedLots {
		if l.Instrument.Symbol != "VWRA" {
			continue
		}
		nested = true
		if got, want := l.Cost.Amount.FloatString(2), "400.00"; got != want {
			t.Errorf("nested lot cost = %s, want %s", got, want)
		}
		if got, want := l.Proceeds.Amount.FloatString(2), "520.00"; got != want {
			t.Errorf("nested lot proceeds = %s, want %s", got, want)
		}
		if got, want := l.CloseDate.Format("2006-01-02"), "2026-02-10"; got != want {
			t.Errorf("nested lot close date = %s, want %s", got, want)
		}
	}
	if !nested {
		t.Error("the nested <Lot> under the VWRA trade was not parsed")
	}

	// A payment in lieu is a credit like a dividend but is withheld at the US
	// statutory 30%, so it has to stay distinguishable.
	var cash, inLieu int
	for _, d := range st.Dividends {
		switch d.Kind {
		case "PAYMENT_IN_LIEU":
			inLieu++
		default:
			cash++
		}
		if d.PayDate.After(time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("dividend on %s is outside the financial year", d.PayDate)
		}
	}
	if cash != 2 || inLieu != 1 {
		t.Errorf("dividends: %d cash + %d in lieu, want 2 + 1", cash, inLieu)
	}

	// Interest, including the negative row for interest paid.
	if got, want := len(st.Interest), 2; got != want {
		t.Fatalf("interest rows = %d, want %d", got, want)
	}
	var received, paid bool
	for _, in := range st.Interest {
		if in.Amount.Amount.Sign() > 0 {
			received = true
		} else {
			paid = true
		}
	}
	if !received || !paid {
		t.Errorf("interest: received=%v paid=%v, want both", received, paid)
	}

	// Withholding that matches no distribution is still a creditable foreign
	// tax; dropping it silently would forfeit the credit.
	if got, want := len(st.UnmatchedWithholding), 1; got != want {
		t.Fatalf("unmatched withholding rows = %d, want %d", got, want)
	}
	w := st.UnmatchedWithholding[0]
	if got, want := w.Amount.Amount.FloatString(2), "2.50"; got != want {
		t.Errorf("unmatched withholding = %s, want %s (positive tax)", got, want)
	}
	if got, want := w.Instrument.Symbol, "MSFT"; got != want {
		t.Errorf("unmatched withholding symbol = %s, want %s", got, want)
	}

	// The withholding that DID match a dividend must not also appear as
	// unmatched, and must be attached to its dividend.
	for _, d := range st.Dividends {
		if d.PayDate.Format("2006-01-02") == "2025-08-14" {
			if got, want := d.Withholding.Amount.FloatString(2), "10.00"; got != want {
				t.Errorf("matched withholding = %s, want %s", got, want)
			}
		}
	}
}

// A calendar-year parse must still behave exactly as Schedule FA expects.
func TestCalendarYearStillSetsYear(t *testing.T) {
	st, err := ParseFlexFile("testdata/sample_flex.xml", 2024)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if st.Year != 2024 {
		t.Errorf("Year = %d, want 2024", st.Year)
	}
	if got, want := st.From.Format("2006-01-02"), "2024-01-01"; got != want {
		t.Errorf("period start = %s, want %s", got, want)
	}
}

// A withholding reversal is posted as a positive amount and must net the tax
// down, not add to it.
func TestWithholdingReversalNets(t *testing.T) {
	const xml = `<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1">
	  <CashTransactions>
	    <CashTransaction type="Dividends" symbol="AAA" isin="US0000000001" currency="USD" amount="100.00" dateTime="20250610"/>
	    <CashTransaction type="Withholding Tax" symbol="AAA" isin="US0000000001" currency="USD" amount="-25.00" dateTime="20250610"/>
	    <CashTransaction type="Withholding Tax" symbol="AAA" isin="US0000000001" currency="USD" amount="10.00" dateTime="20250610"/>
	  </CashTransactions>
	</FlexStatement></FlexStatements></FlexQueryResponse>`
	st, err := ParseFlexPeriod(strings.NewReader(xml), FinancialYear(2025))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(st.Dividends) != 1 {
		t.Fatalf("dividends = %d, want 1", len(st.Dividends))
	}
	if got, want := st.Dividends[0].Withholding.Amount.FloatString(2), "15.00"; got != want {
		t.Errorf("withholding after reversal = %s, want %s", got, want)
	}
}

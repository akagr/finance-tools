package ibkr

import (
	"math/big"
	"testing"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

func TestParseFlexFile(t *testing.T) {
	st, err := ParseFlexFile("testdata/sample_flex.xml", 2024)
	if err != nil {
		t.Fatalf("ParseFlexFile: %v", err)
	}

	// Account.
	if st.Account.Number != "U1234567" {
		t.Errorf("account number = %q, want U1234567", st.Account.Number)
	}
	if st.Account.Name != "Jane Doe" {
		t.Errorf("account name = %q, want Jane Doe", st.Account.Name)
	}
	if st.Account.BaseCurrency != model.USD {
		t.Errorf("base currency = %q, want USD", st.Account.BaseCurrency)
	}
	if got, want := st.Account.OpenDate, date(2021, 5, 10); !got.Equal(want) {
		t.Errorf("open date = %v, want %v", got, want)
	}
	if st.Account.Institution != "Interactive Brokers LLC" {
		t.Errorf("institution = %q, want Interactive Brokers LLC", st.Account.Institution)
	}
	if st.Account.Street != "12 Market St" || st.Account.PostalCode != "400001" || st.Account.Country != "IN" {
		t.Errorf("account address not parsed: %+v", st.Account)
	}

	// One corporate action (AAPL 4-for-1 split) within the year.
	if len(st.CorporateActions) != 1 {
		t.Fatalf("corporate actions = %d, want 1", len(st.CorporateActions))
	}
	if ca := st.CorporateActions[0]; ca.Instrument.Symbol != "AAPL" || !ca.Date.Equal(date(2024, 8, 28)) {
		t.Errorf("unexpected corporate action: %+v", ca)
	}

	// Two open positions (AAPL, VOO); MSFT was fully exited intra-year. VOO's two
	// LOT rows must aggregate into one position of 10 shares with two lots.
	if len(st.OpenPositions) != 2 {
		t.Fatalf("open positions = %d, want 2 (VOO lots must aggregate)", len(st.OpenPositions))
	}
	voo := findPosition(t, st, "VOO")
	if !ratEq(voo.Quantity, big.NewRat(10, 1)) {
		t.Errorf("VOO aggregated position = %s, want 10", voo.Quantity.RatString())
	}
	if n := len(lotsFor(st, "VOO")); n != 2 {
		t.Errorf("VOO lots = %d, want 2", n)
	}

	// AAPL closing value = 100 * 250 = 25000 USD.
	aapl := findPosition(t, st, "AAPL")
	if !ratEq(aapl.Quantity, big.NewRat(100, 1)) {
		t.Errorf("AAPL position = %s, want 100", aapl.Quantity.RatString())
	}
	closing := new(big.Rat).Mul(aapl.Quantity, aapl.MarkPrice.Amount)
	if !ratEq(closing, big.NewRat(25000, 1)) {
		t.Errorf("AAPL closing = %s, want 25000", closing.RatString())
	}

	// Lot detail: AAPL has two lots (60 @ 2023-01-10, 40 @ 2024-03-15).
	aaplLots := lotsFor(st, "AAPL")
	if len(aaplLots) != 2 {
		t.Fatalf("AAPL lots = %d, want 2", len(aaplLots))
	}
	if got := earliestLotDate(aaplLots); !got.Equal(date(2023, 1, 10)) {
		t.Errorf("AAPL earliest lot = %v, want 2023-01-10", got)
	}

	// Trades: 4 in-year (2 buys, 1 buy, 1 sell). Quantities are absolute.
	if len(st.Trades) != 4 {
		t.Fatalf("trades = %d, want 4", len(st.Trades))
	}
	sell := findTrade(t, st, "MSFT", model.Sell)
	if !ratEq(sell.Quantity, big.NewRat(20, 1)) {
		t.Errorf("MSFT sell qty = %s, want 20", sell.Quantity.RatString())
	}
	if !ratEq(sell.Proceeds.Amount, big.NewRat(9000, 1)) {
		t.Errorf("MSFT sell proceeds = %s, want 9000", sell.Proceeds.Amount.RatString())
	}

	// Dividends: 2 in-year (AAPL, VOO); the 2025-01-02 row is filtered out.
	if len(st.Dividends) != 2 {
		t.Fatalf("dividends = %d, want 2 (out-of-year row must be excluded)", len(st.Dividends))
	}
	div := findDividend(t, st, "AAPL")
	if !ratEq(div.Gross.Amount, big.NewRat(25, 1)) {
		t.Errorf("AAPL dividend gross = %s, want 25", div.Gross.Amount.RatString())
	}
	// Withholding matched to the same-day dividend, stored as positive magnitude.
	if !ratEq(div.Withholding.Amount, big.NewRat(25, 4)) { // 6.25
		t.Errorf("AAPL withholding = %s, want 6.25", div.Withholding.Amount.FloatString(2))
	}
}

// --- test helpers ---

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func ratEq(a, b *big.Rat) bool { return a != nil && b != nil && a.Cmp(b) == 0 }

func findPosition(t *testing.T, st *model.Statement, sym string) model.Position {
	t.Helper()
	for _, p := range st.OpenPositions {
		if p.Instrument.Symbol == sym {
			return p
		}
	}
	t.Fatalf("position %s not found", sym)
	return model.Position{}
}

func lotsFor(st *model.Statement, sym string) []model.Lot {
	var out []model.Lot
	for _, l := range st.Lots {
		if l.Instrument.Symbol == sym {
			out = append(out, l)
		}
	}
	return out
}

func earliestLotDate(lots []model.Lot) time.Time {
	var min time.Time
	for _, l := range lots {
		if min.IsZero() || l.OpenDate.Before(min) {
			min = l.OpenDate
		}
	}
	return min
}

func findTrade(t *testing.T, st *model.Statement, sym string, side model.Side) model.Trade {
	t.Helper()
	for _, tr := range st.Trades {
		if tr.Instrument.Symbol == sym && tr.Side == side {
			return tr
		}
	}
	t.Fatalf("trade %s %s not found", sym, side)
	return model.Trade{}
}

func findDividend(t *testing.T, st *model.Statement, sym string) model.Dividend {
	t.Helper()
	for _, d := range st.Dividends {
		if d.Instrument.Symbol == sym {
			return d
		}
	}
	t.Fatalf("dividend %s not found", sym)
	return model.Dividend{}
}

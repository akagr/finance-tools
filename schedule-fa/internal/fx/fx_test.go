package fx

import (
	"math/big"
	"testing"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

func loadTestStore(t *testing.T) *CSVStore {
	t.Helper()
	s := NewCSVStore()
	if err := s.LoadRateKeeperFile(model.USD, "testdata/SBI_REFERENCE_RATES_USD.csv"); err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestRateOn(t *testing.T) {
	s := loadTestStore(t)

	cases := []struct {
		name     string
		on       time.Time
		wantRate string    // RatString of INRPerUnit; "" => expect error
		wantDate time.Time // date actually used
	}{
		{"exact day", day(2024, 1, 5), "831/10", day(2024, 1, 5)},
		{"intraday revision keeps latest", day(2024, 6, 14), "1671/20", day(2024, 6, 14)}, // 83.55
		{"weekend falls back to preceding", day(2024, 6, 15), "1671/20", day(2024, 6, 14)},
		{"year end", day(2024, 12, 31), "1711/20", day(2024, 12, 31)}, // 85.55
		{"zero-rate day is skipped, no earlier rate -> error", day(2024, 1, 4), "", time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := s.RateOn(model.USD, tc.on)
			if tc.wantRate == "" {
				if err == nil {
					t.Fatalf("expected error, got rate %v", r.INRPerUnit)
				}
				return
			}
			if err != nil {
				t.Fatalf("RateOn: %v", err)
			}
			if r.INRPerUnit.RatString() != tc.wantRate {
				t.Errorf("rate = %s, want %s", r.INRPerUnit.RatString(), tc.wantRate)
			}
			if !r.Date.Equal(tc.wantDate) {
				t.Errorf("rate date = %v, want %v", r.Date, tc.wantDate)
			}
		})
	}
}

func TestConvert(t *testing.T) {
	s := loadTestStore(t)

	// 100 USD valued on 31-Dec-2024 at TTBR 85.55 -> 8555 INR.
	amt := model.NewMoney(model.USD, big.NewRat(100, 1))
	c, err := Convert(s, amt, day(2024, 12, 31))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if c.Result.Currency != model.INR {
		t.Errorf("result currency = %s, want INR", c.Result.Currency)
	}
	if c.Result.Amount.Cmp(big.NewRat(8555, 1)) != 0 {
		t.Errorf("result = %s, want 8555", c.Result.Amount.RatString())
	}
	if !c.RateDate.Equal(day(2024, 12, 31)) {
		t.Errorf("rate date = %v, want 2024-12-31", c.RateDate)
	}

	// A value dated on a weekend uses the preceding rate, and the audit trail
	// records the actual (preceding) rate date.
	c2, err := Convert(s, amt, day(2024, 6, 15))
	if err != nil {
		t.Fatalf("Convert weekend: %v", err)
	}
	if !c2.RateDate.Equal(day(2024, 6, 14)) {
		t.Errorf("weekend rate date = %v, want 2024-06-14 (fallback)", c2.RateDate)
	}

	// INR passes through unchanged with no rate lookup.
	inr := model.NewMoney(model.INR, big.NewRat(500, 1))
	c3, err := Convert(s, inr, day(2024, 6, 15))
	if err != nil {
		t.Fatalf("Convert INR: %v", err)
	}
	if c3.Result.Amount.Cmp(big.NewRat(500, 1)) != 0 {
		t.Errorf("INR passthrough = %s, want 500", c3.Result.Amount.RatString())
	}
}

func TestCurrencyFromFilename(t *testing.T) {
	cases := map[string]model.Currency{
		"SBI_REFERENCE_RATES_USD.csv":      model.USD,
		"/a/b/SBI_REFERENCE_RATES_EUR.csv": "EUR",
		"rates-gbp.csv":                    "GBP",
		"random.csv":                       "",
	}
	for in, want := range cases {
		if got := currencyFromFilename(in); got != want {
			t.Errorf("currencyFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

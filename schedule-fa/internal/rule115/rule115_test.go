package rule115

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// The specified date is the last day of the PRECEDING month — which for a
// January event is 31 December of the previous year, and which has to respect
// February's length.
func TestMonthEndBefore(t *testing.T) {
	cases := []struct{ event, want string }{
		{"2025-08-20", "2025-07-31"},
		{"2025-01-05", "2024-12-31"},
		{"2025-03-01", "2025-02-28"},
		{"2024-03-31", "2024-02-29"}, // leap year
		{"2026-01-31", "2025-12-31"},
		{"2025-05-31", "2025-04-30"},
	}
	for _, c := range cases {
		if got := MonthEndBefore(day(c.event)).Format("2006-01-02"); got != c.want {
			t.Errorf("MonthEndBefore(%s) = %s, want %s", c.event, got, c.want)
		}
	}
	if !MonthEndBefore(time.Time{}).IsZero() {
		t.Error("MonthEndBefore(zero) should stay zero")
	}
}

func TestFYEndAndSpecifiedDate(t *testing.T) {
	if got, want := FYEnd(2025).Format("2006-01-02"), "2026-03-31"; got != want {
		t.Errorf("FYEnd(2025) = %s, want %s", got, want)
	}
	// Other income is pinned to the year end whatever the event date, while
	// every other head follows the event.
	if got, want := SpecifiedDate(OtherIncome, day("2025-12-31"), 2025).Format("2006-01-02"), "2026-03-31"; got != want {
		t.Errorf("other income specified date = %s, want %s", got, want)
	}
	if got, want := SpecifiedDate(CapitalGains, day("2025-08-20"), 2025).Format("2006-01-02"), "2025-07-31"; got != want {
		t.Errorf("capital gains specified date = %s, want %s", got, want)
	}
	if got, want := SpecifiedDate(ForeignTax, day("2025-09-05"), 2025).Format("2006-01-02"), "2025-08-31"; got != want {
		t.Errorf("Rule 128(8) date = %s, want %s", got, want)
	}
}

// Convert must go through the fx store's preceding-working-day fallback, which
// matters because a month-end routinely falls on a weekend or holiday: 31 Aug
// 2025 is a Sunday, so the rate published on 29 Aug applies.
func TestConvertUsesPrecedingWorkingDay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SBI_REFERENCE_RATES_USD.csv")
	csv := "DATE,PDF FILE,TT BUY,TT SELL\n" +
		"2025-07-31 09:00,x,87.20,88.05\n" +
		"2025-08-29 09:00,x,87.40,88.25\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	store := fx.NewCSVStore()
	if err := store.LoadRateKeeperFile(model.USD, path); err != nil {
		t.Fatal(err)
	}

	// A dividend paid on 5 Sep 2025: specified date 31 Aug, which was not
	// published, so the 29 Aug rate is used and recorded.
	got, err := Convert(store, model.NewMoney(model.USD, big.NewRat(100, 1)), Dividend, day("2025-09-05"), 2025)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if want := "2025-08-29"; got.RateDate.Format("2006-01-02") != want {
		t.Errorf("rate date = %s, want %s", got.RateDate.Format("2006-01-02"), want)
	}
	if want := "8740.00"; got.Result.Amount.FloatString(2) != want {
		t.Errorf("converted = %s, want %s", got.Result.Amount.FloatString(2), want)
	}

	// A sale on 20 Aug 2025 converts at the 31 Jul rate, NOT the sale-date rate
	// Schedule FA would have used.
	got, err = Convert(store, model.NewMoney(model.USD, big.NewRat(100, 1)), CapitalGains, day("2025-08-20"), 2025)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if want := "2025-07-31"; got.RateDate.Format("2006-01-02") != want {
		t.Errorf("rate date = %s, want %s", got.RateDate.Format("2006-01-02"), want)
	}
}

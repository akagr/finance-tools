package prices

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "prices.csv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestPriceOnWithFallback(t *testing.T) {
	p := writeCSV(t, "date,symbol,isin,close,currency\n"+
		"2024-06-13,AAPL,US0378331005,210.00,USD\n"+
		"2024-06-14,AAPL,US0378331005,212.50,USD\n")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	inst := model.Instrument{Symbol: "AAPL", ISIN: "US0378331005"}

	// Exact date.
	got, err := s.PriceOn(inst, day(2024, 6, 14))
	if err != nil || got.Amount.FloatString(2) != "212.50" {
		t.Fatalf("exact: got %v err %v", got.Amount, err)
	}
	// Weekend → preceding trading day (the 14th).
	got, _ = s.PriceOn(inst, day(2024, 6, 16))
	if got.Amount.FloatString(2) != "212.50" {
		t.Errorf("fallback price = %s, want 212.50", got.Amount.FloatString(2))
	}
	// Before any data → error.
	if _, err := s.PriceOn(inst, day(2024, 6, 1)); err == nil {
		t.Error("expected error before earliest price")
	}
	// Lookup by symbol when ISIN is absent on the query instrument.
	if _, err := s.PriceOn(model.Instrument{Symbol: "AAPL"}, day(2024, 6, 14)); err != nil {
		t.Errorf("symbol lookup failed: %v", err)
	}
	// Unknown instrument → error.
	if _, err := s.PriceOn(model.Instrument{Symbol: "ZZZZ"}, day(2024, 6, 14)); err == nil {
		t.Error("expected error for unknown instrument")
	}
}

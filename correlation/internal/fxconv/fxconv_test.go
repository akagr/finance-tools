package fxconv

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/series"
)

func date(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func writeFX(t *testing.T, content string) *Table {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fx.csv")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := LoadFX(p)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestConvertMultipliesByRate(t *testing.T) {
	tab := writeFX(t, `date,currency,rate
2024-01-01,USD,83.0
2024-01-03,USD,84.0
`)
	s := series.Series{Label: "VWRA", Currency: "USD", Points: []series.Point{
		{Date: date("2024-01-01"), Close: 100},
		{Date: date("2024-01-03"), Close: 110},
	}}
	got, err := Convert(s, "INR", tab)
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "INR" {
		t.Fatalf("currency = %s", got.Currency)
	}
	if math.Abs(got.Points[0].Close-8300) > 1e-9 || math.Abs(got.Points[1].Close-9240) > 1e-9 {
		t.Fatalf("converted = %v", got.Points)
	}
}

func TestConvertPrecedingDayFallback(t *testing.T) {
	tab := writeFX(t, `date,currency,rate
2024-01-01,USD,83.0
`)
	s := series.Series{Label: "X", Currency: "USD", Points: []series.Point{
		{Date: date("2024-01-05"), Close: 10}, // no rate on the 5th -> fall back to the 1st
	}}
	got, err := Convert(s, "INR", tab)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Points[0].Close-830) > 1e-9 {
		t.Fatalf("fallback conversion = %v, want 830", got.Points[0].Close)
	}
}

func TestConvertSameCurrencyNoop(t *testing.T) {
	s := series.Series{Label: "N", Currency: "INR", Points: []series.Point{{Date: date("2024-01-01"), Close: 5}}}
	got, err := Convert(s, "INR", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Points[0].Close != 5 {
		t.Fatal("same-currency series should be unchanged")
	}
}

func TestConvertMissingRateErrors(t *testing.T) {
	tab := writeFX(t, `date,currency,rate
2024-06-01,USD,83.0
`)
	s := series.Series{Label: "X", Currency: "USD", Points: []series.Point{
		{Date: date("2024-01-01"), Close: 10}, // before any rate -> error
	}}
	_, err := Convert(s, "INR", tab)
	if err == nil {
		t.Fatal("want error when no rate on or before the date")
	}
	// Error must be actionable: report actual coverage and a re-fetch hint.
	for _, want := range []string{"2024-06-01", "re-fetch", "fetch.py fx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestConvertNoRatesForCurrencyErrors(t *testing.T) {
	tab := writeFX(t, `date,currency,rate
2024-06-01,EUR,90.0
`)
	s := series.Series{Label: "X", Currency: "USD", Points: []series.Point{
		{Date: date("2024-06-02"), Close: 10}, // USD has no rates at all
	}}
	_, err := Convert(s, "INR", tab)
	if err == nil {
		t.Fatal("want error when currency has no rates")
	}
	if !strings.Contains(err.Error(), "no USD->INR rates at all") {
		t.Errorf("error %q should say there are no rates at all", err.Error())
	}
}

func TestConvertNilTableErrors(t *testing.T) {
	s := series.Series{Label: "X", Currency: "USD", Points: []series.Point{{Date: date("2024-01-01"), Close: 10}}}
	if _, err := Convert(s, "INR", nil); err == nil {
		t.Fatal("want error converting foreign currency with no FX table")
	}
}

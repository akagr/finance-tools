package perf

import (
	"math"
	"testing"

	"github.com/akagr/finance-tools/papertrade/internal/store"
)

func snap(date string, quote, equity float64) store.EquitySnapshot {
	return store.EquitySnapshot{Date: date, Quote: quote, Equity: equity}
}

func TestSummarizeNeedsTwo(t *testing.T) {
	if _, ok := Summarize(nil); ok {
		t.Error("empty should not summarise")
	}
	if _, ok := Summarize([]store.EquitySnapshot{snap("2024-01-01", 100, 100000)}); ok {
		t.Error("single snapshot should not summarise")
	}
}

func TestSummarizeReturnsAndBenchmark(t *testing.T) {
	snaps := []store.EquitySnapshot{
		snap("2024-01-01", 100, 100000),
		snap("2024-01-02", 110, 105000),
		snap("2024-01-03", 121, 110000),
	}
	su, ok := Summarize(snaps)
	if !ok {
		t.Fatal("expected a summary")
	}
	// Strategy equity 100000 -> 110000 = +10%.
	if math.Abs(su.TotalReturn-0.10) > 1e-9 {
		t.Errorf("total return = %v, want 0.10", su.TotalReturn)
	}
	// Buy-and-hold on quotes 100 -> 121 = +21%.
	if math.Abs(su.BenchReturn-0.21) > 1e-9 {
		t.Errorf("bench return = %v, want 0.21", su.BenchReturn)
	}
	if su.Snapshots != 3 || su.Start != "2024-01-01" || su.End != "2024-01-03" {
		t.Errorf("meta wrong: %+v", su)
	}
}

func TestSummarizeMaxDrawdown(t *testing.T) {
	snaps := []store.EquitySnapshot{
		snap("2024-01-01", 100, 100),
		snap("2024-01-02", 100, 120), // peak
		snap("2024-01-03", 100, 90),  // -25% from peak
		snap("2024-01-04", 100, 110),
	}
	su, _ := Summarize(snaps)
	if math.Abs(su.MaxDrawdown-0.25) > 1e-9 {
		t.Errorf("max drawdown = %v, want 0.25", su.MaxDrawdown)
	}
}

func TestSummarizeDedupsRepeatedDate(t *testing.T) {
	// A forced re-step writes a second snapshot for the same date; the last wins.
	snaps := []store.EquitySnapshot{
		snap("2024-01-01", 100, 100000),
		snap("2024-01-02", 110, 105000),
		snap("2024-01-02", 110, 106000), // repeat date, overrides
	}
	su, ok := Summarize(snaps)
	if !ok {
		t.Fatal("expected summary")
	}
	if su.Snapshots != 2 {
		t.Errorf("snapshots = %d, want 2 after dedup", su.Snapshots)
	}
	if math.Abs(su.EndEquity-106000) > 1e-9 {
		t.Errorf("end equity = %v, want 106000 (last wins)", su.EndEquity)
	}
}

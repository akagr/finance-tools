package pipeline

import (
	"path/filepath"
	"testing"
)

func baseOpts() Options {
	return Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		InitialCapital: 100000,
	}
}

// TestAllRunsEveryStrategyOnce checks that "all" produces one line per active
// strategy plus exactly one buy-hold benchmark, with no duplicates.
func TestAllRunsEveryStrategyOnce(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "all"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(rep.Lines), len(activeNames)+1; got != want {
		t.Fatalf("lines = %d, want %d (each active strategy + benchmark)", got, want)
	}
	seen := map[string]int{}
	bench := 0
	for _, l := range rep.Lines {
		seen[l.Strategy]++
		if l.Strategy == benchmarkName {
			bench++
		}
	}
	if bench != 1 {
		t.Errorf("benchmark appears %d times, want exactly 1", bench)
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("strategy %q appears %d times, want 1", name, n)
		}
	}
}

// TestLinesSortedByReturn checks the comparison table is ordered best-first.
func TestLinesSortedByReturn(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "all"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rep.Lines); i++ {
		if rep.Lines[i-1].TotalReturn < rep.Lines[i].TotalReturn {
			t.Errorf("lines not sorted by total return at %d: %v < %v",
				i, rep.Lines[i-1].TotalReturn, rep.Lines[i].TotalReturn)
		}
	}
}

// TestSingleStrategyHasTwoLines checks a normal run shows the chosen strategy
// plus the benchmark.
func TestSingleStrategyHasTwoLines(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "ema-cross"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Lines) != 2 {
		t.Fatalf("lines = %d, want 2 (strategy + benchmark)", len(rep.Lines))
	}
}

// TestBuyHoldAloneIsNotDuplicated checks that choosing buy-hold explicitly does
// not render the benchmark twice.
func TestBuyHoldAloneIsNotDuplicated(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "buy-hold"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Lines) != 1 {
		t.Fatalf("lines = %d, want 1 (benchmark only)", len(rep.Lines))
	}
}

func TestUnknownStrategyErrors(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "nope"
	if _, err := BuildReport(opts); err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestSortByDrawdownRanksLowestFirst(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "all"
	opts.SortBy = "drawdown"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	// drawdown is "lower is better", so the table must be ascending by Max DD.
	for i := 1; i < len(rep.Lines); i++ {
		if rep.Lines[i-1].MaxDrawdown > rep.Lines[i].MaxDrawdown {
			t.Errorf("not ascending by drawdown at %d: %v > %v",
				i, rep.Lines[i-1].MaxDrawdown, rep.Lines[i].MaxDrawdown)
		}
	}
}

func TestSortBySharpeRanksHighestFirst(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "all"
	opts.SortBy = "sharpe"
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rep.Lines); i++ {
		if rep.Lines[i-1].Sharpe < rep.Lines[i].Sharpe {
			t.Errorf("not descending by Sharpe at %d: %v < %v",
				i, rep.Lines[i-1].Sharpe, rep.Lines[i].Sharpe)
		}
	}
}

func TestUnknownSortKeyErrors(t *testing.T) {
	opts := baseOpts()
	opts.SortBy = "bogus"
	if _, err := BuildReport(opts); err == nil {
		t.Fatal("expected error for unknown --sort key")
	}
}

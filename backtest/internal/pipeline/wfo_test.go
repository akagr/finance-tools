package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/series"
)

func mustLoadFixture(t *testing.T) series.Series {
	t.Helper()
	all, err := series.Load(filepath.Join(fixtures, "prices.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("no series in fixture")
	}
	return all[0]
}

func defaultCfg() engine.Config {
	return engine.Config{InitialCapital: 100000, Costs: engine.DefaultCosts()}
}

func wfoOpts() Options {
	o := baseOpts()
	o.Strategy = "sma-cross"
	return o
}

func TestWalkForwardOptValidation(t *testing.T) {
	axes := []SweepAxis{{Name: "fast", Min: 3, Max: 6, Step: 3}, {Name: "slow", Min: 8, Max: 12, Step: 4}}

	if _, err := BuildWalkForwardOpt(wfoOpts(), axes, "sharpe", 1, false); err == nil {
		t.Error("expected error for < 2 folds")
	}
	if _, err := BuildWalkForwardOpt(sweepOpts("all"), axes, "sharpe", 3, false); err == nil {
		t.Error("expected error for --strategy all")
	}
	if _, err := BuildWalkForwardOpt(wfoOpts(), nil, "sharpe", 3, false); err == nil {
		t.Error("expected error for zero axes")
	}
	if _, err := BuildWalkForwardOpt(wfoOpts(), axes, "bogus", 3, false); err == nil {
		t.Error("expected error for unknown metric")
	}
}

func TestWalkForwardOptProducesOptimisedFolds(t *testing.T) {
	axes := []SweepAxis{{Name: "fast", Min: 3, Max: 6, Step: 3}, {Name: "slow", Min: 8, Max: 12, Step: 4}}
	wf, err := BuildWalkForwardOpt(wfoOpts(), axes, "sharpe", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if !wf.Meta.Optimised {
		t.Error("expected Meta.Optimised = true")
	}
	if wf.Meta.Metric != "sharpe" {
		t.Errorf("metric = %q, want sharpe", wf.Meta.Metric)
	}
	if len(wf.Folds) != 3 {
		t.Fatalf("folds = %d, want 3", len(wf.Folds))
	}
	for i, f := range wf.Folds {
		if f.Params == "" {
			t.Errorf("fold %d has no chosen params", i)
		}
		// Params must name both swept axes.
		if !strings.Contains(f.Params, "fast=") || !strings.Contains(f.Params, "slow=") {
			t.Errorf("fold %d params %q missing an axis", i, f.Params)
		}
		if f.Beat != (f.StratReturn > f.BenchReturn) {
			t.Errorf("fold %d Beat inconsistent", i)
		}
	}
}

func TestWalkForwardOptRollingSetsMeta(t *testing.T) {
	axes := []SweepAxis{{Name: "fast", Min: 3, Max: 6, Step: 3}, {Name: "slow", Min: 8, Max: 12, Step: 4}}
	anchored, err := BuildWalkForwardOpt(wfoOpts(), axes, "sharpe", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if anchored.Meta.Rolling {
		t.Error("anchored run should have Rolling=false")
	}
	rolling, err := BuildWalkForwardOpt(wfoOpts(), axes, "sharpe", 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if !rolling.Meta.Rolling {
		t.Error("rolling run should have Rolling=true")
	}
	if len(rolling.Folds) != 3 {
		t.Fatalf("rolling folds = %d, want 3", len(rolling.Folds))
	}
}

func TestWalkForwardOptTooFewBars(t *testing.T) {
	axes := []SweepAxis{{Name: "lookback", Min: 3, Max: 6, Step: 3}}
	opts := baseOpts()
	opts.Strategy = "momentum"
	// 120-bar fixture, 100 folds needs >= 2*(101) bars.
	if _, err := BuildWalkForwardOpt(opts, axes, "sharpe", 100, false); err == nil {
		t.Error("expected error when bars < 2*(folds+1)")
	}
}

func TestOptimiseOnSeriesPicksBest(t *testing.T) {
	// Build a small grid and confirm the returned coords score at least as well
	// as any other valid combo on the same series.
	opts := wfoOpts()
	axes := []SweepAxis{{Name: "fast", Min: 3, Max: 9, Step: 3}, {Name: "slow", Min: 10, Max: 20, Step: 5}}
	s := mustLoadFixture(t)
	cfg := defaultCfg()
	combos := combinations([][]float64{{3, 6, 9}, {10, 15, 20}})
	getMetric := statMetrics["sharpe"]

	best, found, err := optimiseOnSeries(s, opts, axes, combos, cfg, getMetric, false)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a best combination")
	}
	bestStats, valid, err := runCombo(s, opts, axes, best, cfg)
	if err != nil || !valid {
		t.Fatalf("best combo invalid: err=%v valid=%v", err, valid)
	}
	for _, c := range combos {
		st, valid, err := runCombo(s, opts, axes, c, cfg)
		if err != nil || !valid {
			continue
		}
		if getMetric(st) > getMetric(bestStats)+1e-9 {
			t.Errorf("combo %v scores higher than reported best %v", c, best)
		}
	}
}

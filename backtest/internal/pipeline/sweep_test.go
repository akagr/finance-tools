package pipeline

import (
	"testing"
)

func sweepOpts(strategy string) Options {
	o := baseOpts()
	o.Strategy = strategy
	return o
}

func TestAxisTicks(t *testing.T) {
	ticks, err := axisTicks(SweepAxis{Name: "fast", Min: 10, Max: 50, Step: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{10, 20, 30, 40, 50}
	if len(ticks) != len(want) {
		t.Fatalf("ticks = %v, want %v", ticks, want)
	}
	for i := range want {
		if ticks[i] != want[i] {
			t.Errorf("tick %d = %v, want %v", i, ticks[i], want[i])
		}
	}
}

func TestAxisTicksValidation(t *testing.T) {
	if _, err := axisTicks(SweepAxis{Name: "x", Min: 1, Max: 10, Step: 0}); err == nil {
		t.Error("expected error for zero step")
	}
	if _, err := axisTicks(SweepAxis{Name: "x", Min: 10, Max: 1, Step: 1}); err == nil {
		t.Error("expected error for max < min")
	}
}

func TestCombinationsCartesian(t *testing.T) {
	got := combinations([][]float64{{1, 2}, {3, 4}})
	want := [][]float64{{1, 3}, {1, 4}, {2, 3}, {2, 4}}
	if len(got) != len(want) {
		t.Fatalf("combos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i][0] != want[i][0] || got[i][1] != want[i][1] {
			t.Errorf("combo %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestBuildSweep1D(t *testing.T) {
	opts := sweepOpts("momentum")
	sw, err := BuildSweep(opts, []SweepAxis{{Name: "lookback", Min: 5, Max: 20, Step: 5}}, "cagr")
	if err != nil {
		t.Fatal(err)
	}
	if len(sw.AxisNames) != 1 || sw.AxisNames[0] != "lookback" {
		t.Errorf("axis names = %v", sw.AxisNames)
	}
	if len(sw.Points) != 4 { // 5,10,15,20
		t.Fatalf("points = %d, want 4", len(sw.Points))
	}
	for _, p := range sw.Points {
		if !p.Valid {
			t.Errorf("point %v unexpectedly invalid", p.Coords)
		}
	}
}

func TestBuildSweep2DMarksInvalidCombos(t *testing.T) {
	// A crossover grid where fast can meet/exceed slow: those cells must be
	// flagged invalid, not error the whole sweep.
	opts := sweepOpts("sma-cross")
	axes := []SweepAxis{{Name: "fast", Min: 5, Max: 15, Step: 5}, {Name: "slow", Min: 5, Max: 15, Step: 5}}
	sw, err := BuildSweep(opts, axes, "sharpe")
	if err != nil {
		t.Fatal(err)
	}
	invalid := 0
	for _, p := range sw.Points {
		if !p.Valid {
			invalid++
			// fast must be >= slow for an invalid crossover.
			if p.Coords[0] < p.Coords[1] {
				t.Errorf("point %v marked invalid but fast < slow", p.Coords)
			}
		}
	}
	if invalid == 0 {
		t.Error("expected some invalid (fast >= slow) combinations")
	}
}

func TestBuildSweepValidation(t *testing.T) {
	opts := sweepOpts("sma-cross")
	if _, err := BuildSweep(opts, nil, "sharpe"); err == nil {
		t.Error("expected error for zero axes")
	}
	three := []SweepAxis{{Name: "fast", Min: 1, Max: 2, Step: 1}, {Name: "slow", Min: 1, Max: 2, Step: 1}, {Name: "x", Min: 1, Max: 2, Step: 1}}
	if _, err := BuildSweep(opts, three, "sharpe"); err == nil {
		t.Error("expected error for > 2 axes")
	}
	if _, err := BuildSweep(sweepOpts("all"), []SweepAxis{{Name: "fast", Min: 1, Max: 2, Step: 1}}, "sharpe"); err == nil {
		t.Error("expected error for --strategy all")
	}
	if _, err := BuildSweep(opts, []SweepAxis{{Name: "fast", Min: 1, Max: 2, Step: 1}}, "bogus"); err == nil {
		t.Error("expected error for unknown metric")
	}
	if _, err := BuildSweep(opts, []SweepAxis{{Name: "nope", Min: 1, Max: 2, Step: 1}}, "sharpe"); err == nil {
		t.Error("expected error for unknown parameter name")
	}
}

func TestBuildSweepBestIsExtreme(t *testing.T) {
	opts := sweepOpts("momentum")
	sw, err := BuildSweep(opts, []SweepAxis{{Name: "lookback", Min: 5, Max: 25, Step: 5}}, "sharpe")
	if err != nil {
		t.Fatal(err)
	}
	// Best (higher-is-better for sharpe) must be >= every valid point's metric.
	for _, p := range sw.Points {
		if p.Valid && p.Metric > sw.Best+1e-9 {
			t.Errorf("point metric %v exceeds reported best %v", p.Metric, sw.Best)
		}
	}
}

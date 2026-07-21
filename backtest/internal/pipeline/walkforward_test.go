package pipeline

import (
	"strings"
	"testing"
)

func TestFoldBoundsPartition(t *testing.T) {
	bounds := foldBounds(100, 4)
	if len(bounds) != 5 {
		t.Fatalf("bounds len = %d, want 5", len(bounds))
	}
	if bounds[0] != 0 {
		t.Errorf("first bound = %d, want 0", bounds[0])
	}
	if bounds[len(bounds)-1] != 99 {
		t.Errorf("last bound = %d, want 99 (n-1)", bounds[len(bounds)-1])
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i] <= bounds[i-1] {
			t.Errorf("bounds not strictly increasing at %d: %v", i, bounds)
		}
	}
}

func TestWalkForwardRejectsBadInputs(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "sma-cross"
	if _, err := BuildWalkForward(opts, 1); err == nil {
		t.Error("expected error for < 2 folds")
	}

	all := baseOpts()
	all.Strategy = "all"
	if _, err := BuildWalkForward(all, 4); err == nil {
		t.Error("expected error for --strategy all")
	}

	bh := baseOpts()
	bh.Strategy = "buy-hold"
	if _, err := BuildWalkForward(bh, 4); err == nil {
		t.Error("expected error for buy-hold (no benchmark to beat)")
	}
}

func TestWalkForwardProducesOneRowPerFold(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "sma-cross"
	opts.Fast = 3
	opts.Slow = 8
	wf, err := BuildWalkForward(opts, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Folds) != 4 {
		t.Fatalf("folds = %d, want 4", len(wf.Folds))
	}
	// Folds must be chronological and contiguous (each starts where the last ended).
	for i, f := range wf.Folds {
		if f.Index != i+1 {
			t.Errorf("fold %d has index %d", i, f.Index)
		}
		if i > 0 && wf.Folds[i-1].End != f.Start {
			t.Errorf("fold %d start %s != previous end %s", i, f.Start, wf.Folds[i-1].End)
		}
		if f.Beat != (f.StratReturn > f.BenchReturn) {
			t.Errorf("fold %d Beat flag inconsistent with returns", i)
		}
	}
}

func TestWalkForwardTooFewBars(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "sma-cross"
	// The synthetic fixture has 120 bars; asking for 100 folds needs >= 200.
	if _, err := BuildWalkForward(opts, 100); err == nil {
		t.Error("expected error when bars < 2*folds")
	}
}

func TestWalkForwardNoteMentionsFolds(t *testing.T) {
	opts := baseOpts()
	opts.Strategy = "sma-cross"
	opts.Fast = 3
	opts.Slow = 8
	wf, err := BuildWalkForward(opts, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Meta.Notes) == 0 || !strings.Contains(wf.Meta.Notes[0], "of 3") {
		t.Errorf("expected a summary note mentioning 3 folds, got %v", wf.Meta.Notes)
	}
}

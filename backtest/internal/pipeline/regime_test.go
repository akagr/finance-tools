package pipeline

import (
	"math"
	"testing"
)

func regimeOpts() Options {
	o := baseOpts()
	o.Strategy = "sma-cross"
	o.Fast = 3
	o.Slow = 8
	return o
}

func TestRegimeValidation(t *testing.T) {
	if _, err := BuildRegime(regimeOpts(), 1, 20); err == nil {
		t.Error("expected error for trend window < 2")
	}
	if _, err := BuildRegime(regimeOpts(), 20, 1); err == nil {
		t.Error("expected error for vol window < 2")
	}
	all := baseOpts()
	all.Strategy = "all"
	if _, err := BuildRegime(all, 20, 10); err == nil {
		t.Error("expected error for --strategy all")
	}
	// Windows larger than the 120-bar fixture.
	if _, err := BuildRegime(regimeOpts(), 500, 10); err == nil {
		t.Error("expected error when trend window exceeds bars")
	}
}

func TestRegimeProducesFourBuckets(t *testing.T) {
	rg, err := BuildRegime(regimeOpts(), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rg.Buckets) != 4 {
		t.Fatalf("buckets = %d, want 4", len(rg.Buckets))
	}
	names := map[string]bool{}
	for _, b := range rg.Buckets {
		names[b.Name] = true
	}
	for _, want := range []string{"Bull", "Bear", "High vol", "Low vol"} {
		if !names[want] {
			t.Errorf("missing bucket %q", want)
		}
	}
}

func TestRegimeVolBucketsPartitionDays(t *testing.T) {
	rg, err := BuildRegime(regimeOpts(), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	var high, low int
	for _, b := range rg.Buckets {
		switch b.Name {
		case "High vol":
			high = b.Days
		case "Low vol":
			low = b.Days
		}
	}
	// The median split should divide the classified vol days roughly evenly.
	if high == 0 || low == 0 {
		t.Fatalf("degenerate vol split: high=%d low=%d", high, low)
	}
	if diff := math.Abs(float64(high - low)); diff > float64(high+low)/2 {
		t.Errorf("vol split very unbalanced: high=%d low=%d", high, low)
	}
}

func TestCompoundAndMedianHelpers(t *testing.T) {
	if got := compound([]float64{0.1, 0.1}); math.Abs(got-0.21) > 1e-9 {
		t.Errorf("compound = %v, want 0.21", got)
	}
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median(odd) = %v, want 2", got)
	}
	if got := median([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("median(even) = %v, want 2.5", got)
	}
}

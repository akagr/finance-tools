package strategy

import (
	"math"
	"testing"
)

func TestNewVolTargetValidation(t *testing.T) {
	inner := BuyHold{}
	if _, err := NewVolTarget(inner, 0.10, 20); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
	if _, err := NewVolTarget(nil, 0.10, 20); err == nil {
		t.Error("expected error for nil inner")
	}
	if _, err := NewVolTarget(inner, 0, 20); err == nil {
		t.Error("expected error for non-positive target")
	}
	if _, err := NewVolTarget(inner, 0.10, 1); err == nil {
		t.Error("expected error for lookback < 2")
	}
}

// constWeight is a stub inner strategy returning a fixed weight, so the overlay
// can be tested in isolation from any real signal.
type constWeight float64

func (c constWeight) Name() string               { return "const" }
func (c constWeight) Target(_ []float64) float64 { return float64(c) }

func TestVolTargetTrimsHighVol(t *testing.T) {
	v, err := NewVolTarget(constWeight(1.0), 0.10, 5) // target 10% annualised
	if err != nil {
		t.Fatal(err)
	}
	// A highly volatile series (large daily swings) should push realised vol
	// well above 10%, so the position is trimmed below the inner weight of 1.0.
	volatile := []float64{100, 120, 90, 130, 85, 125}
	w := v.Target(volatile)
	if w <= 0 || w >= 1.0 {
		t.Errorf("high-vol weight = %v, want in (0, 1) (trimmed)", w)
	}
}

func TestVolTargetHoldsWhenCalm(t *testing.T) {
	v, _ := NewVolTarget(constWeight(1.0), 0.50, 5) // generous 50% target
	// A gently rising series has low realised vol, well under 50%, so the scale
	// caps at 1 and the inner weight passes through unchanged (no leverage).
	calm := []float64{100, 100.1, 100.2, 100.3, 100.4, 100.5}
	if w := v.Target(calm); math.Abs(w-1.0) > 1e-9 {
		t.Errorf("calm weight = %v, want 1.0 (no leverage up)", w)
	}
}

func TestVolTargetPassesThroughFlatSignal(t *testing.T) {
	// When the inner strategy is out of the market, the overlay leaves it out.
	v, _ := NewVolTarget(constWeight(0.0), 0.10, 5)
	volatile := []float64{100, 120, 90, 130, 85, 125}
	if w := v.Target(volatile); w != 0 {
		t.Errorf("flat inner weight = %v, want 0", w)
	}
}

func TestVolTargetInsufficientHistory(t *testing.T) {
	// With fewer than lookback+1 closes, realised vol is undefined and the inner
	// weight passes through unchanged.
	v, _ := NewVolTarget(constWeight(1.0), 0.10, 5)
	if w := v.Target([]float64{100, 101, 102}); w != 1.0 {
		t.Errorf("insufficient history weight = %v, want 1.0 (unchanged)", w)
	}
}

func TestVolTargetNameIncludesInner(t *testing.T) {
	sma, _ := NewSMACross(20, 50)
	v, _ := NewVolTarget(sma, 0.10, 20)
	want := "sma-cross(20/50)+voltgt(10%/20)"
	if v.Name() != want {
		t.Errorf("Name = %q, want %q", v.Name(), want)
	}
}

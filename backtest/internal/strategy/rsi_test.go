package strategy

import (
	"math"
	"testing"
)

func TestNewRSIValidation(t *testing.T) {
	if _, err := NewRSI(14, 30); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
	for _, tt := range []struct {
		period int
		thr    float64
	}{{0, 30}, {14, 0}, {14, 100}, {14, -1}} {
		if _, err := NewRSI(tt.period, tt.thr); err == nil {
			t.Errorf("NewRSI(%d,%v): expected error", tt.period, tt.thr)
		}
	}
}

func TestRSIHelperExtremes(t *testing.T) {
	// A monotonically rising series has only gains → RSI 100.
	if got := rsi([]float64{1, 2, 3, 4, 5}, 4); got != 100 {
		t.Errorf("rising RSI = %v, want 100", got)
	}
	// A monotonically falling series has only losses → RSI 0.
	if got := rsi([]float64{5, 4, 3, 2, 1}, 4); got != 0 {
		t.Errorf("falling RSI = %v, want 0", got)
	}
	// A symmetric zig-zag sits near the midpoint.
	if got := rsi([]float64{10, 11, 10, 11, 10, 11}, 4); math.Abs(got-50) > 1e-9 {
		t.Errorf("balanced RSI = %v, want ~50", got)
	}
}

func TestRSITargetBuysOversold(t *testing.T) {
	s, err := NewRSI(4, 30)
	if err != nil {
		t.Fatal(err)
	}
	// Falling series → RSI 0 < 30 → buy the dip.
	if got := s.Target([]float64{5, 4, 3, 2, 1}); got != 1.0 {
		t.Errorf("oversold: got %v, want 1.0", got)
	}
	// Rising series → RSI 100 → step aside.
	if got := s.Target([]float64{1, 2, 3, 4, 5}); got != 0.0 {
		t.Errorf("overbought: got %v, want 0.0", got)
	}
	// Not enough history: flat.
	if got := s.Target([]float64{1, 2, 3}); got != 0.0 {
		t.Errorf("insufficient history: got %v, want 0.0", got)
	}
}

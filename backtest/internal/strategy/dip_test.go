package strategy

import "testing"

func TestNewDipValidation(t *testing.T) {
	if _, err := NewDip(1); err != nil {
		t.Errorf("valid param rejected: %v", err)
	}
	for _, drop := range []float64{0, -1} {
		if _, err := NewDip(drop); err == nil {
			t.Errorf("NewDip(%v): expected error", drop)
		}
	}
}

func TestDipTargetBuysBelowPeak(t *testing.T) {
	s, err := NewDip(1)
	if err != nil {
		t.Fatal(err)
	}
	// Peak is 100; last close 98 is 2% below → buy.
	if got := s.Target([]float64{90, 100, 98}); got != 1.0 {
		t.Errorf("2%% below peak: got %v, want 1.0", got)
	}
	// Peak is 100; last close 99.5 is only 0.5% below → stay in cash.
	if got := s.Target([]float64{90, 100, 99.5}); got != 0.0 {
		t.Errorf("0.5%% below peak: got %v, want 0.0", got)
	}
	// New all-time high → cash.
	if got := s.Target([]float64{90, 100, 101}); got != 0.0 {
		t.Errorf("fresh high: got %v, want 0.0", got)
	}
	// Exactly 1% below peak → buy (boundary is inclusive).
	if got := s.Target([]float64{100, 99}); got != 1.0 {
		t.Errorf("exactly 1%% below peak: got %v, want 1.0", got)
	}
	// Empty history: flat.
	if got := s.Target(nil); got != 0.0 {
		t.Errorf("empty history: got %v, want 0.0", got)
	}
}

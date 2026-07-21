package strategy

import "testing"

func TestNewMomentumValidation(t *testing.T) {
	if _, err := NewMomentum(10); err != nil {
		t.Errorf("valid lookback rejected: %v", err)
	}
	for _, lb := range []int{0, -5} {
		if _, err := NewMomentum(lb); err == nil {
			t.Errorf("NewMomentum(%d): expected error", lb)
		}
	}
}

func TestMomentumTarget(t *testing.T) {
	s, err := NewMomentum(3)
	if err != nil {
		t.Fatal(err)
	}
	// Fewer than lookback+1 bars: flat.
	if got := s.Target([]float64{10, 11, 12}); got != 0 {
		t.Errorf("insufficient history: got %v, want 0", got)
	}
	// Last close above the close 3 bars ago → invested.
	if got := s.Target([]float64{10, 11, 12, 13}); got != 1.0 {
		t.Errorf("up over lookback: got %v, want 1.0", got)
	}
	// Last close below the close 3 bars ago → flat.
	if got := s.Target([]float64{13, 12, 11, 10}); got != 0.0 {
		t.Errorf("down over lookback: got %v, want 0.0", got)
	}
	// Only the endpoints matter, not the path between them.
	if got := s.Target([]float64{10, 50, 1, 11}); got != 1.0 {
		t.Errorf("endpoint 11>10: got %v, want 1.0", got)
	}
}

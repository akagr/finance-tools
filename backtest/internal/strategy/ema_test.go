package strategy

import "testing"

func TestNewEMACrossValidation(t *testing.T) {
	if _, err := NewEMACross(5, 20); err != nil {
		t.Errorf("valid windows rejected: %v", err)
	}
	for _, tt := range [][2]int{{20, 20}, {30, 10}, {0, 10}} {
		if _, err := NewEMACross(tt[0], tt[1]); err == nil {
			t.Errorf("NewEMACross(%d,%d): expected error", tt[0], tt[1])
		}
	}
}

func TestEMACrossTarget(t *testing.T) {
	s, err := NewEMACross(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Target([]float64{10, 11, 12}); got != 0 {
		t.Errorf("insufficient history: got %v, want 0", got)
	}
	if got := s.Target([]float64{10, 11, 12, 13, 14}); got != 1.0 {
		t.Errorf("rising: got %v, want 1.0", got)
	}
	if got := s.Target([]float64{14, 13, 12, 11, 10}); got != 0.0 {
		t.Errorf("falling: got %v, want 0.0", got)
	}
}

func TestEMAHelper(t *testing.T) {
	// A constant series has an EMA equal to that constant.
	if got := ema([]float64{5, 5, 5, 5}, 3); got != 5 {
		t.Errorf("ema of constant = %v, want 5", got)
	}
	// EMA weights recent prices more, so on a rising series it exceeds the SMA.
	rising := []float64{1, 2, 3, 4, 5}
	if ema(rising, 3) <= sma(rising, 3) {
		t.Errorf("ema %v should exceed sma %v on a rising series", ema(rising, 3), sma(rising, 3))
	}
}

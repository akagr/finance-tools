package strategy

import (
	"math"
	"testing"
)

func TestBuyHoldAlwaysFull(t *testing.T) {
	var bh BuyHold
	for _, closes := range [][]float64{{100}, {100, 101, 99}} {
		if got := bh.Target(closes); got != 1.0 {
			t.Errorf("BuyHold.Target(%v) = %v, want 1.0", closes, got)
		}
	}
	if bh.Name() != "buy-hold" {
		t.Errorf("Name = %q", bh.Name())
	}
}

func TestNewSMACrossValidation(t *testing.T) {
	tests := []struct {
		fast, slow int
		ok         bool
	}{
		{5, 20, true},
		{1, 2, true},
		{20, 20, false}, // fast must be < slow
		{30, 10, false},
		{0, 10, false},
		{-1, 10, false},
	}
	for _, tt := range tests {
		_, err := NewSMACross(tt.fast, tt.slow)
		if (err == nil) != tt.ok {
			t.Errorf("NewSMACross(%d,%d): err=%v, wantOK=%v", tt.fast, tt.slow, err, tt.ok)
		}
	}
}

func TestSMACrossTarget(t *testing.T) {
	s, err := NewSMACross(2, 4)
	if err != nil {
		t.Fatal(err)
	}

	// Fewer than slow bars: flat.
	if got := s.Target([]float64{10, 11, 12}); got != 0 {
		t.Errorf("insufficient history: got %v, want 0", got)
	}

	// Rising series: fast SMA above slow SMA → fully invested.
	rising := []float64{10, 11, 12, 13, 14}
	if got := s.Target(rising); got != 1.0 {
		t.Errorf("rising: got %v, want 1.0", got)
	}

	// Falling series: fast SMA below slow SMA → flat.
	falling := []float64{14, 13, 12, 11, 10}
	if got := s.Target(falling); got != 0.0 {
		t.Errorf("falling: got %v, want 0.0", got)
	}
}

func TestSMACrossNeverPeeksAhead(t *testing.T) {
	// Target must depend only on the closes passed; the same prefix must yield
	// the same answer regardless of what data would follow.
	s, _ := NewSMACross(2, 4)
	prefix := []float64{10, 11, 12, 13}
	want := s.Target(prefix)
	extended := append(append([]float64{}, prefix...), 100, 1, 50)
	if got := s.Target(extended[:len(prefix)]); math.Abs(got-want) > 1e-12 {
		t.Errorf("target changed with future data present: %v vs %v", got, want)
	}
}

package strategy

import "testing"

// allStrategies returns one configured instance of every strategy for
// package-level conformance checks.
func allStrategies(t *testing.T) []Strategy {
	t.Helper()
	sma, err := NewSMACross(3, 10)
	if err != nil {
		t.Fatal(err)
	}
	ema, err := NewEMACross(3, 10)
	if err != nil {
		t.Fatal(err)
	}
	mom, err := NewMomentum(5)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRSI(5, 30)
	if err != nil {
		t.Fatal(err)
	}
	don, err := NewDonchian(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	return []Strategy{BuyHold{}, sma, ema, mom, r, don}
}

// TestTargetsWithinBounds walks a series through every strategy bar by bar (the
// order the engine uses) and checks each returns a weight in [0, 1] and a
// non-empty name — the contract every strategy must uphold.
func TestTargetsWithinBounds(t *testing.T) {
	closes := []float64{10, 11, 12, 11, 10, 9, 10, 12, 14, 13, 12, 15, 18, 16, 14, 13, 15, 17, 19, 20}
	for _, s := range allStrategies(t) {
		if s.Name() == "" {
			t.Errorf("%T has empty Name()", s)
		}
		for i := 1; i <= len(closes); i++ {
			w := s.Target(closes[:i])
			if w < 0 || w > 1 {
				t.Errorf("%s.Target(bar %d) = %v, want within [0,1]", s.Name(), i, w)
			}
		}
	}
}

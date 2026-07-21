package strategy

import "testing"

func TestNewDonchianValidation(t *testing.T) {
	if _, err := NewDonchian(20, 10); err != nil {
		t.Errorf("valid windows rejected: %v", err)
	}
	for _, tt := range [][2]int{{0, 10}, {20, 0}, {-1, -1}} {
		if _, err := NewDonchian(tt[0], tt[1]); err == nil {
			t.Errorf("NewDonchian(%d,%d): expected error", tt[0], tt[1])
		}
	}
}

// TestDonchianEntersAndExits feeds a series bar by bar (as the engine does) and
// checks the position enters on a breakout above the prior high and exits on a
// breakdown below the prior low, holding in between.
func TestDonchianEntersAndExits(t *testing.T) {
	d, err := NewDonchian(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	prices := []float64{10, 10, 10, 11, 9, 8}
	want := []float64{0, 0, 0, 1, 0, 0} // enters at bar 3 (breakout), exits at bar 4 (breakdown)
	for i := range prices {
		if got := d.Target(prices[:i+1]); got != want[i] {
			t.Errorf("bar %d: pos = %v, want %v", i, got, want[i])
		}
	}
}

// TestDonchianHoldsBetweenSignals verifies the position persists across bars
// that trigger neither a breakout nor a breakdown.
func TestDonchianHoldsBetweenSignals(t *testing.T) {
	d, _ := NewDonchian(3, 3)
	// Rise to a breakout, then drift up gently (new highs but no new lows): stays long.
	prices := []float64{10, 10, 10, 12, 13, 14, 15}
	var last float64
	for i := range prices {
		last = d.Target(prices[:i+1])
	}
	if last != 1.0 {
		t.Errorf("expected to remain long through an uptrend, got %v", last)
	}
}

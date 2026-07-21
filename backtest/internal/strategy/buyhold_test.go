package strategy

import "testing"

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

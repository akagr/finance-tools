package broker

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBuySlippageRaisesPrice(t *testing.T) {
	b := New(Costs{SlippageBps: 10}) // 0.1%
	f := b.Execute(Buy, 10, 100)
	if !approx(f.Price, 100.1) {
		t.Errorf("buy fill price = %v, want 100.1", f.Price)
	}
	if f.Side != Buy || f.Shares != 10 {
		t.Errorf("unexpected fill %+v", f)
	}
}

func TestSellSlippageLowersPrice(t *testing.T) {
	b := New(Costs{SlippageBps: 10})
	f := b.Execute(Sell, 10, 100)
	if !approx(f.Price, 99.9) {
		t.Errorf("sell fill price = %v, want 99.9", f.Price)
	}
}

func TestFeesChargedOnNotional(t *testing.T) {
	b := New(Costs{STTBps: 10, BrokerageBps: 5}) // 15 bps total, no slippage
	f := b.Execute(Buy, 10, 100)
	// notional 1000, fee 15 bps = 1.5
	if !approx(f.Cost, 1.5) {
		t.Errorf("cost = %v, want 1.5", f.Cost)
	}
	if !approx(f.Notional, 1000) {
		t.Errorf("notional = %v, want 1000", f.Notional)
	}
}

func TestZeroCostsUsesDefault(t *testing.T) {
	b := New(Costs{})
	if b.Costs != DefaultCosts() {
		t.Errorf("zero costs = %+v, want defaults %+v", b.Costs, DefaultCosts())
	}
}

package portfolio

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestBuyThenSellRealisesPnL(t *testing.T) {
	p := New(100000)
	// Buy 10 shares at 100, no cost.
	p.Buy(10, 100, 0)
	if !approx(p.Cash, 99000) || p.Shares != 10 {
		t.Fatalf("after buy: cash=%v shares=%v", p.Cash, p.Shares)
	}
	if !approx(p.AvgCost(), 100) {
		t.Errorf("avg cost = %v, want 100", p.AvgCost())
	}
	// Price rises to 120; unrealised = 10*(120-100) = 200.
	if !approx(p.Unrealised(120), 200) {
		t.Errorf("unrealised = %v, want 200", p.Unrealised(120))
	}
	// Sell all 10 at 120, no cost → realise 200.
	p.Sell(10, 120, 0)
	if p.Shares != 0 || !approx(p.Realised, 200) {
		t.Errorf("after sell: shares=%v realised=%v (want 0, 200)", p.Shares, p.Realised)
	}
	if !approx(p.Cash, 100200) {
		t.Errorf("cash = %v, want 100200", p.Cash)
	}
	if p.CostBasis != 0 {
		t.Errorf("cost basis = %v, want 0 when flat", p.CostBasis)
	}
}

func TestPartialSellSplitsBasis(t *testing.T) {
	p := New(0)
	p.Buy(10, 100, 0) // basis 1000
	p.Sell(4, 150, 0) // realise 4*(150-100)=200; basis -> 600
	if !approx(p.CostBasis, 600) {
		t.Errorf("basis = %v, want 600", p.CostBasis)
	}
	if !approx(p.Realised, 200) {
		t.Errorf("realised = %v, want 200", p.Realised)
	}
	if p.Shares != 6 {
		t.Errorf("shares = %v, want 6", p.Shares)
	}
}

func TestEquityAndWeight(t *testing.T) {
	p := New(5000)
	p.Buy(50, 100, 0) // spends 5000 → cash 0, 50 shares
	if !approx(p.Equity(100), 5000) {
		t.Errorf("equity = %v, want 5000", p.Equity(100))
	}
	if !approx(p.Weight(100), 1.0) {
		t.Errorf("weight = %v, want 1.0", p.Weight(100))
	}
	if !approx(p.Equity(120), 6000) {
		t.Errorf("equity at 120 = %v, want 6000", p.Equity(120))
	}
}

func TestCostsReduceCash(t *testing.T) {
	p := New(100000)
	p.Buy(10, 100, 15) // cost 15 charged
	if !approx(p.Cash, 100000-1000-15) {
		t.Errorf("cash = %v, want %v", p.Cash, 100000-1000-15)
	}
	if !approx(p.CostBasis, 1000) {
		t.Errorf("basis = %v, want 1000 (cost excluded from basis)", p.CostBasis)
	}
}

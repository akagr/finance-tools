package engine

import (
	"math"
	"testing"
	"time"

	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

func mkSeries(closes ...float64) series.Series {
	s := series.Series{Label: "T"}
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range closes {
		s.Points = append(s.Points, series.Point{Date: d, Close: c})
		d = d.AddDate(0, 0, 1)
	}
	return s
}

func TestRunRejectsTooFewBars(t *testing.T) {
	if _, err := Run(mkSeries(100), strategy.BuyHold{}, Config{InitialCapital: 1000, Costs: DefaultCosts()}); err == nil {
		t.Fatal("expected error for <2 bars")
	}
}

func TestRunRejectsNonPositiveCapital(t *testing.T) {
	if _, err := Run(mkSeries(100, 101), strategy.BuyHold{}, Config{InitialCapital: 0}); err == nil {
		t.Fatal("expected error for zero capital")
	}
}

func TestBuyHoldTradesOnce(t *testing.T) {
	s := mkSeries(100, 101, 102, 103, 104, 105)
	res, err := Run(s, strategy.BuyHold{}, Config{InitialCapital: 100000, Costs: DefaultCosts()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trades != 1 {
		t.Errorf("buy-hold trades = %d, want 1 (should buy once and hold)", res.Trades)
	}
	// Weight stays ~100% after the initial buy.
	for i := 1; i < len(res.Weights); i++ {
		if math.Abs(res.Weights[i]-1.0) > 1e-3 {
			t.Errorf("weight[%d] = %v, want ~1.0", i, res.Weights[i])
		}
	}
}

func TestBuyHoldZeroCostTracksPrice(t *testing.T) {
	// With no costs, buy-and-hold return must equal the price return exactly.
	s := mkSeries(100, 110, 121) // +10% then +10%
	res, err := Run(s, strategy.BuyHold{}, Config{
		InitialCapital: 100000,
		Costs:          Costs{}, // zero friction
	})
	if err != nil {
		t.Fatal(err)
	}
	gotReturn := res.Equity[len(res.Equity)-1]/res.Equity[0] - 1
	wantReturn := 121.0/100.0 - 1
	if math.Abs(gotReturn-wantReturn) > 1e-9 {
		t.Errorf("zero-cost buy-hold return = %v, want %v", gotReturn, wantReturn)
	}
}

func TestCostsReduceReturn(t *testing.T) {
	s := mkSeries(100, 110, 121)
	free, _ := Run(s, strategy.BuyHold{}, Config{InitialCapital: 100000, Costs: Costs{}})
	costly, _ := Run(s, strategy.BuyHold{}, Config{InitialCapital: 100000, Costs: DefaultCosts()})
	if costly.Equity[2] >= free.Equity[2] {
		t.Errorf("costs did not reduce final equity: costly=%v free=%v", costly.Equity[2], free.Equity[2])
	}
	if costly.TotalCost <= 0 {
		t.Errorf("expected positive total cost, got %v", costly.TotalCost)
	}
}

func TestFlatStrategyNeverInvests(t *testing.T) {
	// A slow window longer than the series keeps SMA-cross flat throughout, so
	// equity must stay exactly at initial capital (no trades, no cost).
	s := mkSeries(100, 90, 80, 120, 130)
	strat, _ := strategy.NewSMACross(2, 100)
	res, err := Run(s, strat, Config{InitialCapital: 50000, Costs: DefaultCosts()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trades != 0 {
		t.Errorf("trades = %d, want 0", res.Trades)
	}
	for i, e := range res.Equity {
		if math.Abs(e-50000) > 1e-9 {
			t.Errorf("equity[%d] = %v, want 50000", i, e)
		}
	}
}

// fixedWeight always targets the same fractional weight, to exercise the
// rebalance band (a fractional target drifts as prices move and would otherwise
// trade every bar).
type fixedWeight float64

func (fixedWeight) Name() string               { return "fixed" }
func (w fixedWeight) Target([]float64) float64 { return float64(w) }

func TestRebalanceBandCutsFractionalChurn(t *testing.T) {
	// A volatile price path with a constant 50% target: a wider band must trade
	// strictly less often than a tight one, since it tolerates more drift before
	// rebalancing.
	s := mkSeries(100, 106, 95, 108, 92, 110, 97, 103, 96, 104, 99, 107, 93, 109)
	tight, err := Run(s, fixedWeight(0.5), Config{InitialCapital: 100000, Costs: DefaultCosts(), RebalanceBand: 0.001})
	if err != nil {
		t.Fatal(err)
	}
	wide, err := Run(s, fixedWeight(0.5), Config{InitialCapital: 100000, Costs: DefaultCosts(), RebalanceBand: 0.10})
	if err != nil {
		t.Fatal(err)
	}
	if wide.Trades >= tight.Trades {
		t.Errorf("wide band trades (%d) should be fewer than tight band (%d)", wide.Trades, tight.Trades)
	}
}

func TestZeroBandUsesDefault(t *testing.T) {
	// A zero RebalanceBand must fall back to the default, not to "trade on every
	// infinitesimal drift" — so a 50% target over a volatile path still trades a
	// bounded number of times, not once per bar.
	s := mkSeries(100, 106, 95, 108, 92, 110, 97, 103, 96, 104)
	res, err := Run(s, fixedWeight(0.5), Config{InitialCapital: 100000, Costs: DefaultCosts()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trades == 0 {
		t.Error("expected some rebalancing for a fractional target")
	}
}

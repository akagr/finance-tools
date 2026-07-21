// Package engine runs a strategy over a daily close series and produces an
// equity curve. It is a deliberately simple close-to-close backtester:
//
//   - One asset, long/flat, fractional shares allowed.
//   - The signal for bar i is computed from closes[0..i] and executed at that
//     same close[i]. This "trade at the close you signalled on" convention is a
//     standard simplification; real fills happen later and at a different price,
//     so live results will differ. Slippage (below) is the crude stand-in.
//   - Costs are charged on every trade's notional: brokerage, STT and slippage,
//     each in basis points. Defaults approximate NSE cash-delivery friction; they
//     are configurable and intentionally conservative — underestimating costs is
//     how backtests lie.
//
// Money is float64, not big.Rat: this is a research tool like the sibling
// `correlation` module, where figures feed statistics rather than a tax return.
package engine

import (
	"fmt"
	"math"

	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

// Costs are per-trade frictions in basis points (1 bp = 0.01%), charged on the
// absolute notional of each trade.
type Costs struct {
	BrokerageBps float64 // broker fee per side
	STTBps       float64 // securities transaction tax per side
	SlippageBps  float64 // assumed adverse fill vs the signalled close
}

// DefaultCosts is a conservative NSE cash-delivery approximation: zero flat
// brokerage (many discount brokers charge nothing on delivery), 0.1% STT per
// side, and 5 bps of slippage. Exchange/GST/stamp charges are small and folded
// into slippage for simplicity.
func DefaultCosts() Costs {
	return Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5}
}

// perSideBps is the total cost charged on one trade's notional.
func (c Costs) perSideBps() float64 { return c.BrokerageBps + c.STTBps + c.SlippageBps }

// cost returns the money charged on a trade of the given absolute notional.
func (c Costs) cost(notional float64) float64 {
	return math.Abs(notional) * c.perSideBps() / 10000.0
}

// Config parameterises a backtest run.
type Config struct {
	InitialCapital float64
	Costs          Costs
	// RebalanceBand is the minimum gap between the target weight and the
	// currently held weight (as a fraction of equity) before the engine trades.
	// It models the reality that nobody rebalances a tiny drift, and stops a
	// fractional-weight target (e.g. a volatility-scaled position) from churning
	// every bar as prices move. Zero uses defaultRebalanceBand. A long/flat (0 or
	// 1) strategy is unaffected: its target jumps by ~100%, dwarfing any band.
	RebalanceBand float64
}

// defaultRebalanceBand is used when Config.RebalanceBand is zero: rebalance only
// once the position has drifted 1% of equity from its target.
const defaultRebalanceBand = 0.01

// Result is the outcome of one strategy run.
type Result struct {
	Strategy  string
	Dates     []string  // one per bar
	Equity    []float64 // mark-to-market portfolio value at each bar's close, post-trade
	Weights   []float64 // realised weight in the asset at each bar
	Trades    int       // number of bars on which a trade occurred
	Turnover  float64   // sum of traded notional over the run
	TotalCost float64   // sum of all costs paid
}

// Run walks the series bar by bar, asking the strategy for a target weight,
// rebalancing to it (net of costs), and recording the equity curve.
func Run(s series.Series, strat strategy.Strategy, cfg Config) (Result, error) {
	n := len(s.Points)
	if n < 2 {
		return Result{}, fmt.Errorf("engine: need >=2 bars, got %d", n)
	}
	if cfg.InitialCapital <= 0 {
		return Result{}, fmt.Errorf("engine: initial capital must be > 0, got %v", cfg.InitialCapital)
	}

	closes := make([]float64, n)
	for i, p := range s.Points {
		closes[i] = p.Close
	}

	res := Result{
		Strategy: strat.Name(),
		Dates:    make([]string, n),
		Equity:   make([]float64, n),
		Weights:  make([]float64, n),
	}

	cash := cfg.InitialCapital
	shares := 0.0
	band := cfg.RebalanceBand
	if band <= 0 {
		band = defaultRebalanceBand
	}

	for i := 0; i < n; i++ {
		price := closes[i]
		equity := cash + shares*price

		target := strat.Target(closes[:i+1])
		if target < 0 {
			target = 0
		} else if target > 1 {
			target = 1
		}

		targetShares := target * equity / price
		delta := targetShares - shares
		if math.Abs(delta*price) > band*equity {
			// Cost-aware sizing: paying the fee shrinks equity, so re-solve the
			// target against equity net of the (tiny) cost. This stops a constant
			// -weight rule (e.g. buy-and-hold) from churning every bar to unwind
			// the leverage its own fee would otherwise create.
			c := cfg.Costs.cost(math.Abs(delta) * price)
			targetShares = target * (equity - c) / price
			delta = targetShares - shares
			notional := math.Abs(delta) * price
			c = cfg.Costs.cost(notional)
			cash -= delta * price // buying (delta>0) spends cash; selling adds it
			cash -= c
			shares = targetShares
			res.Trades++
			res.Turnover += notional
			res.TotalCost += c
			equity = cash + shares*price // re-mark after paying costs
		}

		res.Dates[i] = s.Points[i].Date.Format("2006-01-02")
		res.Equity[i] = equity
		if equity > 0 {
			res.Weights[i] = shares * price / equity
		}
	}

	return res, nil
}

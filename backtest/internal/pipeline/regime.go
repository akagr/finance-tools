package pipeline

import (
	"fmt"
	"math"
	"sort"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

// BuildRegime breaks a strategy's performance down by market state, so you can
// see *where* an edge comes from. Each day is labelled by two independent
// schemes — trend (price above/below its long moving average → bull/bear) and
// volatility (trailing realised vol above/below the full-sample median →
// high/low) — and the strategy's return is compounded within each bucket, next
// to buy-and-hold over the same days.
//
// This is how you learn a trend rule "only earns its keep in bear/high-vol
// markets and bleeds in calm bull runs", or that a mean-reversion rule is the
// opposite. A strategy whose entire edge lives in one rare regime is fragile:
// it needs that regime to recur to pay off.
//
// Regime labels are descriptive (the volatility threshold uses the whole
// sample's median), so they explain the past — they are not themselves a
// tradeable, lookahead-free signal.
func BuildRegime(opts Options, trendMA, volWindow int) (report.Regime, error) {
	if trendMA < 2 || volWindow < 2 {
		return report.Regime{}, fmt.Errorf("pipeline: regime windows must be >= 2 (trend=%d vol=%d)", trendMA, volWindow)
	}
	if opts.Strategy == "all" {
		return report.Regime{}, fmt.Errorf("pipeline: regime needs a single strategy, got %q", opts.Strategy)
	}

	all, err := series.Load(opts.PricesPath)
	if err != nil {
		return report.Regime{}, err
	}
	if len(all) == 0 {
		return report.Regime{}, fmt.Errorf("pipeline: no price series in %s", opts.PricesPath)
	}
	s, err := pick(all, opts.Symbol)
	if err != nil {
		return report.Regime{}, err
	}
	n := len(s.Points)
	if n <= trendMA || n <= volWindow {
		return report.Regime{}, fmt.Errorf("pipeline: %d bars too few for trend=%d / vol=%d windows", n, trendMA, volWindow)
	}

	strat, err := buildStrategy(opts)
	if err != nil {
		return report.Regime{}, err
	}
	if strat, err = maybeVolTarget(strat, opts); err != nil {
		return report.Regime{}, err
	}
	capital := opts.InitialCapital
	if capital <= 0 {
		capital = 100000
	}
	costs := opts.Costs
	if costs == (engine.Costs{}) {
		costs = engine.DefaultCosts()
	}
	cfg := engine.Config{InitialCapital: capital, Costs: costs}

	stratRes, err := engine.Run(s, strat, cfg)
	if err != nil {
		return report.Regime{}, err
	}
	benchRes, err := engine.Run(s, strategy.BuyHold{}, cfg)
	if err != nil {
		return report.Regime{}, err
	}

	closes := make([]float64, n)
	for i, p := range s.Points {
		closes[i] = p.Close
	}
	stratR := equityReturns(stratRes.Equity)
	benchR := equityReturns(benchRes.Equity)

	// Trailing realised vol at each bar (annualised), and its full-sample median.
	volAt := make([]float64, n)
	for i := range volAt {
		volAt[i] = math.NaN()
	}
	var volVals []float64
	for i := volWindow; i < n; i++ {
		v := realisedVolCloses(closes, i, volWindow)
		volAt[i] = v
		volVals = append(volVals, v)
	}
	volMedian := median(volVals)

	// Accumulate daily returns into buckets. A return earned from bar i-1 to i is
	// attributed to the market state observed at bar i-1 (known in advance).
	buckets := map[string]*regimeAcc{
		"Bull": {}, "Bear": {}, "High": {}, "Low": {},
	}
	for i := 1; i < n; i++ {
		prev := i - 1
		sr, br := stratR[i-1], benchR[i-1]
		if prev >= trendMA {
			if closes[prev] >= sma(closes[:prev+1], trendMA) {
				buckets["Bull"].add(sr, br)
			} else {
				buckets["Bear"].add(sr, br)
			}
		}
		if prev >= volWindow && !math.IsNaN(volAt[prev]) {
			if volAt[prev] >= volMedian {
				buckets["High"].add(sr, br)
			} else {
				buckets["Low"].add(sr, br)
			}
		}
	}

	order := []struct{ group, name, key string }{
		{"Trend", "Bull", "Bull"}, {"Trend", "Bear", "Bear"},
		{"Volatility", "High vol", "High"}, {"Volatility", "Low vol", "Low"},
	}
	var out []report.RegimeBucket
	bestEdge, worstEdge := math.Inf(-1), math.Inf(1)
	var bestName, worstName string
	for _, o := range order {
		acc := buckets[o.key]
		sRet := compound(acc.strat)
		bRet := compound(acc.bench)
		out = append(out, report.RegimeBucket{
			Group: o.group, Name: o.name, Days: len(acc.strat),
			StratReturn: sRet, BenchReturn: bRet, StratSharpe: sharpeOf(acc.strat),
		})
		if len(acc.strat) > 0 {
			edge := sRet - bRet
			if edge > bestEdge {
				bestEdge, bestName = edge, o.name
			}
			if edge < worstEdge {
				worstEdge, worstName = edge, o.name
			}
		}
	}

	var notes []string
	if bestName != "" {
		notes = append(notes, fmt.Sprintf(
			"The strategy's edge over buy-and-hold is largest in the %s regime (%+.1f%%) and weakest in %s (%+.1f%%). An edge concentrated in one regime only pays off when that regime recurs — size your confidence accordingly.",
			bestName, bestEdge*100, worstName, worstEdge*100))
	}
	notes = append(notes, "Regime labels are descriptive: the high/low-vol split uses the whole sample's median, so this explains the past rather than being a lookahead-free trading signal.")

	return report.Regime{
		Meta: report.RegimeMeta{
			Symbol:    s.Label,
			Strategy:  stratRes.Strategy,
			Start:     stratRes.Dates[0],
			End:       stratRes.Dates[len(stratRes.Dates)-1],
			Bars:      n,
			TrendMA:   trendMA,
			VolWindow: volWindow,
			Notes:     notes,
		},
		Buckets: out,
	}, nil
}

// regimeAcc collects the strategy and benchmark daily returns for one bucket.
type regimeAcc struct {
	strat []float64
	bench []float64
}

func (a *regimeAcc) add(s, b float64) {
	a.strat = append(a.strat, s)
	a.bench = append(a.bench, b)
}

// equityReturns turns an equity curve into daily simple returns (length n-1).
func equityReturns(equity []float64) []float64 {
	out := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] > 0 {
			out = append(out, equity[i]/equity[i-1]-1)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

// realisedVolCloses is the annualised stdev of the close returns over the window
// ending at index i (exclusive of i's own forward return).
func realisedVolCloses(closes []float64, i, window int) float64 {
	if i < window {
		return 0
	}
	rets := make([]float64, 0, window)
	for k := i - window + 1; k <= i; k++ {
		if closes[k-1] > 0 {
			rets = append(rets, closes[k]/closes[k-1]-1)
		}
	}
	if len(rets) < 2 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var ss float64
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	return math.Sqrt(ss/float64(len(rets)-1)) * math.Sqrt(tradingDaysPerYear)
}

func compound(rets []float64) float64 {
	eq := 1.0
	for _, r := range rets {
		eq *= 1 + r
	}
	return eq - 1
}

func sharpeOf(rets []float64) float64 {
	if len(rets) < 2 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var ss float64
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	sd := math.Sqrt(ss / float64(len(rets)-1))
	if sd == 0 {
		return 0
	}
	return mean / sd * math.Sqrt(tradingDaysPerYear)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// sma is the mean of the last n values of xs.
func sma(xs []float64, n int) float64 {
	if n <= 0 || len(xs) < n {
		return 0
	}
	sum := 0.0
	for _, v := range xs[len(xs)-n:] {
		sum += v
	}
	return sum / float64(n)
}

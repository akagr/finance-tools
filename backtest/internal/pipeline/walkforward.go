package pipeline

import (
	"fmt"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/metrics"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

// BuildWalkForward runs one strategy continuously over the whole series, then
// slices the resulting equity curve into `folds` consecutive out-of-sample
// segments and reports each segment's performance against buy-and-hold. Running
// continuously (rather than restarting per fold) keeps indicators warmed up, so
// each fold is a genuine "how did it do over this stretch" measurement.
//
// The point is consistency: an edge that shows up in every fold is far more
// believable than one that came from a single lucky sub-period. It does not
// re-optimise parameters per fold — that is a later step — so it validates a
// fixed rule across time and regimes.
func BuildWalkForward(opts Options, folds int) (report.WalkForward, error) {
	if folds < 2 {
		return report.WalkForward{}, fmt.Errorf("pipeline: walk-forward needs >= 2 folds, got %d", folds)
	}
	if opts.Strategy == "all" || opts.Strategy == "buy-hold" {
		return report.WalkForward{}, fmt.Errorf("pipeline: walk-forward needs a single non-benchmark strategy, got %q", opts.Strategy)
	}

	all, err := series.Load(opts.PricesPath)
	if err != nil {
		return report.WalkForward{}, err
	}
	if len(all) == 0 {
		return report.WalkForward{}, fmt.Errorf("pipeline: no price series in %s", opts.PricesPath)
	}
	s, err := pick(all, opts.Symbol)
	if err != nil {
		return report.WalkForward{}, err
	}
	n := len(s.Points)
	// Each fold needs at least two bars to have a return.
	if n < 2*folds {
		return report.WalkForward{}, fmt.Errorf("pipeline: %d bars is too few for %d folds (need >= %d)", n, folds, 2*folds)
	}

	strat, err := buildStrategy(opts)
	if err != nil {
		return report.WalkForward{}, err
	}
	if strat, err = maybeVolTarget(strat, opts); err != nil {
		return report.WalkForward{}, err
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
		return report.WalkForward{}, err
	}
	benchRes, err := engine.Run(s, strategy.BuyHold{}, cfg)
	if err != nil {
		return report.WalkForward{}, err
	}

	bounds := foldBounds(n, folds)
	wfFolds := make([]report.Fold, 0, folds)
	beat := 0
	for k := 0; k < folds; k++ {
		a, b := bounds[k], bounds[k+1]
		sStats := sliceStats(stratRes, a, b)
		bStats := sliceStats(benchRes, a, b)
		didBeat := sStats.TotalReturn > bStats.TotalReturn
		if didBeat {
			beat++
		}
		wfFolds = append(wfFolds, report.Fold{
			Index:       k + 1,
			Start:       stratRes.Dates[a],
			End:         stratRes.Dates[b],
			StratReturn: sStats.TotalReturn,
			BenchReturn: bStats.TotalReturn,
			StratSharpe: sStats.Sharpe,
			StratMaxDD:  sStats.MaxDrawdown,
			Beat:        didBeat,
		})
	}

	var notes []string
	notes = append(notes, fmt.Sprintf(
		"The strategy beat buy-and-hold in %d of %d out-of-sample folds. Consistency across folds is the signal to look for; an edge concentrated in one fold is usually luck or a single favourable regime, not a repeatable strategy.",
		beat, folds))
	if opts.VolTarget > 0 {
		notes = append(notes, "Volatility targeting is on, so per-fold returns are of the risk-scaled position, not the raw signal.")
	}

	return report.WalkForward{
		Meta: report.WFMeta{
			Symbol:   s.Label,
			Strategy: stratRes.Strategy,
			Start:    stratRes.Dates[0],
			End:      stratRes.Dates[len(stratRes.Dates)-1],
			Bars:     n,
			Folds:    folds,
			Notes:    notes,
		},
		Folds: wfFolds,
	}, nil
}

// foldBounds returns folds+1 indices partitioning [0, n-1] into `folds`
// contiguous segments. Consecutive segments share a boundary bar, so their
// returns chain multiplicatively into the full-period return.
func foldBounds(n, folds int) []int {
	bounds := make([]int, folds+1)
	for k := 0; k <= folds; k++ {
		bounds[k] = k * (n - 1) / folds
	}
	bounds[folds] = n - 1
	return bounds
}

// sliceStats computes metrics over the equity curve slice [a, b] (inclusive),
// re-based so the segment starts at its own opening value. Trade-based fields
// are left zero: they are run-level totals, not attributable to a slice.
func sliceStats(res engine.Result, a, b int) metrics.Stats {
	dates := res.Dates[a : b+1]
	equity := res.Equity[a : b+1]
	weights := res.Weights[a : b+1]
	return metrics.Compute(dates, equity, weights, 0, 0, 0)
}

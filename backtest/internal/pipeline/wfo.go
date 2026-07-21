package pipeline

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/metrics"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

// BuildWalkForwardOpt runs an anchored walk-forward *optimisation*: the timeline
// is split into folds+1 equal segments; for each test fold the parameters are
// re-fit on all prior ("training") data by sweeping the grid for the best
// `metric`, and only then measured on the next unseen segment. Because the
// parameters never see the data they are judged on, the stitched-together
// out-of-sample folds are the most honest estimate this tool offers of how a
// tuned strategy would actually have performed live.
//
// This is the acid test: a rule that looks good only because its parameters were
// chosen with hindsight will fall apart here, whereas one whose best training
// parameters keep working out-of-sample has a far more credible edge.
func BuildWalkForwardOpt(opts Options, axes []SweepAxis, metric string, folds int, rolling bool) (report.WalkForward, error) {
	if folds < 2 {
		return report.WalkForward{}, fmt.Errorf("pipeline: walk-forward needs >= 2 folds, got %d", folds)
	}
	if opts.Strategy == "all" || opts.Strategy == "buy-hold" {
		return report.WalkForward{}, fmt.Errorf("pipeline: walk-forward needs a single non-benchmark strategy, got %q", opts.Strategy)
	}
	if len(axes) < 1 || len(axes) > 2 {
		return report.WalkForward{}, fmt.Errorf("pipeline: optimisation needs 1 or 2 parameters, got %d", len(axes))
	}
	getMetric, ok := statMetric(metric)
	if metric == "" {
		metric, getMetric, ok = "sharpe", statMetrics["sharpe"], true
	}
	if !ok {
		return report.WalkForward{}, fmt.Errorf("pipeline: unknown --metric %q (want %v)", metric, StatMetricNames())
	}

	axisValues := make([][]float64, len(axes))
	total := 1
	for i, ax := range axes {
		vals, err := axisTicks(ax)
		if err != nil {
			return report.WalkForward{}, err
		}
		axisValues[i] = vals
		total *= len(vals)
	}
	if total > maxSweepPoints {
		return report.WalkForward{}, fmt.Errorf("pipeline: grid has %d points, exceeds cap %d; widen the step or narrow the range", total, maxSweepPoints)
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
	// folds+1 segments (one initial training segment, then one test per fold),
	// each needing at least two bars.
	if n < 2*(folds+1) {
		return report.WalkForward{}, fmt.Errorf("pipeline: %d bars is too few for %d optimisation folds (need >= %d)", n, folds, 2*(folds+1))
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
	lowerIsBetter := metric == "drawdown"
	combos := combinations(axisValues)

	// Buy-and-hold over the whole series once; sliced per fold for the benchmark.
	benchRes, err := engine.Run(s, strategy.BuyHold{}, cfg)
	if err != nil {
		return report.WalkForward{}, err
	}

	bounds := foldBounds(n, folds+1) // folds+1 segments → indices 0..folds+1
	trainLen := bounds[1]            // one segment's worth of bars (bounds[0] == 0)
	wfFolds := make([]report.Fold, 0, folds)
	beat := 0
	for j := 1; j <= folds; j++ {
		aTest, bTest := bounds[j], bounds[j+1]
		// Anchored: train on all prior data. Rolling: train on a fixed-length
		// trailing window, so distant history can't dominate the fit.
		trainStart := 0
		if rolling {
			if ts := aTest - trainLen; ts > 0 {
				trainStart = ts
			}
		}
		train := series.Series{Label: s.Label, Points: s.Points[trainStart : aTest+1]}

		bestCoords, found, err := optimiseOnSeries(train, opts, axes, combos, cfg, getMetric, lowerIsBetter)
		if err != nil {
			return report.WalkForward{}, err
		}
		if !found {
			return report.WalkForward{}, fmt.Errorf("pipeline: fold %d has no valid parameter combination on its training window; widen the range or lengthen the history", j)
		}

		testOpts := opts
		for i, ax := range axes {
			if testOpts, err = applyParam(testOpts, ax.Name, bestCoords[i]); err != nil {
				return report.WalkForward{}, err
			}
		}
		strat, err := buildStrategy(testOpts)
		if err != nil {
			return report.WalkForward{}, err
		}
		if strat, err = maybeVolTarget(strat, testOpts); err != nil {
			return report.WalkForward{}, err
		}
		// Run from the start up to the test end so indicators are warm, then
		// measure only the test slice.
		upTo := series.Series{Label: s.Label, Points: s.Points[:bTest+1]}
		stratRes, err := engine.Run(upTo, strat, cfg)
		if err != nil {
			return report.WalkForward{}, err
		}
		sStats := sliceStats(stratRes, aTest, bTest)
		bStats := sliceStats(benchRes, aTest, bTest)
		didBeat := sStats.TotalReturn > bStats.TotalReturn
		if didBeat {
			beat++
		}
		wfFolds = append(wfFolds, report.Fold{
			Index:       j,
			Start:       s.Points[aTest].Date.Format("2006-01-02"),
			End:         s.Points[bTest].Date.Format("2006-01-02"),
			Params:      formatParams(axes, bestCoords),
			StratReturn: sStats.TotalReturn,
			BenchReturn: bStats.TotalReturn,
			StratSharpe: sStats.Sharpe,
			StratMaxDD:  sStats.MaxDrawdown,
			Beat:        didBeat,
		})
	}

	window := "anchored (all prior data)"
	if rolling {
		window = fmt.Sprintf("rolling (~%d-bar trailing window)", trainLen)
	}
	notes := []string{fmt.Sprintf(
		"Parameters were re-fit on prior data only (%s) and tested on the next unseen fold, so this is a genuine out-of-sample result: the strategy beat buy-and-hold in %d of %d folds. If the winning parameters keep changing wildly between folds, or the edge vanishes here despite looking good in a plain backtest, the rule was overfit.",
		window, beat, folds)}

	return report.WalkForward{
		Meta: report.WFMeta{
			Symbol:    s.Label,
			Strategy:  opts.Strategy,
			Start:     s.Points[0].Date.Format("2006-01-02"),
			End:       s.Points[n-1].Date.Format("2006-01-02"),
			Bars:      n,
			Folds:     folds,
			Optimised: true,
			Rolling:   rolling,
			Metric:    metric,
			Notes:     notes,
		},
		Folds: wfFolds,
	}, nil
}

// optimiseOnSeries sweeps the grid over a training series and returns the
// coordinates of the best-scoring valid combination. found is false when no
// combination was valid on that (possibly short) window.
func optimiseOnSeries(s series.Series, opts Options, axes []SweepAxis, combos [][]float64, cfg engine.Config, getMetric func(metrics.Stats) float64, lowerIsBetter bool) ([]float64, bool, error) {
	best := math.Inf(-1)
	if lowerIsBetter {
		best = math.Inf(1)
	}
	var bestCoords []float64
	for _, coords := range combos {
		st, valid, err := runCombo(s, opts, axes, coords, cfg)
		if err != nil {
			return nil, false, err
		}
		if !valid {
			continue
		}
		score := getMetric(st)
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		if (lowerIsBetter && score < best) || (!lowerIsBetter && score > best) {
			best = score
			bestCoords = append([]float64(nil), coords...)
		}
	}
	return bestCoords, bestCoords != nil, nil
}

// formatParams renders the chosen coordinates as "name=value" pairs.
func formatParams(axes []SweepAxis, coords []float64) string {
	parts := make([]string, len(axes))
	for i, ax := range axes {
		parts[i] = ax.Name + "=" + trimNum(coords[i])
	}
	return strings.Join(parts, " ")
}

func trimNum(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

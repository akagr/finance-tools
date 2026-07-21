package pipeline

import (
	"fmt"
	"math"
	"sort"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/metrics"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/series"
)

// SweepAxis is one parameter to vary and the inclusive range to vary it over.
type SweepAxis struct {
	Name string // fast|slow|lookback|rsi-period|rsi-threshold|entry|exit
	Min  float64
	Max  float64
	Step float64
}

// maxSweepPoints caps the grid size so a careless range can't spin forever.
const maxSweepPoints = 2000

// BuildSweep re-runs a single strategy across a grid of one or two parameters
// and reports the chosen metric at every point. Its purpose is to reveal the
// *shape* of the parameter surface: a broad region of good values means the edge
// is robust, whereas a lone spike surrounded by poor values is almost certainly
// overfit — a parameter tuned to this history's noise that will not repeat.
func BuildSweep(opts Options, axes []SweepAxis, metric string) (report.Sweep, error) {
	if len(axes) < 1 || len(axes) > 2 {
		return report.Sweep{}, fmt.Errorf("pipeline: sweep needs 1 or 2 parameters, got %d", len(axes))
	}
	if opts.Strategy == "all" || opts.Strategy == "buy-hold" {
		return report.Sweep{}, fmt.Errorf("pipeline: sweep needs a single non-benchmark strategy, got %q", opts.Strategy)
	}
	if _, ok := statMetric(metric); !ok && metric != "" {
		return report.Sweep{}, fmt.Errorf("pipeline: unknown --metric %q (want %v)", metric, StatMetricNames())
	}
	if metric == "" {
		metric = "sharpe"
	}

	axisValues := make([][]float64, len(axes))
	total := 1
	for i, ax := range axes {
		vals, err := axisTicks(ax)
		if err != nil {
			return report.Sweep{}, err
		}
		axisValues[i] = vals
		total *= len(vals)
	}
	if total > maxSweepPoints {
		return report.Sweep{}, fmt.Errorf("pipeline: sweep grid has %d points, exceeds cap %d; widen the step or narrow the range", total, maxSweepPoints)
	}

	all, err := series.Load(opts.PricesPath)
	if err != nil {
		return report.Sweep{}, err
	}
	if len(all) == 0 {
		return report.Sweep{}, fmt.Errorf("pipeline: no price series in %s", opts.PricesPath)
	}
	s, err := pick(all, opts.Symbol)
	if err != nil {
		return report.Sweep{}, err
	}
	if len(s.Points) < 2 {
		return report.Sweep{}, fmt.Errorf("pipeline: series %q has %d bars, need >=2", s.Label, len(s.Points))
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
	getMetric, _ := statMetric(metric)

	names := make([]string, len(axes))
	for i, ax := range axes {
		names[i] = ax.Name
	}

	var points []report.SweepPoint
	best := math.Inf(-1)
	worst := math.Inf(1)
	lowerIsBetter := metric == "drawdown"
	if lowerIsBetter {
		best, worst = math.Inf(1), math.Inf(-1)
	}

	combos := combinations(axisValues)
	for _, coords := range combos {
		st, valid, err := runCombo(s, opts, axes, coords, cfg)
		if err != nil {
			return report.Sweep{}, err
		}
		if !valid {
			// Invalid combination (e.g. fast >= slow): record it as a gap so the
			// grid stays rectangular rather than aborting the whole sweep.
			points = append(points, report.SweepPoint{Coords: append([]float64(nil), coords...), Valid: false})
			continue
		}
		mv := getMetric(st)
		points = append(points, report.SweepPoint{
			Coords: append([]float64(nil), coords...), Valid: true,
			Return: st.TotalReturn, CAGR: st.CAGR, Sharpe: st.Sharpe,
			Sortino: st.Sortino, MaxDrawdown: st.MaxDrawdown, Calmar: st.Calmar,
			Trades: st.Trades, Metric: mv,
		})
		if lowerIsBetter {
			if mv < best {
				best = mv
			}
			if mv > worst {
				worst = mv
			}
		} else {
			if mv > best {
				best = mv
			}
			if mv < worst {
				worst = mv
			}
		}
	}

	notes := []string{
		"Look at the shape, not the single best cell. A broad region of good values means the edge is robust to the exact parameters; a lone peak surrounded by poor values is almost certainly overfit to this history and will not repeat.",
	}

	return report.Sweep{
		Meta: report.SweepMeta{
			Symbol:   s.Label,
			Strategy: opts.Strategy,
			Start:    s.Points[0].Date.Format("2006-01-02"),
			End:      s.Points[len(s.Points)-1].Date.Format("2006-01-02"),
			Bars:     len(s.Points),
			Metric:   metric,
			Notes:    notes,
		},
		AxisNames:     names,
		AxisValues:    axisValues,
		Points:        points,
		Best:          best,
		Worst:         worst,
		LowerIsBetter: lowerIsBetter,
	}, nil
}

// axisTicks expands an axis into its inclusive list of values.
func axisTicks(ax SweepAxis) ([]float64, error) {
	if ax.Step <= 0 {
		return nil, fmt.Errorf("pipeline: sweep step for %q must be > 0 (got %v)", ax.Name, ax.Step)
	}
	if ax.Max < ax.Min {
		return nil, fmt.Errorf("pipeline: sweep max (%v) < min (%v) for %q", ax.Max, ax.Min, ax.Name)
	}
	var out []float64
	// The 1e-9 nudge keeps the last tick from being dropped by float rounding.
	for v := ax.Min; v <= ax.Max+1e-9; v += ax.Step {
		out = append(out, v)
	}
	return out, nil
}

// combinations returns the cartesian product of the per-axis value lists, with
// the last axis varying fastest (row-major for a 2-D grid).
func combinations(axisValues [][]float64) [][]float64 {
	result := [][]float64{{}}
	for _, vals := range axisValues {
		var next [][]float64
		for _, prefix := range result {
			for _, v := range vals {
				row := append(append([]float64(nil), prefix...), v)
				next = append(next, row)
			}
		}
		result = next
	}
	return result
}

// runCombo applies the coords to opts, builds the strategy (with any vol-target
// overlay) and runs it over s, returning its stats. valid is false — with a nil
// error — when the parameter combination is rejected by the strategy (e.g. a
// crossover with fast >= slow), so callers can skip it without aborting.
func runCombo(s series.Series, opts Options, axes []SweepAxis, coords []float64, cfg engine.Config) (metrics.Stats, bool, error) {
	runOpts := opts
	for i, ax := range axes {
		var err error
		if runOpts, err = applyParam(runOpts, ax.Name, coords[i]); err != nil {
			return metrics.Stats{}, false, err
		}
	}
	strat, err := buildStrategy(runOpts)
	if err != nil {
		return metrics.Stats{}, false, nil
	}
	if strat, err = maybeVolTarget(strat, runOpts); err != nil {
		return metrics.Stats{}, false, err
	}
	res, err := engine.Run(s, strat, cfg)
	if err != nil {
		return metrics.Stats{}, false, err
	}
	return metrics.Compute(res.Dates, res.Equity, res.Weights, res.Trades, res.Turnover, res.TotalCost), true, nil
}

// applyParam sets the named parameter on a copy of opts.
func applyParam(opts Options, name string, v float64) (Options, error) {
	switch name {
	case "fast":
		opts.Fast = int(math.Round(v))
	case "slow":
		opts.Slow = int(math.Round(v))
	case "lookback":
		opts.Lookback = int(math.Round(v))
	case "rsi-period":
		opts.RSIPeriod = int(math.Round(v))
	case "rsi-threshold":
		opts.RSIThreshold = v
	case "entry":
		opts.DonchianEntry = int(math.Round(v))
	case "exit":
		opts.DonchianExit = int(math.Round(v))
	default:
		return opts, fmt.Errorf("pipeline: unknown sweep parameter %q (want fast|slow|lookback|rsi-period|rsi-threshold|entry|exit)", name)
	}
	return opts, nil
}

// statMetrics maps a metric name to how it is read from metrics.Stats. Shared by
// the sweep grid; mirrors the report.Line sortKeys used by --sort.
var statMetrics = map[string]func(metrics.Stats) float64{
	"return":   func(s metrics.Stats) float64 { return s.TotalReturn },
	"cagr":     func(s metrics.Stats) float64 { return s.CAGR },
	"sharpe":   func(s metrics.Stats) float64 { return s.Sharpe },
	"sortino":  func(s metrics.Stats) float64 { return s.Sortino },
	"calmar":   func(s metrics.Stats) float64 { return s.Calmar },
	"drawdown": func(s metrics.Stats) float64 { return s.MaxDrawdown },
}

func statMetric(name string) (func(metrics.Stats) float64, bool) {
	f, ok := statMetrics[name]
	return f, ok
}

// StatMetricNames returns the accepted --metric values, sorted, for help and
// validation messages.
func StatMetricNames() []string {
	names := make([]string, 0, len(statMetrics))
	for k := range statMetrics {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

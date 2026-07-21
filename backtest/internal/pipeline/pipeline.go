// Package pipeline is the orchestration seam shared by the CLI and the golden
// test: it loads a price series, runs the chosen strategy and the buy-and-hold
// benchmark through the engine, computes performance metrics, and assembles a
// render-ready report. The command layer does I/O only.
package pipeline

import (
	"fmt"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/metrics"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/series"
	"github.com/akagr/finance-tools/backtest/internal/strategy"
)

// Options configures a backtest run.
type Options struct {
	PricesPath     string  // CSV file (columns: date,symbol,close)
	Symbol         string  // which symbol in the CSV to test; "" = first found
	Strategy       string  // "sma-cross" (only active strategy for now)
	Fast           int     // fast SMA window (sma-cross)
	Slow           int     // slow SMA window (sma-cross)
	InitialCapital float64 // starting cash; defaults to 100000 if <= 0
	Costs          engine.Costs
}

// smallSample is the point below which results are flagged as unreliable.
const smallSample = 60

// BuildReport runs the full offline backtest and returns a render-ready report.
func BuildReport(opts Options) (report.Report, error) {
	all, err := series.Load(opts.PricesPath)
	if err != nil {
		return report.Report{}, err
	}
	if len(all) == 0 {
		return report.Report{}, fmt.Errorf("pipeline: no price series in %s", opts.PricesPath)
	}

	s, err := pick(all, opts.Symbol)
	if err != nil {
		return report.Report{}, err
	}
	if len(s.Points) < 2 {
		return report.Report{}, fmt.Errorf("pipeline: series %q has %d bars, need >=2", s.Label, len(s.Points))
	}

	strat, err := buildStrategy(opts)
	if err != nil {
		return report.Report{}, err
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
		return report.Report{}, err
	}
	benchRes, err := engine.Run(s, strategy.BuyHold{}, cfg)
	if err != nil {
		return report.Report{}, err
	}

	stratStats := metrics.Compute(stratRes.Dates, stratRes.Equity, stratRes.Weights, stratRes.Trades, stratRes.Turnover, stratRes.TotalCost)
	benchStats := metrics.Compute(benchRes.Dates, benchRes.Equity, benchRes.Weights, benchRes.Trades, benchRes.Turnover, benchRes.TotalCost)

	var notes []string
	if len(s.Points) < smallSample {
		notes = append(notes, fmt.Sprintf("Small sample: only %d bars. Metrics are noisy; use a longer date range before trusting any edge.", len(s.Points)))
	}
	if stratStats.TotalReturn <= benchStats.TotalReturn {
		notes = append(notes, "The strategy did not beat buy-and-hold after costs over this period — the expected outcome for most simple rules, and exactly why you backtest before deploying capital.")
	}

	rep := report.Report{
		Meta: report.Meta{
			Symbol:         s.Label,
			Start:          stratRes.Dates[0],
			End:            stratRes.Dates[len(stratRes.Dates)-1],
			Bars:           len(s.Points),
			InitialCapital: capital,
			BrokerageBps:   costs.BrokerageBps,
			STTBps:         costs.STTBps,
			SlippageBps:    costs.SlippageBps,
			Notes:          notes,
		},
		Lines: []report.Line{
			report.LineFrom(stratRes.Strategy, stratStats),
			report.LineFrom(benchRes.Strategy, benchStats),
		},
	}
	return rep, nil
}

func pick(all []series.Series, symbol string) (series.Series, error) {
	if symbol == "" {
		return all[0], nil
	}
	for _, s := range all {
		if s.Label == symbol {
			return s, nil
		}
	}
	labels := make([]string, len(all))
	for i, s := range all {
		labels[i] = s.Label
	}
	return series.Series{}, fmt.Errorf("pipeline: symbol %q not found; available: %v", symbol, labels)
}

func buildStrategy(opts Options) (strategy.Strategy, error) {
	switch opts.Strategy {
	case "", "sma-cross":
		fast, slow := opts.Fast, opts.Slow
		if fast == 0 {
			fast = 20
		}
		if slow == 0 {
			slow = 50
		}
		return strategy.NewSMACross(fast, slow)
	case "buy-hold":
		return strategy.BuyHold{}, nil
	default:
		return nil, fmt.Errorf("pipeline: unknown strategy %q (want sma-cross|buy-hold)", opts.Strategy)
	}
}

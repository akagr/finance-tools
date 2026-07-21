// Package pipeline is the orchestration seam shared by the CLI and the golden
// test: it loads a price series, runs one strategy (or all of them) plus the
// buy-and-hold benchmark through the engine, computes performance metrics, and
// assembles a render-ready report. The command layer does I/O only.
package pipeline

import (
	"fmt"
	"sort"

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
	Strategy       string  // one of the names below, or "all" to run every strategy
	Fast           int     // fast MA window (sma-cross, ema-cross)
	Slow           int     // slow MA window (sma-cross, ema-cross)
	Lookback       int     // lookback window (momentum)
	RSIPeriod      int     // RSI period (rsi)
	RSIThreshold   float64 // buy when RSI is below this (rsi)
	DonchianEntry  int     // breakout entry window (donchian)
	DonchianExit   int     // breakdown exit window (donchian)
	InitialCapital float64 // starting cash; defaults to 100000 if <= 0
	Costs          engine.Costs
	SortBy         string  // metric to rank the table by; "" = return (see sortKeys)
	VolTarget      float64 // >0 enables volatility targeting at this annualised level (e.g. 0.10)
	VolLookback    int     // trailing bars used to estimate realised vol (default 20)
}

// smallSample is the point below which results are flagged as unreliable.
const smallSample = 60

// benchmarkName is the strategy every other is measured against.
const benchmarkName = "buy-hold"

// BuildReport runs the full offline backtest and returns a render-ready report.
// When Options.Strategy is "all", every strategy is run and compared in one
// table; otherwise the chosen strategy is shown against the buy-and-hold
// benchmark. Lines are sorted by total return (best first), so where the
// benchmark lands makes it obvious which strategies actually beat it.
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

	strats, err := strategiesFor(opts)
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

	var (
		lines       []report.Line
		firstDate   string
		lastDate    string
		benchReturn float64
	)
	for _, st := range strats {
		res, err := engine.Run(s, st, cfg)
		if err != nil {
			return report.Report{}, err
		}
		stats := metrics.Compute(res.Dates, res.Equity, res.Weights, res.Trades, res.Turnover, res.TotalCost)
		lines = append(lines, report.LineFrom(res.Strategy, stats))
		if res.Strategy == benchmarkName {
			benchReturn = stats.TotalReturn
		}
		firstDate, lastDate = res.Dates[0], res.Dates[len(res.Dates)-1]
	}

	// Rank the table best-first by the chosen metric; the benchmark falls into
	// its natural rank, making it obvious which strategies beat it.
	if err := sortLines(lines, opts.SortBy); err != nil {
		return report.Report{}, err
	}

	notes := buildNotes(opts, len(s.Points), lines, benchReturn)

	rep := report.Report{
		Meta: report.Meta{
			Symbol:         s.Label,
			Start:          firstDate,
			End:            lastDate,
			Bars:           len(s.Points),
			InitialCapital: capital,
			BrokerageBps:   costs.BrokerageBps,
			STTBps:         costs.STTBps,
			SlippageBps:    costs.SlippageBps,
			Notes:          notes,
		},
		Lines: lines,
	}
	return rep, nil
}

// buildNotes assembles the review flags shown beneath the table.
func buildNotes(opts Options, bars int, lines []report.Line, benchReturn float64) []string {
	var notes []string
	if bars < smallSample {
		notes = append(notes, fmt.Sprintf("Small sample: only %d bars. Metrics are noisy; use a longer date range before trusting any edge.", bars))
	}
	if opts.Strategy == "all" {
		beat := 0
		total := 0
		for _, l := range lines {
			if l.Strategy == benchmarkName {
				continue
			}
			total++
			if l.TotalReturn > benchReturn {
				beat++
			}
		}
		notes = append(notes, fmt.Sprintf("%d of %d strategies beat buy-and-hold after costs over this period. Beating a benchmark on past data is not an edge — it is a hypothesis to validate out-of-sample before risking capital.", beat, total))
		return notes
	}
	// Single-strategy mode: flag when the chosen rule (the non-benchmark line)
	// failed to beat buy-and-hold.
	for _, l := range lines {
		if l.Strategy != benchmarkName && l.TotalReturn <= benchReturn {
			notes = append(notes, "The strategy did not beat buy-and-hold after costs over this period — the expected outcome for most simple rules, and exactly why you backtest before deploying capital.")
			break
		}
	}
	return notes
}

// sortKey extracts the metric a line is ranked by; lowerIsBetter marks metrics
// (drawdown) where a smaller value ranks higher.
type sortKey struct {
	get           func(report.Line) float64
	lowerIsBetter bool
}

// sortKeys maps --sort values to how the comparison table is ordered. "return"
// is the default. For every key the table is rendered best-first.
var sortKeys = map[string]sortKey{
	"return":   {get: func(l report.Line) float64 { return l.TotalReturn }},
	"cagr":     {get: func(l report.Line) float64 { return l.CAGR }},
	"sharpe":   {get: func(l report.Line) float64 { return l.Sharpe }},
	"sortino":  {get: func(l report.Line) float64 { return l.Sortino }},
	"calmar":   {get: func(l report.Line) float64 { return l.Calmar }},
	"drawdown": {get: func(l report.Line) float64 { return l.MaxDrawdown }, lowerIsBetter: true},
}

// SortKeyNames returns the accepted --sort values, sorted, for help text and
// validation messages.
func SortKeyNames() []string {
	names := make([]string, 0, len(sortKeys))
	for k := range sortKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// sortLines ranks lines best-first by the named metric (default "return").
func sortLines(lines []report.Line, by string) error {
	if by == "" {
		by = "return"
	}
	key, ok := sortKeys[by]
	if !ok {
		return fmt.Errorf("pipeline: unknown --sort %q (want one of %v)", by, SortKeyNames())
	}
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := key.get(lines[i]), key.get(lines[j])
		if key.lowerIsBetter {
			return a < b
		}
		return a > b
	})
	return nil
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

// strategiesFor returns the strategies to run for a given Options, always
// including the buy-and-hold benchmark exactly once. For "all" it returns every
// active strategy (built with the configured or default parameters) plus the
// benchmark; otherwise the single chosen strategy plus the benchmark (or just
// the benchmark if that is what was chosen). Active strategies are wrapped with
// a volatility-targeting overlay when Options.VolTarget > 0; the benchmark is
// always left pure so the comparison stays honest.
func strategiesFor(opts Options) ([]strategy.Strategy, error) {
	if opts.Strategy == "all" {
		out := make([]strategy.Strategy, 0, len(activeNames)+1)
		for _, name := range activeNames {
			st, err := buildStrategy(withStrategy(opts, name))
			if err != nil {
				return nil, err
			}
			if st, err = maybeVolTarget(st, opts); err != nil {
				return nil, err
			}
			out = append(out, st)
		}
		return append(out, strategy.BuyHold{}), nil
	}

	st, err := buildStrategy(opts)
	if err != nil {
		return nil, err
	}
	if st.Name() == benchmarkName {
		return []strategy.Strategy{st}, nil
	}
	if st, err = maybeVolTarget(st, opts); err != nil {
		return nil, err
	}
	return []strategy.Strategy{st, strategy.BuyHold{}}, nil
}

// maybeVolTarget wraps st with a volatility-targeting overlay when enabled,
// otherwise returns st unchanged.
func maybeVolTarget(st strategy.Strategy, opts Options) (strategy.Strategy, error) {
	if opts.VolTarget <= 0 {
		return st, nil
	}
	return strategy.NewVolTarget(st, opts.VolTarget, orDefaultInt(opts.VolLookback, 20))
}

// activeNames lists the non-benchmark strategies, in display order, that "all"
// runs. Keep in sync with buildStrategy.
var activeNames = []string{"sma-cross", "ema-cross", "momentum", "rsi", "donchian"}

func withStrategy(opts Options, name string) Options {
	opts.Strategy = name
	return opts
}

func buildStrategy(opts Options) (strategy.Strategy, error) {
	switch opts.Strategy {
	case "", "sma-cross":
		return strategy.NewSMACross(orDefaultInt(opts.Fast, 20), orDefaultInt(opts.Slow, 50))
	case "ema-cross":
		return strategy.NewEMACross(orDefaultInt(opts.Fast, 20), orDefaultInt(opts.Slow, 50))
	case "momentum":
		return strategy.NewMomentum(orDefaultInt(opts.Lookback, 120))
	case "rsi":
		return strategy.NewRSI(orDefaultInt(opts.RSIPeriod, 14), orDefaultFloat(opts.RSIThreshold, 30))
	case "donchian":
		return strategy.NewDonchian(orDefaultInt(opts.DonchianEntry, 20), orDefaultInt(opts.DonchianExit, 10))
	case "buy-hold":
		return strategy.BuyHold{}, nil
	default:
		return nil, fmt.Errorf("pipeline: unknown strategy %q (want all|sma-cross|ema-cross|momentum|rsi|donchian|buy-hold)", opts.Strategy)
	}
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orDefaultFloat(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

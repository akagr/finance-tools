package pipeline

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/report"
)

var update = flag.Bool("update", false, "update golden files")

const fixtures = "../../testdata"

func render(t *testing.T, opts Options, format string) []byte {
	t.Helper()
	rep, err := BuildReport(opts)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := report.Render(&buf, rep, format); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run: go test ./internal/pipeline -update)", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch; run: go test ./internal/pipeline -update to review the diff", name)
	}
}

// Locks the whole offline backtest render path: SMA crossover vs buy-and-hold on
// the synthetic fixture, with fixed capital and costs, in every format.
func TestGoldenSMACross(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		Fast:           5,
		Slow:           20,
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	checkGolden(t, "sma_cross.md", render(t, opts, "md"))
	checkGolden(t, "sma_cross.csv", render(t, opts, "csv"))
	checkGolden(t, "sma_cross.json", render(t, opts, "json"))
}

// Locks the render + strategy path for each remaining strategy against the same
// synthetic fixture (Markdown only, to keep the golden set manageable).
func TestGoldenStrategies(t *testing.T) {
	base := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	cases := []struct {
		golden string
		mutate func(*Options)
	}{
		{"ema_cross.md", func(o *Options) { o.Strategy = "ema-cross"; o.Fast = 5; o.Slow = 20 }},
		{"momentum.md", func(o *Options) { o.Strategy = "momentum"; o.Lookback = 20 }},
		{"rsi.md", func(o *Options) { o.Strategy = "rsi"; o.RSIPeriod = 14; o.RSIThreshold = 45 }},
		{"donchian.md", func(o *Options) { o.Strategy = "donchian"; o.DonchianEntry = 20; o.DonchianExit = 10 }},
	}
	for _, c := range cases {
		opts := base
		c.mutate(&opts)
		checkGolden(t, c.golden, render(t, opts, "md"))
	}
}

// Locks the multi-strategy comparison path: "all" runs every strategy plus the
// benchmark into one sorted table.
func TestGoldenAll(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "all",
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	checkGolden(t, "all.md", render(t, opts, "md"))
	checkGolden(t, "all.json", render(t, opts, "json"))
}

// Locks the volatility-targeting overlay path: the active strategy is wrapped
// and sized, while the benchmark stays pure.
func TestGoldenVolTarget(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		Fast:           5,
		Slow:           20,
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
		VolTarget:      0.10,
		VolLookback:    20,
	}
	checkGolden(t, "voltarget.md", render(t, opts, "md"))
}

// Locks the walk-forward render path across every format.
func TestGoldenWalkForward(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		Fast:           3,
		Slow:           8,
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	wf, err := BuildWalkForward(opts, 4)
	if err != nil {
		t.Fatal(err)
	}
	renderWF := func(format string) []byte {
		var buf bytes.Buffer
		if err := report.RenderWalkForward(&buf, wf, format); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	checkGolden(t, "walkforward.md", renderWF("md"))
	checkGolden(t, "walkforward.csv", renderWF("csv"))
	checkGolden(t, "walkforward.json", renderWF("json"))
}

// Locks the walk-forward optimisation render path (Params column, optimised
// header) across formats.
func TestGoldenWalkForwardOpt(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	axes := []SweepAxis{
		{Name: "fast", Min: 3, Max: 6, Step: 3},
		{Name: "slow", Min: 8, Max: 12, Step: 4},
	}
	wf, err := BuildWalkForwardOpt(opts, axes, "sharpe", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	renderWFO := func(format string) []byte {
		var buf bytes.Buffer
		if err := report.RenderWalkForward(&buf, wf, format); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	checkGolden(t, "wfo.md", renderWFO("md"))
	checkGolden(t, "wfo.csv", renderWFO("csv"))
	checkGolden(t, "wfo.json", renderWFO("json"))
}

// Locks the Monte-Carlo render path (deterministic via a fixed seed).
func TestGoldenMonteCarlo(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		Fast:           3,
		Slow:           8,
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	mc, err := BuildMonteCarlo(opts, 500, 12345)
	if err != nil {
		t.Fatal(err)
	}
	renderMC := func(format string) []byte {
		var buf bytes.Buffer
		if err := report.RenderMonteCarlo(&buf, mc, format); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	checkGolden(t, "montecarlo.md", renderMC("md"))
	checkGolden(t, "montecarlo.json", renderMC("json"))
}

// Locks the regime-analysis render path.
func TestGoldenRegime(t *testing.T) {
	opts := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		Strategy:       "sma-cross",
		Fast:           3,
		Slow:           8,
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	rg, err := BuildRegime(opts, 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	renderRg := func(format string) []byte {
		var buf bytes.Buffer
		if err := report.RenderRegime(&buf, rg, format); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	checkGolden(t, "regime.md", renderRg("md"))
	checkGolden(t, "regime.csv", renderRg("csv"))
	checkGolden(t, "regime.json", renderRg("json"))
}

// Locks the parameter-sweep render path: a 2-D crossover grid (with invalid
// cells) and a 1-D momentum sweep.
func TestGoldenSweep(t *testing.T) {
	base := Options{
		PricesPath:     filepath.Join(fixtures, "prices.csv"),
		InitialCapital: 100000,
		Costs:          engine.Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5},
	}
	renderSw := func(sw report.Sweep, format string) []byte {
		var buf bytes.Buffer
		if err := report.RenderSweep(&buf, sw, format); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	grid := base
	grid.Strategy = "sma-cross"
	sw2d, err := BuildSweep(grid, []SweepAxis{
		{Name: "fast", Min: 5, Max: 15, Step: 5},
		{Name: "slow", Min: 10, Max: 30, Step: 10},
	}, "sharpe")
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "sweep_2d.md", renderSw(sw2d, "md"))
	checkGolden(t, "sweep_2d.json", renderSw(sw2d, "json"))

	mom := base
	mom.Strategy = "momentum"
	sw1d, err := BuildSweep(mom, []SweepAxis{{Name: "lookback", Min: 10, Max: 40, Step: 10}}, "cagr")
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "sweep_1d.md", renderSw(sw1d, "md"))
	checkGolden(t, "sweep_1d.csv", renderSw(sw1d, "csv"))
}

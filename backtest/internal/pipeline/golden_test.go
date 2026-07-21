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

package pipeline

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/akagr/finance-tools/correlation/internal/report"
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

// Native mode: mixed USD/INR series, weekly log returns. Exercises the
// mixed-currency and small-sample notes.
func TestGoldenNative(t *testing.T) {
	opts := Options{
		PricesPath:      filepath.Join(fixtures, "prices.csv"),
		DefaultCurrency: "USD",
		Frequency:       "weekly",
		ReturnKind:      "log",
	}
	checkGolden(t, "native.md", render(t, opts, "md"))
	checkGolden(t, "native.csv", render(t, opts, "csv"))
	checkGolden(t, "native.json", render(t, opts, "json"))
}

// Base-currency mode: both series converted to INR via the FX fixture.
func TestGoldenBaseINR(t *testing.T) {
	opts := Options{
		PricesPath:      filepath.Join(fixtures, "prices.csv"),
		DefaultCurrency: "USD",
		BaseCurrency:    "INR",
		FXPath:          filepath.Join(fixtures, "fx.csv"),
		Frequency:       "weekly",
		ReturnKind:      "log",
	}
	checkGolden(t, "base_inr.md", render(t, opts, "md"))
	checkGolden(t, "base_inr.json", render(t, opts, "json"))
}

package pipeline

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/akagr/finance-tools/schedule-fa/internal/entities"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/ibkr"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
	"github.com/akagr/finance-tools/schedule-fa/internal/report"
)

// Regenerate the golden files with:  go test ./internal/pipeline -update
var update = flag.Bool("update", false, "update golden files")

// TestGoldenOfflineReport locks the full offline path — parse a Flex XML, convert
// with SBI TTBR, compute the approximate peak, build Tables A2/A3, and render —
// against checked-in golden output. Inputs are the synthetic fixtures from the
// ibkr and fx packages (no real data, mode C so the result is deterministic).
// Entity overrides are supplied for all three issuers, so the report exercises
// both fully clean rows (MSFT, VOO) and a row that still needs review despite
// complete metadata because of a genuine data gap (AAPL: a forward split plus a
// pre-history acquisition date with no TTBR).
func TestGoldenOfflineReport(t *testing.T) {
	st, err := ibkr.ParseFlexFile("../ibkr/testdata/sample_flex.xml", 2024)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := fx.NewCSVStore()
	if err := store.LoadRateKeeperFile(model.USD, "../fx/testdata/SBI_REFERENCE_RATES_USD.csv"); err != nil {
		t.Fatalf("rates: %v", err)
	}
	ents, err := entities.Load("testdata/entities.csv")
	if err != nil {
		t.Fatalf("entities: %v", err)
	}

	res, err := BuildReport(st, store, nil, ents) // mode C, with entity overrides
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	for _, f := range []report.Format{report.CSV, report.JSON, report.Markdown, report.HTML} {
		rnd, err := report.For(f)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := rnd.Render(&buf, res.Report); err != nil {
			t.Fatalf("render %s: %v", f, err)
		}
		assertGolden(t, "report."+string(f), buf.Bytes())
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run: go test ./internal/pipeline -update)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden — run `go test ./internal/pipeline -update` to refresh "+
			"if the change is intended.\n--- got ---\n%s", name, truncate(got, want))
	}
}

// truncate shows the first line that differs, to keep failures readable.
func truncate(got, want []byte) string {
	gl, wl := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if !bytes.Equal(gl[i], wl[i]) {
			return "line " + itoa(i+1) + ":\n  got:  " + string(gl[i]) + "\n  want: " + string(wl[i])
		}
	}
	if len(gl) != len(wl) {
		return "line count: got " + itoa(len(gl)) + ", want " + itoa(len(wl))
	}
	return "(identical line-by-line; trailing bytes differ)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

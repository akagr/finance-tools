package report

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
	"github.com/akagr/tax-tools/schedule-fa/internal/schedulefa"
)

func sampleReport() *schedulefa.Report {
	conv := fx.Conversion{
		Source:   model.NewMoney(model.USD, big.NewRat(1500, 1)),
		Rate:     fx.Rate{Currency: model.USD, Date: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), INRPerUnit: big.NewRat(80, 1)},
		RateDate: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		Result:   model.NewMoney(model.INR, big.NewRat(120000, 1)),
	}
	amt := schedulefa.Amount{INR: model.NewMoney(model.INR, big.NewRat(120000, 1)), Audit: []fx.Conversion{conv}}
	return &schedulefa.Report{
		Year: 2024,
		A3: []schedulefa.A3Row{{
			CountryName: "United States of America", CountryCode: "2",
			EntityName: "Alpha, Inc", NatureEntity: "Listed equity share", AcquiredOn: "2024-03-15",
			InitialValue: amt, PeakValue: amt, PeakApprox: true, ClosingValue: amt,
			NeedsReview: true, ReviewNote: "peak is approximate (mode C)",
		}},
	}
}

func render(t *testing.T, f Format, r *schedulefa.Report) string {
	t.Helper()
	rnd, err := For(f)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := rnd.Render(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestJSONRender(t *testing.T) {
	out := render(t, JSON, sampleReport())
	var got jsonReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Year != 2024 || len(got.A3) != 1 {
		t.Fatalf("unexpected JSON: %+v", got)
	}
	row := got.A3[0]
	if row.PeakValue != "120000.00" || row.CountryCode != "2" || !row.NeedsReview {
		t.Errorf("unexpected row: %+v", row)
	}
	if len(row.Audit) == 0 || row.Audit[0].TTBR != "80.0000" || row.Audit[0].RateDate != "2024-12-31" {
		t.Errorf("audit not rendered: %+v", row.Audit)
	}
}

func TestCSVRenderQuotesCommas(t *testing.T) {
	out := render(t, CSV, sampleReport())
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 row, got %d lines", len(lines))
	}
	// "Alpha, Inc" contains a comma and must be quoted.
	if !strings.Contains(lines[1], `"Alpha, Inc"`) {
		t.Errorf("entity with comma not quoted: %s", lines[1])
	}
	if !strings.Contains(lines[1], "120000.00") {
		t.Errorf("INR figure missing: %s", lines[1])
	}
}

func TestHTMLRender(t *testing.T) {
	out := render(t, HTML, sampleReport())
	for _, want := range []string{
		"<!doctype html>", "Schedule FA — calendar year 2024",
		"Table A2", "Table A3", "Audit trail", "Alpha, Inc",
		"<span class=\"flag\">", "@media print",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q", want)
		}
	}
	// Entity name with a comma must be HTML-escaped contextually (no raw injection).
	if strings.Contains(out, "<script") {
		t.Error("unexpected raw script in output")
	}
}

func TestMarkdownRenderAndWrite(t *testing.T) {
	out := render(t, Markdown, sampleReport())
	for _, want := range []string{"# Schedule FA — calendar year 2024", "Table A3", "Audit trail", "Reconciliation", "Alpha, Inc"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q", want)
		}
	}

	dir := t.TempDir()
	paths, err := Write(dir, []Format{Markdown, CSV, JSON}, sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("wrote %d files, want 3", len(paths))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing output %s: %v", p, err)
		}
	}
	if filepath.Base(paths[0]) != "report.md" {
		t.Errorf("first file = %s, want report.md", filepath.Base(paths[0]))
	}
}

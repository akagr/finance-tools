package report

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/akagr/finance-tools/schedule-fa/internal/fsi"
	"github.com/akagr/finance-tools/schedule-fa/internal/gains"
)

func sampleFSIReport() *fsi.Report {
	rat := func(n int64) *big.Rat { return big.NewRat(n, 1) }
	head := func(h fsi.Head, inc, paid, payable, relief int64, article string) fsi.HeadRow {
		return fsi.HeadRow{
			Head: h, Income: rat(inc), TaxPaidOutside: rat(paid),
			TaxPayableIndia: rat(payable), Relief: rat(relief), DTAAArticle: article,
		}
	}
	return &fsi.Report{
		FYStart: 2025,
		From:    time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC),
		Method:  gains.PerLeg,
		Countries: []fsi.CountryRow{{
			CountryName: "United States of America", CountryCode: "2", TIN: "XXXXX1234X",
			Heads: []fsi.HeadRow{
				head(fsi.HeadSalary, 0, 0, 0, 0, ""),
				head(fsi.HeadHouseProperty, 0, 0, 0, 0, ""),
				head(fsi.HeadCapitalGains, 5000, 0, 625, 0, ""),
				head(fsi.HeadOtherSources, 1000, 250, 312, 250, "10"),
			},
			Total:       head("Total", 6000, 250, 937, 250, ""),
			NeedsReview: true, ReviewNote: "a note that must reach the page",
		}},
		TR: []fsi.TRRow{{
			CountryName: "United States of America", CountryCode: "2", TIN: "XXXXX1234X",
			TaxPaidOutside: rat(250), ReliefAvailable: rat(250), Section: "90",
		}},
		TRTaxPaid: rat(250), TRRelief: rat(250), TRDTAA: rat(250),
		Gains: &gains.Summary{
			STCG: new(big.Rat), LTCG: new(big.Rat), LTCGPreCut: new(big.Rat),
		},
		TieOut: fsi.TieOut{
			ScheduleCGA5: new(big.Rat), ScheduleCGB8: rat(5000),
			ScheduleCGB8PreCut: new(big.Rat), ScheduleOS: rat(1000),
		},
		Assumptions: []string{"a stated assumption"},
		ReviewNotes: []string{"something to check before filing"},
	}
}

// The printable page must carry the numbers and the warnings, not just the
// chrome — a report that quietly drops a review note is worse than none.
func TestFSIHTMLCarriesFiguresAndWarnings(t *testing.T) {
	rnd, err := FSIFor(HTML)
	if err != nil {
		t.Fatalf("FSIFor(html): %v", err)
	}
	var buf bytes.Buffer
	if err := rnd.RenderFSI(&buf, sampleFSIReport()); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Schedule FSI", "FY 2025-26", "AY 2026-27",
		"United States of America", "XXXXX1234X",
		"5000.00", "250.00", // an income figure and the relief
		"Schedule TR", "Form 67", "Tie-out",
		"a note that must reach the page",
		"a stated assumption",
		"something to check before filing",
		"per-leg",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	// Template mishaps are silent in HTML, so assert they did not happen.
	for _, bad := range []string{"<no value>", "ZgotmplZ", "%!"} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered page contains template error marker %q", bad)
		}
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("page should be a complete self-contained document")
	}
	if !strings.Contains(out, "@media print") {
		t.Error("page should keep the print styling that makes Print → Save as PDF usable")
	}
	// The pre-23-Jul row is noise when it is zero, exactly as in the Markdown.
	if strings.Contains(out, "before 23 Jul 2024") {
		t.Error("the pre-rate-cut tie-out row should be hidden when zero")
	}
}

// html is now a valid Schedule FSI format; a bogus one still must not be.
func TestFSIFormats(t *testing.T) {
	for _, f := range []Format{Markdown, CSV, JSON, HTML} {
		if _, err := FSIFor(f); err != nil {
			t.Errorf("FSIFor(%s) = %v, want a renderer", f, err)
		}
	}
	if _, err := FSIFor(Format("pdf")); err == nil {
		t.Error("FSIFor(pdf) should be rejected")
	}
}

package pipeline

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/akagr/finance-tools/schedule-fa/internal/entities"
	"github.com/akagr/finance-tools/schedule-fa/internal/fsi"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/gains"
	"github.com/akagr/finance-tools/schedule-fa/internal/ibkr"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
	"github.com/akagr/finance-tools/schedule-fa/internal/report"
)

// buildFSIFixture parses the synthetic FY 2025-26 statement and builds Schedule
// FSI from it with fixed assumptions, so the output is deterministic.
func buildFSIFixture(t *testing.T, method gains.Method) *fsi.Report {
	t.Helper()
	st, err := ibkr.ParseFlexFilePeriod("../ibkr/testdata/sample_flex_fy.xml", ibkr.FinancialYear(2025))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := fx.NewCSVStore()
	if err := store.LoadRateKeeperFile(model.USD, "testdata/ttbr-fy2025-26/SBI_REFERENCE_RATES_USD.csv"); err != nil {
		t.Fatalf("rates: %v", err)
	}
	ents, err := entities.Load("testdata/entities.csv")
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	rep, err := BuildFSI(st, store, fsi.Options{
		FYStart:      2025,
		TIN:          "XXXXX1234X",
		MarginalRate: big.NewRat(30, 1),
		Surcharge:    new(big.Rat),
		Cess:         big.NewRat(4, 1),
		CGMethod:     method,
		Entities:     ents,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return rep
}

// TestGoldenFSIReport locks the whole offline income path: parse a Flex XML over
// the FINANCIAL year, convert under Rule 115, classify gains, assemble Schedules
// FSI and TR, and render.
func TestGoldenFSIReport(t *testing.T) {
	rep := buildFSIFixture(t, gains.PerLeg)

	for _, f := range []report.Format{report.CSV, report.JSON, report.Markdown, report.HTML} {
		rnd, err := report.FSIFor(f)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := rnd.RenderFSI(&buf, rep); err != nil {
			t.Fatalf("render %s: %v", f, err)
		}
		assertGolden(t, "report-fsi."+string(f), buf.Bytes())
	}

	var buf bytes.Buffer
	if err := report.RenderITD(&buf, rep); err != nil {
		t.Fatalf("render ITD fragment: %v", err)
	}
	assertGolden(t, "schedule-fsi.json", buf.Bytes())
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The financial year is the single most common thing to get wrong when a tool
// also does the calendar-year Schedule FA, so --fy is strict about it.
func TestParseFY(t *testing.T) {
	ok := []struct {
		in   string
		want int
	}{
		{"2025-26", 2025},
		{"2025-2026", 2025},
		{"2025", 2025},
		{" 2024-25 ", 2024},
		{"2099-00", 2099},
	}
	for _, c := range ok {
		got, err := parseFY(c.in)
		if err != nil {
			t.Errorf("parseFY(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseFY(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	bad := []string{"", "not-a-year", "1999-00", "2100-01", "2025-27", "2025-2027"}
	for _, in := range bad {
		if _, err := parseFY(in); err == nil {
			t.Errorf("parseFY(%q) should have been rejected", in)
		}
	}
}

func TestCmdFSIFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"missing fy", []string{"--statement", "x.xml"}, 2},
		{"fy spanning two years", []string{"--fy", "2025-27", "--statement", "x.xml"}, 2},
		{"no source", []string{"--fy", "2025-26"}, 2},
		{"online missing query", []string{"--fy", "2025-26", "--flex-token", "T"}, 2},
		{"unknown cg method", []string{"--fy", "2025-26", "--statement", "x.xml", "--cg-fx", "spot"}, 2},
		{"negative marginal rate", []string{"--fy", "2025-26", "--statement", "x.xml", "--marginal-rate", "-5"}, 2},
		{"html not supported", []string{"--fy", "2025-26", "--statement", "x.xml", "--format", "html"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer silence(t)()
			if got := cmdFSI(tc.args); got != tc.want {
				t.Errorf("cmdFSI(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// End to end through the real CLI entry point: parse the synthetic FY statement,
// build the schedules, and write every artifact.
func TestCmdFSIWritesReports(t *testing.T) {
	defer silence(t)()
	out := t.TempDir()
	args := []string{
		"--fy", "2025-26",
		"--statement", "../../internal/ibkr/testdata/sample_flex_fy.xml",
		"--rates", "../../internal/pipeline/testdata/ttbr-fy2025-26",
		"--entities", "../../internal/pipeline/testdata/entities.csv",
		"--tin", "XXXXX1234X",
		"--out", out,
		"--format", "md,csv,json",
	}
	if got := cmdFSI(args); got != 0 {
		t.Fatalf("cmdFSI = %d, want 0", got)
	}
	for _, name := range []string{"report-fsi.md", "report-fsi.csv", "report-fsi.json", "schedule-fsi.json"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Errorf("%s not written: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

// Trades with no closed-lot detail cannot yield capital gains, so the run must
// still succeed but say so loudly rather than silently reporting nil gains.
func TestCmdFSIWithoutClosedLots(t *testing.T) {
	defer silence(t)()
	out := t.TempDir()
	args := []string{
		"--fy", "2024-25",
		"--statement", "../../internal/ibkr/testdata/sample_flex.xml",
		"--rates", "../../internal/fx/testdata/SBI_REFERENCE_RATES_USD.csv",
		"--entities", "../../internal/pipeline/testdata/entities.csv",
		"--out", out,
		"--format", "md",
	}
	if got := cmdFSI(args); got != 0 {
		t.Fatalf("cmdFSI = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(out, "report-fsi.md")); err != nil {
		t.Errorf("report not written: %v", err)
	}
}

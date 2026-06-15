package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFormats(t *testing.T) {
	t.Run("all formats", func(t *testing.T) {
		got, err := parseFormats("md,csv,json,html")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 {
			t.Errorf("got %d formats, want 4: %v", len(got), got)
		}
	})

	t.Run("trims spaces and skips empties", func(t *testing.T) {
		got, err := parseFormats(" md , , html ")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || string(got[0]) != "md" || string(got[1]) != "html" {
			t.Errorf("got %v, want [md html]", got)
		}
	})

	t.Run("unknown format errors", func(t *testing.T) {
		if _, err := parseFormats("md,pdf"); err == nil {
			t.Error("expected error for unknown format pdf")
		}
	})

	t.Run("empty errors", func(t *testing.T) {
		if _, err := parseFormats("   "); err == nil {
			t.Error("expected error for empty format list")
		}
	})
}

// silence redirects stdout/stderr to /dev/null for the duration of a call, so
// the generator's progress output doesn't clutter test logs.
func silence(t *testing.T) func() {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	return func() {
		os.Stdout, os.Stderr = oldOut, oldErr
		null.Close()
	}
}

func TestCmdGenerateFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"missing year", []string{"--statement", "x.xml"}, 2},
		{"year too low", []string{"--year", "1999", "--statement", "x.xml"}, 2},
		{"year too high", []string{"--year", "2100", "--statement", "x.xml"}, 2},
		{"no source", []string{"--year", "2026"}, 2},
		{"online missing query", []string{"--year", "2026", "--flex-token", "T"}, 2},
		{"online missing token", []string{"--year", "2026", "--flex-query", "123"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer silence(t)()
			if got := cmdGenerate(tc.args); got != tc.want {
				t.Errorf("cmdGenerate(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestCmdGenerateMissingStatementFile(t *testing.T) {
	defer silence(t)()
	// Valid flags but the statement file does not exist → ingest fails → exit 1.
	args := []string{"--year", "2024", "--statement", filepath.Join(t.TempDir(), "nope.xml")}
	if got := cmdGenerate(args); got != 1 {
		t.Errorf("cmdGenerate with missing file = %d, want 1", got)
	}
}

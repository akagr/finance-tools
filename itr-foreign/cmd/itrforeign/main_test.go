package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akagr/finance-tools/itr-foreign/internal/yahoo"
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

func TestParsePriceTickers(t *testing.T) {
	in := `# a comment
IBKR  IBKR    US45841N1072  USD

VWRA  VWRA.L  IE00BK5BQT80
BAD  onlytwo
`
	got, err := parsePriceTickers(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []priceTicker{
		{Symbol: "IBKR", Yahoo: "IBKR", ISIN: "US45841N1072", Currency: "USD"},
		{Symbol: "VWRA", Yahoo: "VWRA.L", ISIN: "IE00BK5BQT80", Currency: "USD"}, // default currency
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tickers, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ticker %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// fakeFetcher returns canned bars per Yahoo symbol (or an error).
type fakeFetcher struct {
	bars map[string][]yahoo.Bar
	errs map[string]error
}

func (f fakeFetcher) Chart(_ context.Context, symbol string, _, _ time.Time) ([]yahoo.Bar, error) {
	if err, ok := f.errs[symbol]; ok {
		return nil, err
	}
	return f.bars[symbol], nil
}

func TestFetchPricesTo(t *testing.T) {
	tickers := []priceTicker{
		{Symbol: "IBKR", Yahoo: "IBKR", ISIN: "US45841N1072", Currency: "USD"},
		{Symbol: "VWRA", Yahoo: "VWRA.L", ISIN: "IE00BK5BQT80", Currency: "USD"},
	}
	f := fakeFetcher{bars: map[string][]yahoo.Bar{
		"IBKR":   {{Date: "2026-01-02", Close: 150}, {Date: "2026-01-03", Close: 151.5}},
		"VWRA.L": {{Date: "2026-01-02", Close: 120.25}},
	}}

	defer silence(t)()
	var buf bytes.Buffer
	n, err := fetchPricesTo(context.Background(), f, &buf, tickers, 2026, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
	want := "date,symbol,isin,close,currency\n" +
		"2026-01-02,IBKR,US45841N1072,150.0000,USD\n" +
		"2026-01-03,IBKR,US45841N1072,151.5000,USD\n" +
		"2026-01-02,VWRA,IE00BK5BQT80,120.2500,USD\n"
	if buf.String() != want {
		t.Errorf("CSV mismatch:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestFetchPricesToSkipsFailures(t *testing.T) {
	tickers := []priceTicker{
		{Symbol: "OK", Yahoo: "OK", ISIN: "I1", Currency: "USD"},
		{Symbol: "BAD", Yahoo: "BAD", ISIN: "I2", Currency: "USD"},
	}
	f := fakeFetcher{
		bars: map[string][]yahoo.Bar{"OK": {{Date: "2026-06-01", Close: 10}}},
		errs: map[string]error{"BAD": context.DeadlineExceeded},
	}

	defer silence(t)()
	var buf bytes.Buffer
	n, err := fetchPricesTo(context.Background(), f, &buf, tickers, 2026, 0)
	if err != nil {
		t.Fatalf("a per-ticker failure should not be fatal: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 (BAD skipped)", n)
	}
	if !strings.Contains(buf.String(), "2026-06-01,OK,I1,10.0000,USD") {
		t.Errorf("missing OK row: %q", buf.String())
	}
}

func TestCmdFetchPricesFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"missing year", []string{}, 2},
		{"year too low", []string{"--year", "1999"}, 2},
		{"year too high", []string{"--year", "2100"}, 2},
		{"missing tickers file", []string{"--year", "2026", "--tickers", filepath.Join(t.TempDir(), "nope.txt")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer silence(t)()
			if got := cmdFetchPrices(tc.args); got != tc.want {
				t.Errorf("cmdFetchPrices(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

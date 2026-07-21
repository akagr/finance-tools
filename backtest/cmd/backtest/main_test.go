package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/akagr/finance-tools/backtest/internal/yahoo"
)

func TestParseFetchTickers(t *testing.T) {
	in := strings.NewReader("# comment\n\nNIFTY50 ^NSEI\nNIFTYBEES NIFTYBEES.NS\nbad-line\n")
	got, err := parseFetchTickers(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("tickers = %d, want 2 (comments/blank/malformed skipped)", len(got))
	}
	if got[0] != (fetchTicker{Label: "NIFTY50", Yahoo: "^NSEI"}) {
		t.Errorf("first ticker = %#v", got[0])
	}
}

func TestParseRange(t *testing.T) {
	if _, _, code := parseRange("", "2024-01-01"); code == 0 {
		t.Error("expected non-zero code for missing start")
	}
	if _, _, code := parseRange("2024-01-02", "2024-01-01"); code == 0 {
		t.Error("expected non-zero code for end before start")
	}
	s, e, code := parseRange("2024-01-01", "2024-12-31")
	if code != 0 {
		t.Fatalf("valid range rejected: code=%d", code)
	}
	if !s.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) || !e.Equal(time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parsed range = %v..%v", s, e)
	}
}

// fakeFetcher returns canned bars, so the fetch loop is tested offline.
type fakeFetcher struct{ bars map[string][]yahoo.Bar }

func (f fakeFetcher) Chart(_ context.Context, symbol string, _, _ time.Time) ([]yahoo.Bar, error) {
	return f.bars[symbol], nil
}

func TestFetchPricesTo(t *testing.T) {
	f := fakeFetcher{bars: map[string][]yahoo.Bar{
		"^NSEI": {{Date: "2024-01-01", Close: 21000.5}, {Date: "2024-01-02", Close: 21100.25}},
	}}
	var buf bytes.Buffer
	tickers := []fetchTicker{{Label: "NIFTY50", Yahoo: "^NSEI"}}
	if err := fetchPricesTo(context.Background(), f, &buf, tickers, time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	wantHeader := "date,symbol,close\n"
	if !strings.HasPrefix(got, wantHeader) {
		t.Errorf("missing header; got:\n%s", got)
	}
	if !strings.Contains(got, "2024-01-01,NIFTY50,21000.5000") {
		t.Errorf("missing expected row; got:\n%s", got)
	}
}

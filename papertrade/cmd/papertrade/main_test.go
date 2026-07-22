package main

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/store"
)

func TestStrategyFlagsConfigMapping(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sf := registerStrategyFlags(fs)
	if err := fs.Parse([]string{
		"--strategy", "ema-cross", "--fast", "12", "--slow", "48",
		"--vol-target", "15", "--vol-lookback", "30",
		"--stt-bps", "8", "--slippage-bps", "3",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := sf.config()
	if cfg.Name != "ema-cross" || cfg.Fast != 12 || cfg.Slow != 48 {
		t.Errorf("config mapping wrong: %+v", cfg)
	}
	if cfg.VolTarget != 0.15 { // percent flag → fraction
		t.Errorf("vol target = %v, want 0.15", cfg.VolTarget)
	}
	if cfg.VolLookback != 30 {
		t.Errorf("vol lookback = %v, want 30", cfg.VolLookback)
	}
	costs := sf.costs()
	if costs.STTBps != 8 || costs.SlippageBps != 3 {
		t.Errorf("costs mapping wrong: %+v", costs)
	}
}

func TestOrNone(t *testing.T) {
	if orNone("") != "(none yet)" {
		t.Errorf("orNone(empty) = %q", orNone(""))
	}
	if orNone("2024-01-01") != "2024-01-01" {
		t.Errorf("orNone(date) = %q", orNone("2024-01-01"))
	}
}

func TestTrunc(t *testing.T) {
	if trunc("short", 16) != "short" {
		t.Errorf("trunc kept-short wrong: %q", trunc("short", 16))
	}
	if got := trunc("abcdefghij", 5); got != "abcd…" {
		t.Errorf("trunc = %q, want abcd…", got)
	}
}

func TestScanAccounts(t *testing.T) {
	root := t.TempDir()
	mk := func(name, strat string, initial, equity float64, lastBar string, fills int) {
		st := store.New(filepath.Join(root, name))
		a := &account.Account{
			Name: name, Symbol: "NIFTY50", YahooSymbol: "^NSEI",
			Strategy: account.StrategyConfig{Name: strat}, InitialCapital: initial,
			LastBarDate: lastBar,
		}
		if err := st.Save(a); err != nil {
			t.Fatal(err)
		}
		if equity > 0 {
			if err := st.AppendEquity(store.EquitySnapshot{Date: lastBar, Equity: equity}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < fills; i++ {
			if err := st.AppendLog(store.LogEntry{Date: lastBar}); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("b-acct", "ema-cross", 100000, 110000, "2024-12-31", 3)
	mk("a-acct", "sma-cross", 100000, 95000, "2024-12-30", 1)
	// A non-account directory must be ignored.
	if err := os.MkdirAll(filepath.Join(root, "not-an-account"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := scanAccounts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("accounts = %d, want 2", len(got))
	}
	// Sorted by dir name: a-acct before b-acct.
	if got[0].Dir != "a-acct" || got[1].Dir != "b-acct" {
		t.Errorf("not sorted by dir: %v, %v", got[0].Dir, got[1].Dir)
	}
	if !got[1].HasEquity || got[1].Equity != 110000 {
		t.Errorf("b-acct equity wrong: %+v", got[1])
	}
	if math.Abs(got[1].Return-0.10) > 1e-9 {
		t.Errorf("b-acct return = %v, want 0.10", got[1].Return)
	}
	if got[0].Fills != 1 || got[1].Fills != 3 {
		t.Errorf("fills wrong: %d, %d", got[0].Fills, got[1].Fills)
	}
}

func TestScanAccountsEmptyRoot(t *testing.T) {
	got, err := scanAccounts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no accounts, got %d", len(got))
	}
}

package store

import (
	"testing"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/broker"
	"github.com/akagr/finance-tools/papertrade/internal/portfolio"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	if st.Exists() {
		t.Fatal("fresh dir should not have an account")
	}
	a := &account.Account{
		Name:           "acc",
		Symbol:         "NIFTY50",
		YahooSymbol:    "^NSEI",
		Strategy:       account.StrategyConfig{Name: "sma-cross", Fast: 50, Slow: 200},
		Costs:          broker.DefaultCosts(),
		InitialCapital: 100000,
		Portfolio:      portfolio.New(100000),
		LastBarDate:    "2024-12-31",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
	}
	if err := st.Save(a); err != nil {
		t.Fatal(err)
	}
	if !st.Exists() {
		t.Fatal("account should exist after save")
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != a.Name || got.Symbol != a.Symbol || got.Strategy.Fast != 50 || got.LastBarDate != "2024-12-31" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestAppendAndReadLog(t *testing.T) {
	st := New(t.TempDir())
	for i := 0; i < 3; i++ {
		if err := st.AppendLog(LogEntry{
			Date: "2024-01-0" + string(rune('1'+i)),
			Fill: broker.Fill{Side: broker.Buy, Shares: float64(i + 1)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := st.ReadLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[2].Fill.Shares != 3 {
		t.Errorf("last entry shares = %v, want 3", entries[2].Fill.Shares)
	}
}

func TestLoadMissingErrors(t *testing.T) {
	st := New(t.TempDir())
	if _, err := st.Load(); err == nil {
		t.Error("expected error loading a missing account")
	}
}

func TestAppendAndReadEquity(t *testing.T) {
	st := New(t.TempDir())
	for i := 0; i < 3; i++ {
		if err := st.AppendEquity(EquitySnapshot{
			Date:   "2024-01-0" + string(rune('1'+i)),
			Equity: float64(100000 + i*1000),
		}); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := st.ReadEquity()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(snaps))
	}
	if snaps[2].Equity != 102000 {
		t.Errorf("last equity = %v, want 102000", snaps[2].Equity)
	}
}

package session

import (
	"testing"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/broker"
	"github.com/akagr/finance-tools/papertrade/internal/portfolio"
	"github.com/akagr/finance-tools/papertrade/internal/store"
)

func newAccount() *account.Account {
	return &account.Account{
		Name:           "t",
		Symbol:         "T",
		YahooSymbol:    "T",
		Strategy:       account.StrategyConfig{Name: "sma-cross", Fast: 2, Slow: 4},
		Costs:          broker.Costs{STTBps: 10, SlippageBps: 5},
		InitialCapital: 100000,
		Portfolio:      portfolio.New(100000),
	}
}

// bars builds ascending Bars from closes with sequential dates.
func bars(closes ...float64) []Bar {
	out := make([]Bar, len(closes))
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = Bar{Date: d.Format("2006-01-02"), Close: c}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

func TestStepEntersOnUptrend(t *testing.T) {
	a := newAccount()
	// Rising series → fast SMA above slow → target 1.0 → buy in.
	b := bars(10, 11, 12, 13, 14)
	res, err := Step(a, b, false, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Traded || res.Fill.Side != broker.Buy {
		t.Fatalf("expected a buy, got %+v", res)
	}
	if a.Portfolio.Shares <= 0 {
		t.Errorf("expected a long position, shares=%v", a.Portfolio.Shares)
	}
	if a.LastBarDate != b[len(b)-1].Date {
		t.Errorf("last bar date = %q, want %q", a.LastBarDate, b[len(b)-1].Date)
	}
}

func TestStepIdempotentPerBar(t *testing.T) {
	a := newAccount()
	b := bars(10, 11, 12, 13, 14)
	if _, err := Step(a, b, false, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	sharesAfterFirst := a.Portfolio.Shares
	// Same latest bar → skipped, no change.
	res, err := Step(a, b, false, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Errorf("second step on same bar should skip, got %+v", res)
	}
	if a.Portfolio.Shares != sharesAfterFirst {
		t.Errorf("shares changed on skipped step")
	}
}

func TestStepForceReprocesses(t *testing.T) {
	a := newAccount()
	b := bars(10, 11, 12, 13, 14)
	if _, err := Step(a, b, false, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	res, err := Step(a, b, true, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Errorf("force step should not skip")
	}
}

func TestStepExitsOnDowntrend(t *testing.T) {
	a := newAccount()
	// First enter on an uptrend.
	up := bars(10, 11, 12, 13, 14)
	if _, err := Step(a, up, false, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if a.Portfolio.Shares == 0 {
		t.Fatal("expected to be long before the downturn")
	}
	// Then a downtrend flips the signal to flat → sell out.
	down := bars(10, 11, 12, 13, 14, 13, 11, 9, 7)
	res, err := Step(a, down, false, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Traded || res.Fill.Side != broker.Sell {
		t.Fatalf("expected a sell, got %+v", res)
	}
	if a.Portfolio.Shares != 0 {
		t.Errorf("expected flat after exit, shares=%v", a.Portfolio.Shares)
	}
}

func TestStepWritesLog(t *testing.T) {
	a := newAccount()
	st := store.New(t.TempDir())
	b := bars(10, 11, 12, 13, 14)
	if _, err := Step(a, b, false, st, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if entries[0].Fill.Side != broker.Buy || entries[0].SharesAfter <= 0 {
		t.Errorf("unexpected log entry %+v", entries[0])
	}
}

func TestStepBuyNeverGoesNegativeCash(t *testing.T) {
	// buy-hold targets 100%; with costs, a naive buy would overspend. The
	// affordability cap must keep cash non-negative.
	a := newAccount()
	a.Strategy = account.StrategyConfig{Name: "buy-hold"}
	a.Costs = broker.Costs{STTBps: 10, SlippageBps: 50} // exaggerated friction
	b := bars(100, 101, 102, 103, 104)
	if _, err := Step(a, b, false, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if a.Portfolio.Cash < -1e-6 {
		t.Errorf("cash went negative: %v", a.Portfolio.Cash)
	}
	if a.Portfolio.Shares <= 0 {
		t.Errorf("expected a long position, shares=%v", a.Portfolio.Shares)
	}
}

func TestStepLogsEquityEvenWithoutTrade(t *testing.T) {
	// A strategy that stays flat (slow window longer than history) never trades,
	// but each processed bar must still record an equity snapshot.
	a := newAccount()
	a.Strategy = account.StrategyConfig{Name: "sma-cross", Fast: 2, Slow: 100}
	st := store.New(t.TempDir())
	b := bars(10, 11, 12, 13, 14)
	res, err := Step(a, b, false, st, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Traded {
		t.Fatal("expected no trade for a never-invested strategy")
	}
	snaps, err := st.ReadEquity()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("equity snapshots = %d, want 1", len(snaps))
	}
	if snaps[0].Equity != 100000 || snaps[0].Date != b[len(b)-1].Date {
		t.Errorf("unexpected snapshot %+v", snaps[0])
	}
	// Fills log stays empty.
	fills, _ := st.ReadLog()
	if len(fills) != 0 {
		t.Errorf("expected no fills, got %d", len(fills))
	}
}

func TestStepRejectsTooFewBars(t *testing.T) {
	a := newAccount()
	if _, err := Step(a, bars(10), false, nil, time.Now()); err == nil {
		t.Error("expected error for <2 bars")
	}
}

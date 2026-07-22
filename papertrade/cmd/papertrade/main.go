// Command papertrade runs a trading strategy forward in time on real market data
// with *simulated* fills, so you can validate a strategy's live decision-making
// and the data pipeline before risking any capital. State persists between runs
// under a per-account directory; run `step` once per trading day (by hand or
// from cron) to advance the paper account.
//
// This is a research tool, not a trading system, and places no real orders.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/broker"
	"github.com/akagr/finance-tools/papertrade/internal/perf"
	"github.com/akagr/finance-tools/papertrade/internal/portfolio"
	"github.com/akagr/finance-tools/papertrade/internal/session"
	"github.com/akagr/finance-tools/papertrade/internal/store"
	"github.com/akagr/finance-tools/papertrade/internal/yahoo"
)

const version = "0.1.0"

const disclaimer = "NOTE: paper trading only — no real orders are placed. Not investment advice."

// lookbackDays is how much history to fetch each step so indicators are warm.
const lookbackDays = 500

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		os.Exit(cmdInit(os.Args[2:]))
	case "step":
		os.Exit(cmdStep(os.Args[2:]))
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "summary":
		os.Exit(cmdSummary(os.Args[2:]))
	case "history":
		os.Exit(cmdHistory(os.Args[2:]))
	case "list", "ls":
		os.Exit(cmdList(os.Args[2:]))
	case "version":
		fmt.Println("papertrade " + version)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `papertrade — run a strategy forward on live data with simulated fills, before risking capital

Usage:
  papertrade init  --dir <dir> --symbol <label> --yahoo <sym> [--strategy <name>] [strategy flags]
  papertrade step  --dir <dir> [--force]
  papertrade status --dir <dir>
  papertrade summary --dir <dir>
  papertrade history --dir <dir>
  papertrade list --root <dir>
  papertrade version

Run "papertrade init -h" or "papertrade step -h" for flags.

%s
`, disclaimer)
}

// strategyFlags registers the shared strategy/cost flags on a flag set.
type strategyFlags struct {
	strategy                 *string
	fast, slow, lookback     *int
	rsiPeriod                *int
	rsiThresh                *float64
	entry, exit              *int
	brokBps, sttBps, slipBps *float64
	volTarget                *float64
	volLook                  *int
}

func registerStrategyFlags(fs *flag.FlagSet) strategyFlags {
	return strategyFlags{
		strategy:  fs.String("strategy", "sma-cross", "strategy: sma-cross|ema-cross|momentum|rsi|donchian|buy-hold"),
		fast:      fs.Int("fast", 20, "fast MA window (sma-cross, ema-cross)"),
		slow:      fs.Int("slow", 50, "slow MA window (sma-cross, ema-cross)"),
		lookback:  fs.Int("lookback", 120, "lookback window in bars (momentum)"),
		rsiPeriod: fs.Int("rsi-period", 14, "RSI period (rsi)"),
		rsiThresh: fs.Float64("rsi-threshold", 30, "buy when RSI is below this (rsi)"),
		entry:     fs.Int("entry", 20, "breakout entry window in bars (donchian)"),
		exit:      fs.Int("exit", 10, "breakdown exit window in bars (donchian)"),
		brokBps:   fs.Float64("brokerage-bps", 0, "brokerage per trade, basis points"),
		sttBps:    fs.Float64("stt-bps", 10, "securities transaction tax per trade, basis points"),
		slipBps:   fs.Float64("slippage-bps", 5, "assumed slippage per trade, basis points"),
		volTarget: fs.Float64("vol-target", 0, "annualised volatility target in percent (e.g. 10); 0 disables sizing"),
		volLook:   fs.Int("vol-lookback", 20, "trailing bars used to estimate realised volatility (--vol-target)"),
	}
}

func (sf strategyFlags) config() account.StrategyConfig {
	return account.StrategyConfig{
		Name:          *sf.strategy,
		Fast:          *sf.fast,
		Slow:          *sf.slow,
		Lookback:      *sf.lookback,
		RSIPeriod:     *sf.rsiPeriod,
		RSIThreshold:  *sf.rsiThresh,
		DonchianEntry: *sf.entry,
		DonchianExit:  *sf.exit,
		VolTarget:     *sf.volTarget / 100,
		VolLookback:   *sf.volLook,
	}
}

func (sf strategyFlags) costs() broker.Costs {
	return broker.Costs{BrokerageBps: *sf.brokBps, STTBps: *sf.sttBps, SlippageBps: *sf.slipBps}
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var (
		dir     = fs.String("dir", "", "account directory (created if missing)")
		symbol  = fs.String("symbol", "", "label for the traded instrument (e.g. NIFTY50)")
		yahoo_  = fs.String("yahoo", "", "Yahoo Finance symbol to fetch (e.g. ^NSEI)")
		capital = fs.Float64("capital", 100000, "starting paper capital in INR")
		force   = fs.Bool("force", false, "overwrite an existing account in --dir")
	)
	sf := registerStrategyFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *symbol == "" || *yahoo_ == "" {
		fmt.Fprintln(os.Stderr, "error: --dir, --symbol and --yahoo are required")
		return 2
	}
	if *capital <= 0 {
		fmt.Fprintln(os.Stderr, "error: --capital must be > 0")
		return 2
	}

	st := store.New(*dir)
	if st.Exists() && !*force {
		fmt.Fprintf(os.Stderr, "error: an account already exists in %s (use --force to overwrite)\n", *dir)
		return 1
	}

	now := time.Now().UTC()
	a := &account.Account{
		Name:           *symbol,
		Symbol:         *symbol,
		YahooSymbol:    *yahoo_,
		Strategy:       sf.config(),
		Costs:          sf.costs(),
		InitialCapital: *capital,
		Portfolio:      portfolio.New(*capital),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// Validate the strategy config early.
	if _, err := a.BuildStrategy(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := st.Save(a); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("Initialised paper account %q in %s: %s on %s, ₹%.2f capital.\n",
		a.Name, *dir, a.Strategy.Name, a.Symbol, a.InitialCapital)
	fmt.Println("Run `papertrade step --dir " + *dir + "` once per trading day to advance it.")
	return 0
}

func cmdStep(args []string) int {
	fs := flag.NewFlagSet("step", flag.ExitOnError)
	var (
		dir   = fs.String("dir", "", "account directory")
		force = fs.Bool("force", false, "act even if the latest bar was already processed")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --dir is required")
		return 2
	}
	st := store.New(*dir)
	a, err := st.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	barsIn, err := yahoo.NewClient().Chart(ctx, a.YahooSymbol, start, end)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: fetching prices:", err)
		return 1
	}
	if len(barsIn) < 2 {
		fmt.Fprintf(os.Stderr, "error: not enough price history for %q (got %d bars)\n", a.YahooSymbol, len(barsIn))
		return 1
	}
	bars := make([]session.Bar, len(barsIn))
	for i, b := range barsIn {
		bars[i] = session.Bar{Date: b.Date, Close: b.Close}
	}

	res, err := session.Step(a, bars, *force, st, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := st.Save(a); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	switch {
	case res.Skipped:
		fmt.Printf("No new bar (latest %s already processed). Nothing to do.\n", res.Date)
	case res.Traded:
		fmt.Printf("%s: %s %.4f @ ₹%.2f (target weight %.0f%%). Equity ₹%.2f.\n",
			res.Date, res.Fill.Side, res.Fill.Shares, res.Fill.Price, res.TargetWt*100, res.EquityAfter)
	default:
		fmt.Printf("%s: no trade (already within %.0f%% of target weight). Equity ₹%.2f.\n",
			res.Date, res.TargetWt*100, res.EquityAfter)
	}
	return 0
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dir := fs.String("dir", "", "account directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --dir is required")
		return 2
	}
	st := store.New(*dir)
	a, err := st.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	// Mark to market at the latest available quote when we hold a position.
	p := a.Portfolio
	quote, quoted := latestQuote(a.YahooSymbol)
	fmt.Printf("Paper account: %s (%s on %s)\n", a.Name, a.Strategy.Name, a.Symbol)
	fmt.Printf("  Last bar acted on : %s\n", orNone(a.LastBarDate))
	fmt.Printf("  Cash              : ₹%.2f\n", p.Cash)
	fmt.Printf("  Shares            : %.4f", p.Shares)
	if p.Shares > 0 {
		fmt.Printf(" (avg cost ₹%.2f)", p.AvgCost())
	}
	fmt.Println()
	fmt.Printf("  Realised P&L      : ₹%.2f\n", p.Realised)
	if quoted {
		eq := p.Equity(quote)
		ret := 0.0
		if a.InitialCapital > 0 {
			ret = eq/a.InitialCapital - 1
		}
		fmt.Printf("  Last quote        : ₹%.2f\n", quote)
		if p.Shares > 0 {
			fmt.Printf("  Unrealised P&L    : ₹%.2f\n", p.Unrealised(quote))
		}
		fmt.Printf("  Equity (marked)   : ₹%.2f\n", eq)
		fmt.Printf("  Total return      : %.2f%%\n", ret*100)
	} else {
		fmt.Println("  (live quote unavailable; showing cash and realised P&L only)")
	}
	fmt.Printf("  Initial capital   : ₹%.2f\n", a.InitialCapital)
	fmt.Println()
	fmt.Println(disclaimer)
	return 0
}

// latestQuote fetches the most recent close for a symbol; quoted is false if the
// feed is unavailable, so status still works offline.
func latestQuote(symbol string) (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -10)
	bars, err := yahoo.NewClient().Chart(ctx, symbol, start, end)
	if err != nil || len(bars) == 0 {
		return 0, false
	}
	return bars[len(bars)-1].Close, true
}

func cmdSummary(args []string) int {
	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	dir := fs.String("dir", "", "account directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --dir is required")
		return 2
	}
	st := store.New(*dir)
	a, err := st.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	snaps, err := st.ReadEquity()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	su, ok := perf.Summarize(snaps)
	if !ok {
		fmt.Printf("Not enough history yet for %s: run `step` on at least two distinct trading days.\n", a.Name)
		return 0
	}

	edge := su.TotalReturn - su.BenchReturn
	fmt.Printf("Paper performance: %s (%s on %s)\n", a.Name, a.Strategy.Name, a.Symbol)
	fmt.Printf("  Period            : %s → %s (%d daily snapshots)\n", su.Start, su.End, su.Snapshots)
	fmt.Printf("  Equity            : ₹%.2f → ₹%.2f\n", su.StartEquity, su.EndEquity)
	fmt.Printf("  Total return      : %.2f%%\n", su.TotalReturn*100)
	fmt.Printf("  CAGR              : %.2f%%\n", su.CAGR*100)
	fmt.Printf("  Annualised vol    : %.2f%%\n", su.AnnVol*100)
	fmt.Printf("  Sharpe            : %.2f\n", su.Sharpe)
	fmt.Printf("  Max drawdown      : %.2f%%\n", su.MaxDrawdown*100)
	fmt.Println()
	fmt.Printf("  Buy & hold return : %.2f%% (max drawdown %.2f%%)\n", su.BenchReturn*100, su.BenchMaxDD*100)
	fmt.Printf("  Edge vs buy & hold: %+.2f%%\n", edge*100)
	fmt.Println()
	fmt.Println("Tracking starts at the first `step`, so early numbers are noisy — let it run for weeks.")
	fmt.Println(disclaimer)
	return 0
}

func cmdHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	dir := fs.String("dir", "", "account directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --dir is required")
		return 2
	}
	st := store.New(*dir)
	if _, err := st.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	entries, err := st.ReadLog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("No fills yet.")
		return 0
	}
	fmt.Printf("%-12s %-4s %12s %12s %10s %14s\n", "date", "side", "shares", "price", "cost", "equity_after")
	for _, e := range entries {
		fmt.Printf("%-12s %-4s %12.4f %12.2f %10.2f %14.2f\n",
			e.Date, e.Fill.Side, e.Fill.Shares, e.Fill.Price, e.Fill.Cost, e.EquityAfter)
	}
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "(none yet)"
	}
	return s
}

// accountSummary is one line of the multi-account overview.
type accountSummary struct {
	Dir       string
	Name      string
	Strategy  string
	Symbol    string
	LastBar   string
	Equity    float64
	Return    float64
	Fills     int
	HasEquity bool
}

// scanAccounts finds every immediate subdirectory of root holding an account and
// summarises it from stored state alone (no network). Results are sorted by
// directory name for stable output.
func scanAccounts(root string) ([]accountSummary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []accountSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		st := store.New(dir)
		if !st.Exists() {
			continue
		}
		a, err := st.Load()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		s := accountSummary{
			Dir: e.Name(), Name: a.Name, Strategy: a.Strategy.Name,
			Symbol: a.Symbol, LastBar: a.LastBarDate,
		}
		if snaps, err := st.ReadEquity(); err == nil && len(snaps) > 0 {
			last := snaps[len(snaps)-1]
			s.Equity = last.Equity
			s.HasEquity = true
			if a.InitialCapital > 0 {
				s.Return = last.Equity/a.InitialCapital - 1
			}
		}
		if fills, err := st.ReadLog(); err == nil {
			s.Fills = len(fills)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	root := fs.String("root", "", "directory containing one or more account subdirectories")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" {
		fmt.Fprintln(os.Stderr, "error: --root is required")
		return 2
	}
	accounts, err := scanAccounts(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(accounts) == 0 {
		fmt.Printf("No paper accounts found under %s.\n", *root)
		return 0
	}
	fmt.Printf("%-16s %-16s %-10s %-12s %14s %10s %7s\n",
		"account", "strategy", "symbol", "last bar", "equity", "return", "fills")
	for _, a := range accounts {
		equity := "—"
		ret := "—"
		if a.HasEquity {
			equity = fmt.Sprintf("%.2f", a.Equity)
			ret = fmt.Sprintf("%.2f%%", a.Return*100)
		}
		fmt.Printf("%-16s %-16s %-10s %-12s %14s %10s %7d\n",
			trunc(a.Dir, 16), trunc(a.Strategy, 16), trunc(a.Symbol, 10),
			orNone(a.LastBar), equity, ret, a.Fills)
	}
	fmt.Println()
	fmt.Println(disclaimer)
	return 0
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

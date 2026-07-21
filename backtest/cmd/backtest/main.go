// Command backtest runs rule-based trading strategies over historical NSE daily
// closes so you can measure an edge before risking any capital. The pipeline is:
// fetch daily closes (Yahoo) → load CSV → run strategy + buy-and-hold benchmark
// through the engine (with costs) → compute metrics → render.
//
// This is a research tool. Output is not investment advice and a backtest is not
// a forecast; see the disclaimer printed with every report.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/akagr/finance-tools/backtest/internal/engine"
	"github.com/akagr/finance-tools/backtest/internal/pipeline"
	"github.com/akagr/finance-tools/backtest/internal/report"
	"github.com/akagr/finance-tools/backtest/internal/yahoo"
)

const version = "0.1.0"

const disclaimer = "NOTE: not investment advice. A backtest is a hypothesis fit to the past, not a forecast."

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "version":
		fmt.Println("backtest " + version)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `backtest — measure a trading rule's edge on historical NSE data, before risking capital

Usage:
  backtest fetch prices --start <YYYY-MM-DD> --end <YYYY-MM-DD> [--tickers <file>]
  backtest run --prices <csv> [--symbol <s>] [--strategy <name>] [strategy flags]
  backtest version

Strategies (each is run against a buy-and-hold benchmark):
  all         run every strategy below and compare them in one table
  sma-cross   simple MA crossover      flags: --fast --slow
  ema-cross   exponential MA crossover flags: --fast --slow
  momentum    time-series momentum     flag:  --lookback
  rsi         RSI oversold (contrarian) flags: --rsi-period --rsi-threshold
  donchian    channel breakout (Turtle) flags: --entry --exit
  buy-hold    always fully invested

Run "backtest run -h" for all flags.

%s
`, disclaimer)
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		pricesP   = fs.String("prices", "", "price CSV file (columns: date,symbol,close)")
		symbol    = fs.String("symbol", "", "symbol in the CSV to test (default: first found)")
		strat     = fs.String("strategy", "sma-cross", "strategy: all|sma-cross|ema-cross|momentum|rsi|donchian|buy-hold")
		fast      = fs.Int("fast", 20, "fast MA window (sma-cross, ema-cross)")
		slow      = fs.Int("slow", 50, "slow MA window (sma-cross, ema-cross)")
		lookback  = fs.Int("lookback", 120, "lookback window in bars (momentum)")
		rsiPeriod = fs.Int("rsi-period", 14, "RSI period (rsi)")
		rsiThresh = fs.Float64("rsi-threshold", 30, "buy when RSI is below this (rsi)")
		entry     = fs.Int("entry", 20, "breakout entry window in bars (donchian)")
		exit      = fs.Int("exit", 10, "breakdown exit window in bars (donchian)")
		capital   = fs.Float64("capital", 100000, "initial capital in INR")
		brokBps   = fs.Float64("brokerage-bps", 0, "brokerage per trade, basis points")
		sttBps    = fs.Float64("stt-bps", 10, "securities transaction tax per trade, basis points")
		slipBps   = fs.Float64("slippage-bps", 5, "assumed slippage per trade, basis points")
		format    = fs.String("format", "md", "comma-separated output formats: md,csv,json")
		sortBy    = fs.String("sort", "return", "rank the table by: return|cagr|sharpe|sortino|calmar|drawdown")
		volTarget = fs.Float64("vol-target", 0, "annualised volatility target in percent (e.g. 10); 0 disables position sizing")
		volLook   = fs.Int("vol-lookback", 20, "trailing bars used to estimate realised volatility (--vol-target)")
		out       = fs.String("out", "", "output directory (default: print to stdout)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pricesP == "" {
		fmt.Fprintln(os.Stderr, "error: --prices is required")
		return 2
	}

	rep, err := pipeline.BuildReport(pipeline.Options{
		PricesPath:     *pricesP,
		Symbol:         *symbol,
		Strategy:       *strat,
		Fast:           *fast,
		Slow:           *slow,
		Lookback:       *lookback,
		RSIPeriod:      *rsiPeriod,
		RSIThreshold:   *rsiThresh,
		DonchianEntry:  *entry,
		DonchianExit:   *exit,
		InitialCapital: *capital,
		Costs:          engine.Costs{BrokerageBps: *brokBps, STTBps: *sttBps, SlippageBps: *slipBps},
		SortBy:         *sortBy,
		VolTarget:      *volTarget / 100, // flag is a percent; pipeline wants a fraction
		VolLookback:    *volLook,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	formats := splitCSV(*format)
	if *out == "" {
		for i, fmtName := range formats {
			if i > 0 {
				fmt.Println()
			}
			if err := report.Render(os.Stdout, rep, fmtName); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				return 1
			}
		}
		return 0
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for _, fmtName := range formats {
		path := filepath.Join(*out, "backtest."+extFor(fmtName))
		file, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if err := report.Render(file, rep, fmtName); err != nil {
			file.Close()
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		file.Close()
		fmt.Fprintln(os.Stderr, "wrote", path)
	}
	return 0
}

func extFor(format string) string {
	switch strings.ToLower(format) {
	case "md", "markdown":
		return "md"
	case "csv":
		return "csv"
	case "json":
		return "json"
	default:
		return format
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

const dateLayout = "2006-01-02"

// barFetcher is the subset of *yahoo.Client the fetch command needs; an
// interface so the fetch loop is testable without hitting the network.
type barFetcher interface {
	Chart(ctx context.Context, symbol string, start, end time.Time) ([]yahoo.Bar, error)
}

func cmdFetch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: fetch needs a subcommand: prices")
		return 2
	}
	switch args[0] {
	case "prices":
		return cmdFetchPrices(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown fetch subcommand %q (want prices)\n", args[0])
		return 2
	}
}

func cmdFetchPrices(args []string) int {
	fs := flag.NewFlagSet("fetch prices", flag.ExitOnError)
	var (
		start    = fs.String("start", "", "start date YYYY-MM-DD (inclusive)")
		end      = fs.String("end", "", "end date YYYY-MM-DD (inclusive)")
		tickersP = fs.String("tickers", "data/tickers.txt", "tickers file: lines of '<label> <yahoo-symbol>'")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, e, code := parseRange(*start, *end)
	if code != 0 {
		return code
	}

	tf, err := os.Open(*tickersP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening tickers file %q: %v\n", *tickersP, err)
		return 1
	}
	defer tf.Close()
	tickers, err := parseFetchTickers(tf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading tickers file %q: %v\n", *tickersP, err)
		return 1
	}
	if len(tickers) == 0 {
		fmt.Fprintf(os.Stderr, "error: no tickers in %q\n", *tickersP)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := fetchPricesTo(ctx, yahoo.NewClient(), os.Stdout, tickers, s, e); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func parseRange(start, end string) (time.Time, time.Time, int) {
	if start == "" || end == "" {
		fmt.Fprintln(os.Stderr, "error: --start and --end are required (YYYY-MM-DD)")
		return time.Time{}, time.Time{}, 2
	}
	s, err := time.Parse(dateLayout, start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: bad --start %q (want YYYY-MM-DD)\n", start)
		return time.Time{}, time.Time{}, 2
	}
	e, err := time.Parse(dateLayout, end)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: bad --end %q (want YYYY-MM-DD)\n", end)
		return time.Time{}, time.Time{}, 2
	}
	if e.Before(s) {
		fmt.Fprintf(os.Stderr, "error: --end %s is before --start %s\n", end, start)
		return time.Time{}, time.Time{}, 2
	}
	return s, e, 0
}

// fetchTicker is one asset to fetch daily closes for.
type fetchTicker struct {
	Label string
	Yahoo string
}

// parseFetchTickers reads lines of "<label> <yahoo-symbol>"; blank lines and
// lines beginning with '#' are ignored.
func parseFetchTickers(r io.Reader) ([]fetchTicker, error) {
	var out []fetchTicker
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			fmt.Fprintf(os.Stderr, "WARN: skipping malformed ticker line: %s\n", line)
			continue
		}
		out = append(out, fetchTicker{Label: parts[0], Yahoo: parts[1]})
	}
	return out, sc.Err()
}

// fetchPricesTo writes columns date,symbol,close for each ticker.
func fetchPricesTo(ctx context.Context, f barFetcher, w io.Writer, tickers []fetchTicker, start, end time.Time) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "symbol", "close"}); err != nil {
		return err
	}
	for _, tk := range tickers {
		bars, err := f.Chart(ctx, tk.Yahoo, start, end)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", tk.Label, tk.Yahoo, err)
		}
		for _, b := range bars {
			if err := cw.Write([]string{b.Date, tk.Label, strconv.FormatFloat(b.Close, 'f', 4, 64)}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

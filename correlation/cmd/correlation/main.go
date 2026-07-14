// Command correlation computes return correlations across two or more assets so
// you can see how diversified a portfolio really is. The pipeline is: load price
// series (CSV) → optionally convert to a base currency → align to a common
// frequency → compute period returns → correlation/covariance → render.
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

	"github.com/akagr/finance-tools/correlation/internal/pipeline"
	"github.com/akagr/finance-tools/correlation/internal/report"
	"github.com/akagr/finance-tools/correlation/internal/yahoo"
)

const version = "0.1.0"

const disclaimer = "NOTE: not investment advice. Output is a working draft; correlations are backward-looking and unstable."

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "compute":
		os.Exit(cmdCompute(os.Args[2:]))
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "version":
		fmt.Println("correlation " + version)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `correlation — return correlations across assets, to gauge diversification

Usage:
  correlation compute --prices <csv|dir> [flags]
  correlation fetch prices --start <YYYY-MM-DD> --end <YYYY-MM-DD> [--tickers <file>]
  correlation fetch fx --start <YYYY-MM-DD> --end <YYYY-MM-DD> <currency>:<yahoo-symbol> [...]
  correlation version

Run "correlation compute -h" for flags.

%s
`, disclaimer)
}

func cmdCompute(args []string) int {
	fs := flag.NewFlagSet("compute", flag.ExitOnError)
	var (
		pricesP   = fs.String("prices", "", "price CSV file or directory (columns: date,symbol,close[,currency])")
		defCur    = fs.String("default-currency", "USD", "currency for price rows that omit one")
		baseCur   = fs.String("base-currency", "", "convert every series to this currency before correlating (native mode if empty)")
		fxP       = fs.String("fx", "", "FX CSV (columns: date,currency,rate) used when --base-currency needs conversions")
		frequency = fs.String("frequency", "weekly", "resampling frequency: daily|weekly|monthly")
		retKind   = fs.String("returns", "log", "return type: log|simple")
		rollWin   = fs.Int("rolling-window", 0, "if >0, also emit rolling correlation over this many return observations")
		format    = fs.String("format", "md", "comma-separated output formats: md,csv,json")
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
		PricesPath:      *pricesP,
		DefaultCurrency: *defCur,
		BaseCurrency:    *baseCur,
		FXPath:          *fxP,
		Frequency:       *frequency,
		ReturnKind:      *retKind,
		RollingWindow:   *rollWin,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	formats := splitCSV(*format)
	if *out == "" {
		for i, f := range formats {
			if i > 0 {
				fmt.Println()
			}
			if err := report.Render(os.Stdout, rep, f); err != nil {
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
	for _, f := range formats {
		path := filepath.Join(*out, "correlation."+extFor(f))
		file, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if err := report.Render(file, rep, f); err != nil {
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
// interface so the fetch loops are testable without hitting the network.
type barFetcher interface {
	Chart(ctx context.Context, symbol string, start, end time.Time) ([]yahoo.Bar, error)
}

// cmdFetch dispatches the `fetch prices` / `fetch fx` subcommands, which pull
// daily closes (and FX rates) from the Yahoo Finance chart API and write the
// CSVs `compute` expects to stdout.
func cmdFetch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: fetch needs a subcommand: prices | fx")
		return 2
	}
	switch args[0] {
	case "prices":
		return cmdFetchPrices(args[1:])
	case "fx":
		return cmdFetchFX(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown fetch subcommand %q (want prices | fx)\n", args[0])
		return 2
	}
}

func cmdFetchPrices(args []string) int {
	fs := flag.NewFlagSet("fetch prices", flag.ExitOnError)
	var (
		start    = fs.String("start", "", "start date YYYY-MM-DD (inclusive)")
		end      = fs.String("end", "", "end date YYYY-MM-DD (inclusive)")
		tickersP = fs.String("tickers", "data/tickers.txt", "tickers file: lines of '<label> <yahoo-symbol> [currency]'")
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

func cmdFetchFX(args []string) int {
	fs := flag.NewFlagSet("fetch fx", flag.ExitOnError)
	var (
		start = fs.String("start", "", "start date YYYY-MM-DD (inclusive)")
		end   = fs.String("end", "", "end date YYYY-MM-DD (inclusive)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, e, code := parseRange(*start, *end)
	if code != 0 {
		return code
	}
	specs := fs.Args()
	if len(specs) == 0 {
		fmt.Fprintln(os.Stderr, "error: fetch fx needs at least one <currency>:<yahoo-symbol> spec, e.g. USD:INR=X")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := fetchFXTo(ctx, yahoo.NewClient(), os.Stdout, specs, s, e); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// parseRange validates the --start/--end flags, returning an exit code (0 = ok).
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
	Label    string
	Yahoo    string
	Currency string
}

// parseFetchTickers reads lines of "<label> <yahoo-symbol> [currency]"; blank
// lines and lines beginning with '#' are ignored. Currency defaults to USD.
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
		cur := "USD"
		if len(parts) > 2 {
			cur = parts[2]
		}
		out = append(out, fetchTicker{Label: parts[0], Yahoo: parts[1], Currency: cur})
	}
	return out, sc.Err()
}

// fetchPricesTo writes columns date,symbol,close,currency for each ticker.
func fetchPricesTo(ctx context.Context, f barFetcher, w io.Writer, tickers []fetchTicker, start, end time.Time) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "symbol", "close", "currency"}); err != nil {
		return err
	}
	for _, tk := range tickers {
		bars, err := f.Chart(ctx, tk.Yahoo, start, end)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", tk.Label, tk.Yahoo, err)
		}
		for _, b := range bars {
			if err := cw.Write([]string{b.Date, tk.Label, strconv.FormatFloat(b.Close, 'f', 4, 64), tk.Currency}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

// fetchFXTo writes columns date,currency,rate for each <currency>:<symbol> spec.
// rate is the value of 1 unit of <currency> in the base currency (e.g. INR per USD).
func fetchFXTo(ctx context.Context, f barFetcher, w io.Writer, specs []string, start, end time.Time) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "currency", "rate"}); err != nil {
		return err
	}
	for _, spec := range specs {
		currency, symbol, ok := strings.Cut(spec, ":")
		if !ok || currency == "" || symbol == "" {
			return fmt.Errorf("bad fx spec %q; want CURRENCY:YAHOO-SYMBOL (e.g. USD:INR=X)", spec)
		}
		bars, err := f.Chart(ctx, symbol, start, end)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", currency, symbol, err)
		}
		for _, b := range bars {
			if err := cw.Write([]string{b.Date, currency, strconv.FormatFloat(b.Close, 'f', 4, 64)}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

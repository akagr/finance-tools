// Command schedulefa generates an Indian Schedule FA (Foreign Assets) report
// from Interactive Brokers holdings. The generate pipeline is: ingest a Flex
// statement (a saved XML or an online Flex Web Service pull) → convert with SBI
// TTBR → compute peaks → build Tables A2/A3 → render md/csv/json.
package main

import (
	"bufio"
	"bytes"
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

	"github.com/akagr/finance-tools/schedule-fa/internal/entities"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/ibkr"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
	"github.com/akagr/finance-tools/schedule-fa/internal/peak"
	"github.com/akagr/finance-tools/schedule-fa/internal/pipeline"
	"github.com/akagr/finance-tools/schedule-fa/internal/prices"
	"github.com/akagr/finance-tools/schedule-fa/internal/report"
	"github.com/akagr/finance-tools/schedule-fa/internal/yahoo"
)

const disclaimer = "NOTE: not tax advice. Output is a working draft to verify before filing."

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "generate":
		os.Exit(cmdGenerate(os.Args[2:]))
	case "fetch-prices":
		os.Exit(cmdFetchPrices(os.Args[2:]))
	case "version":
		fmt.Println("schedulefa 0.6.0")
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `schedulefa — Schedule FA (Foreign Assets) report from IBKR holdings

Usage:
  schedulefa generate --year <YYYY> --statement <file.xml> [flags]
  schedulefa fetch-prices --year <YYYY> [--tickers <file>] [--out <file>]
  schedulefa version

Run "schedulefa generate -h" for generate flags.
Run "schedulefa fetch-prices -h" for fetch-prices flags.

%s
`, disclaimer)
}

func cmdGenerate(args []string) int {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	var (
		year      = fs.Int("year", 0, "CALENDAR year to report (Jan 1 – Dec 31), e.g. 2024")
		statement = fs.String("statement", "", "path to an IBKR Activity Flex Query XML export (offline mode)")
		flexToken = fs.String("flex-token", "", "IBKR Flex Web Service token (online mode)")
		flexQuery = fs.String("flex-query", "", "IBKR Activity Flex Query id (online mode)")
		saveStmt  = fs.String("save-statement", "", "when online, also save the fetched Flex XML to this path")
		rates     = fs.String("rates", "", "path to an SBI TTBR rates CSV (overrides bundled)")
		entitiesP = fs.String("entities", "data/entities", "entity metadata CSV file or dir (optional)")
		pricesP   = fs.String("prices", "", "path to a daily prices CSV/dir (enables exact peak, mode B)")
		out       = fs.String("out", "private/report", "output directory (default under gitignored private/)")
		format    = fs.String("format", "md,csv,json", "comma-separated: md,csv,json,html")
	)
	fs.Parse(args)

	// Enforce the calendar-year basis — the single most common Schedule FA error.
	if *year == 0 {
		fmt.Fprintln(os.Stderr, "error: --year is required (CALENDAR year, e.g. 2024 for AY 2025-26)")
		return 2
	}
	if *year < 2000 || *year > 2099 {
		fmt.Fprintf(os.Stderr, "error: --year %d is not a plausible calendar year\n", *year)
		return 2
	}
	online := *flexToken != "" || *flexQuery != ""
	if *statement == "" && !online {
		fmt.Fprintln(os.Stderr, "error: provide --statement <file.xml> (offline) or --flex-token + --flex-query (online)")
		return 2
	}
	if online && (*flexToken == "" || *flexQuery == "") {
		fmt.Fprintln(os.Stderr, "error: online mode needs both --flex-token and --flex-query")
		return 2
	}

	fmt.Printf("schedulefa: Schedule FA for calendar year %d (%d-01-01 to %d-12-31)\n", *year, *year, *year)

	// Ingest: pull from the Flex Web Service (online) or parse a saved XML.
	var st *model.Statement
	if online {
		fmt.Printf("  source           : Flex Web Service (query %s)\n", *flexQuery)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		body, err := ibkr.NewFlexClient().Fetch(ctx, *flexToken, *flexQuery)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if *saveStmt != "" {
			if err := os.WriteFile(*saveStmt, body, 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "error: saving statement to %q: %v\n", *saveStmt, err)
				return 1
			}
			fmt.Printf("  saved statement  : %s\n", *saveStmt)
		}
		st, err = ibkr.ParseFlexXML(bytes.NewReader(body), *year)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	} else {
		var err error
		st, err = ibkr.ParseFlexFile(*statement, *year)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Printf("  account          : %s (%s), base %s\n", st.Account.Number, st.Account.Name, st.Account.BaseCurrency)
	fmt.Printf("  open positions   : %d (year-end snapshot)\n", len(st.OpenPositions))
	fmt.Printf("  lots/trades/divs : %d / %d / %d\n", len(st.Lots), len(st.Trades), len(st.Dividends))

	// Load SBI TTBR rates (M2). Default to ./data/ttbr if --rates is omitted.
	ratesPath := orDefault(*rates, "data/ttbr")
	store, err := fx.LoadRateKeeper(ratesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading TTBR rates from %q: %v\n", ratesPath, err)
		fmt.Fprintln(os.Stderr, "hint: download a RateKeeper CSV (see data/ttbr/README.md) and pass --rates <file|dir>")
		return 1
	}
	fmt.Printf("  rates            : %s\n", ratesPath)

	// Prices enable the exact peak engine (mode B); otherwise mode C.
	var priceProvider peak.PriceProvider
	if *pricesP != "" {
		priceStore, err := prices.Load(*pricesP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: loading prices from %q: %v\n", *pricesP, err)
			return 1
		}
		priceProvider = priceStore
	}
	ents, err := entities.Load(*entitiesP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading entity metadata from %q: %v\n", *entitiesP, err)
		return 1
	}
	res, err := pipeline.BuildReport(st, store, priceProvider, ents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: building report: %v\n", err)
		return 1
	}
	rep := res.Report
	if res.ExactPeak {
		fmt.Printf("  peak mode        : exact (mode B), prices %s%s\n", *pricesP, exactNote(res.A2PeakExact))
	} else {
		fmt.Printf("  peak mode        : approximate (mode C) — pass --prices for exact (mode B)\n")
	}
	formats, err := parseFormats(*format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	paths, err := report.Write(*out, formats, rep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: writing report: %v\n", err)
		return 1
	}

	review := 0
	for _, r := range rep.A3 {
		if r.NeedsReview {
			review++
		}
	}
	fmt.Printf("  A3 rows          : %d  (%d need manual review)\n", len(rep.A3), review)
	fmt.Printf("  wrote            : %s\n", strings.Join(paths, ", "))
	fmt.Fprintln(os.Stderr, "\n"+disclaimer)
	return 0
}

func parseFormats(s string) ([]report.Format, error) {
	var out []report.Format
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		switch report.Format(f) {
		case report.Markdown, report.CSV, report.JSON, report.HTML:
			out = append(out, report.Format(f))
		default:
			return nil, fmt.Errorf("unknown --format %q (want md,csv,json,html)", f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid formats in %q", s)
	}
	return out, nil
}

func exactNote(exact bool) string {
	if exact {
		return ""
	}
	return " (some held days missing prices — A2 peak left as upper bound)"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// priceTicker is one holding to fetch daily closes for.
type priceTicker struct {
	Symbol   string
	Yahoo    string
	ISIN     string
	Currency string
}

// barFetcher is the subset of *yahoo.Client the fetch-prices command needs;
// an interface so the fetch loop is testable without hitting the network.
type barFetcher interface {
	Chart(ctx context.Context, symbol string, start, end time.Time) ([]yahoo.Bar, error)
}

// cmdFetchPrices fetches daily closes from Yahoo Finance into the CSV that
// `generate --prices` expects (columns: date,symbol,isin,close,currency). It
// writes the RAW (unadjusted) close — the figure Schedule FA wants.
func cmdFetchPrices(args []string) int {
	fs := flag.NewFlagSet("fetch-prices", flag.ExitOnError)
	var (
		year     = fs.Int("year", 0, "CALENDAR year to fetch prices for (Jan 1 – Dec 31), e.g. 2026")
		tickersP = fs.String("tickers", "data/prices/tickers.txt", "tickers file: lines of '<symbol> <yahoo-symbol> <isin> [currency]'")
		out      = fs.String("out", "", "output CSV path (default: data/prices/prices-<year>.csv)")
	)
	fs.Parse(args)

	if *year == 0 {
		fmt.Fprintln(os.Stderr, "error: --year is required (CALENDAR year, e.g. 2026)")
		return 2
	}
	if *year < 2000 || *year > 2099 {
		fmt.Fprintf(os.Stderr, "error: --year %d is not a plausible calendar year\n", *year)
		return 2
	}

	tf, err := os.Open(*tickersP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening tickers file %q: %v\n", *tickersP, err)
		return 1
	}
	defer tf.Close()
	tickers, err := parsePriceTickers(tf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading tickers file %q: %v\n", *tickersP, err)
		return 1
	}
	if len(tickers) == 0 {
		fmt.Fprintf(os.Stderr, "error: no tickers in %q\n", *tickersP)
		return 1
	}

	outPath := orDefault(*out, filepath.Join("data", "prices", fmt.Sprintf("prices-%d.csv", *year)))
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating output dir: %v\n", err)
		return 1
	}
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating %q: %v\n", outPath, err)
		return 1
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	total, err := fetchPricesTo(ctx, yahoo.NewClient(), f, tickers, *year, 500*time.Millisecond)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "wrote %d rows to %s\n", total, outPath)
	if total == 0 {
		return 1
	}
	return 0
}

// parsePriceTickers reads lines of "<symbol> <yahoo-symbol> <isin> [currency]";
// blank lines and lines beginning with '#' are ignored. Currency defaults to USD.
func parsePriceTickers(r io.Reader) ([]priceTicker, error) {
	var out []priceTicker
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			fmt.Fprintf(os.Stderr, "WARN: skipping malformed ticker line: %s\n", line)
			continue
		}
		cur := "USD"
		if len(parts) > 3 {
			cur = parts[3]
		}
		out = append(out, priceTicker{Symbol: parts[0], Yahoo: parts[1], ISIN: parts[2], Currency: cur})
	}
	return out, sc.Err()
}

// fetchPricesTo fetches each ticker's daily closes for the calendar year and
// writes CSV rows to w. delay is the pause between tickers (0 to disable). A
// per-ticker fetch failure is reported to stderr and skipped, not fatal. It
// returns the number of data rows written.
func fetchPricesTo(ctx context.Context, f barFetcher, w io.Writer, tickers []priceTicker, year int, delay time.Duration) (int, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "symbol", "isin", "close", "currency"}); err != nil {
		return 0, err
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

	total := 0
	for i, tk := range tickers {
		bars, err := f.Chart(ctx, tk.Yahoo, start, end)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: %s (%s) failed: %v\n", tk.Symbol, tk.Yahoo, err)
			continue
		}
		for _, b := range bars {
			if err := cw.Write([]string{b.Date, tk.Symbol, tk.ISIN, strconv.FormatFloat(b.Close, 'f', 4, 64), tk.Currency}); err != nil {
				return total, err
			}
			total++
		}
		tag := fmt.Sprintf("%d rows", len(bars))
		if len(bars) == 0 {
			tag = "0 rows (check the Yahoo symbol)"
		}
		fmt.Fprintf(os.Stderr, "  %s (%s): %s\n", tk.Symbol, tk.Yahoo, tag)
		if delay > 0 && i < len(tickers)-1 {
			if err := sleepCtx(ctx, delay); err != nil {
				return total, err
			}
		}
	}
	cw.Flush()
	return total, cw.Error()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

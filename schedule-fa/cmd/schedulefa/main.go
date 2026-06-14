// Command schedulefa generates an Indian Schedule FA (Foreign Assets) report
// from Interactive Brokers holdings. The generate pipeline is: ingest a Flex
// statement (a saved XML or an online Flex Web Service pull) → convert with SBI
// TTBR → compute peaks → build Tables A2/A3 → render md/csv/json.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/entities"
	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/ibkr"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
	"github.com/akagr/tax-tools/schedule-fa/internal/peak"
	"github.com/akagr/tax-tools/schedule-fa/internal/prices"
	"github.com/akagr/tax-tools/schedule-fa/internal/report"
	"github.com/akagr/tax-tools/schedule-fa/internal/schedulefa"
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
  schedulefa version

Run "schedulefa generate -h" for generate flags.

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

	// Peak: exact (mode B) when --prices is given, else approximate (mode C).
	var peaks []peak.Result
	var a2Peak *fx.Conversion
	if *pricesP != "" {
		priceStore, err := prices.Load(*pricesP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: loading prices from %q: %v\n", *pricesP, err)
			return 1
		}
		secs, port, exact, err := peak.ComputeExact(st, store, priceStore)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: computing exact peak: %v\n", err)
			return 1
		}
		peaks = secs
		if exact {
			a2Peak = &port
		}
		fmt.Printf("  peak mode        : exact (mode B), prices %s%s\n", *pricesP, exactNote(exact))
	} else {
		peaks, _ = peak.Compute(st, store, peak.ModeApprox, nil)
		fmt.Printf("  peak mode        : approximate (mode C) — pass --prices for exact (mode B)\n")
	}

	ents, err := entities.Load(*entitiesP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading entity metadata from %q: %v\n", *entitiesP, err)
		return 1
	}
	rep, err := schedulefa.Build(st, store, peaks, schedulefa.Options{Entities: ents, A2Peak: a2Peak})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: building report: %v\n", err)
		return 1
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

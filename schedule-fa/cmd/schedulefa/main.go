// Command schedulefa generates an Indian Schedule FA (Foreign Assets) report
// from Interactive Brokers holdings.
//
// M0: CLI scaffold + flag validation only. The generate pipeline (parse →
// fx → peak → build → render) is wired up in M1–M7.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/akagr/tax-tools/schedule-fa/internal/entities"
	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/ibkr"
	"github.com/akagr/tax-tools/schedule-fa/internal/peak"
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
		fmt.Println("schedulefa 0.0.0 (M0 scaffold)")
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
		flexToken = fs.String("flex-token", "", "IBKR Flex Web Service token (online mode; M6)")
		flexQuery = fs.String("flex-query", "", "IBKR Flex Query id (online mode; M6)")
		rates     = fs.String("rates", "", "path to an SBI TTBR rates CSV (overrides bundled)")
		entitiesP = fs.String("entities", "data/entities", "entity metadata CSV file or dir (optional)")
		prices    = fs.String("prices", "", "path to a daily prices CSV (enables exact peak; M4)")
		out       = fs.String("out", "private/report", "output directory (default under gitignored private/)")
		format    = fs.String("format", "md,csv,json", "comma-separated: md,csv,json")
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
		fmt.Fprintln(os.Stderr, "error: provide --statement <file.xml> (offline) or --flex-token/--flex-query (online, M6)")
		return 2
	}
	if online {
		fmt.Fprintln(os.Stderr, "error: Flex Web Service (online) ingest is not available until M6; use --statement for now")
		return 1
	}

	fmt.Printf("schedulefa: Schedule FA for calendar year %d (%d-01-01 to %d-12-31)\n", *year, *year, *year)

	// M1: parse the IBKR Flex XML and summarize. Downstream stages follow.
	st, err := ibkr.ParseFlexFile(*statement, *year)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("  account          : %s (%s), base %s\n", st.Account.Number, st.Account.Name, st.Account.BaseCurrency)
	fmt.Printf("  open positions   : %d (year-end snapshot)\n", len(st.OpenPositions))
	fmt.Printf("  lots/trades/divs : %d / %d / %d\n", len(st.Lots), len(st.Trades), len(st.Dividends))

	if *prices != "" {
		fmt.Fprintln(os.Stderr, "note: --prices (exact peak, mode B) is not wired until M4; using approximate peak (mode C)")
	}

	// Load SBI TTBR rates (M2). Default to ./data/ttbr if --rates is omitted.
	ratesPath := orDefault(*rates, "data/ttbr")
	store, err := fx.LoadRateKeeper(ratesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading TTBR rates from %q: %v\n", ratesPath, err)
		fmt.Fprintln(os.Stderr, "hint: download a RateKeeper CSV (see data/ttbr/README.md) and pass --rates <file|dir>")
		return 1
	}
	fmt.Printf("  rates            : %s\n", ratesPath)

	// Peak (mode C) → A3 rows → render.
	peaks, err := peak.Compute(st, store, peak.ModeApprox, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: computing peak values: %v\n", err)
		return 1
	}
	ents, err := entities.Load(*entitiesP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading entity metadata from %q: %v\n", *entitiesP, err)
		return 1
	}
	rep, err := schedulefa.Build(st, store, peaks, ents)
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
		case report.Markdown, report.CSV, report.JSON:
			out = append(out, report.Format(f))
		default:
			return nil, fmt.Errorf("unknown --format %q (want md,csv,json)", f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid formats in %q", s)
	}
	return out, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

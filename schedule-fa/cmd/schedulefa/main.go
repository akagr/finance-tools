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

	"github.com/akagr/tax-tools/schedule-fa/internal/ibkr"
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
		prices    = fs.String("prices", "", "path to a daily prices CSV (enables exact peak; M4)")
		out       = fs.String("out", "./report", "output directory")
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
	fmt.Printf("  lots             : %d\n", len(st.Lots))
	fmt.Printf("  trades           : %d\n", len(st.Trades))
	fmt.Printf("  dividends        : %d\n", len(st.Dividends))

	fmt.Printf("  rates            : %s\n", orDefault(*rates, "(bundled data/ttbr/*.csv)"))
	fmt.Printf("  peak mode        : approximate (mode C) — exact (mode B) needs --prices (M4)\n")
	if *prices != "" {
		fmt.Printf("  prices           : %s\n", *prices)
	}
	fmt.Printf("  output           : %s  [%s]\n", *out, strings.ReplaceAll(*format, ",", ", "))
	fmt.Fprintln(os.Stderr, "\nparsed OK. Remaining pipeline (fx → peak → build → report) not implemented yet (M1). "+disclaimer)
	return 1
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

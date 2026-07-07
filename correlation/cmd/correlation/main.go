// Command correlation computes return correlations across two or more assets so
// you can see how diversified a portfolio really is. The pipeline is: load price
// series (CSV) → optionally convert to a base currency → align to a common
// frequency → compute period returns → correlation/covariance → render.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akagr/finance-tools/correlation/internal/pipeline"
	"github.com/akagr/finance-tools/correlation/internal/report"
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

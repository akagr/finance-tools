// Package pipeline is the orchestration seam shared by the CLI and the golden
// test: it loads price series, optionally converts them to a base currency,
// aligns and differences them into returns, runs the correlation stats, and
// assembles a render-ready report. The command layer does I/O only.
package pipeline

import (
	"fmt"
	"math"
	"sort"

	"github.com/akagr/finance-tools/correlation/internal/align"
	"github.com/akagr/finance-tools/correlation/internal/fxconv"
	"github.com/akagr/finance-tools/correlation/internal/report"
	"github.com/akagr/finance-tools/correlation/internal/returns"
	"github.com/akagr/finance-tools/correlation/internal/series"
	"github.com/akagr/finance-tools/correlation/internal/stats"
)

// Options configures a correlation run.
type Options struct {
	PricesPath      string // CSV file or directory of *.csv
	DefaultCurrency string // currency for price rows that omit one
	BaseCurrency    string // "" = native mode (no FX conversion)
	FXPath          string // FX CSV; required when BaseCurrency needs conversions
	Frequency       string // daily|weekly|monthly
	ReturnKind      string // log|simple
	RollingWindow   int    // >0 enables rolling correlation over this many return observations
}

// smallSample is the point below which correlations are flagged as noisy.
const smallSample = 20

// BuildReport runs the full offline pipeline and returns a render-ready report.
func BuildReport(opts Options) (report.Report, error) {
	freq, err := align.ParseFrequency(opts.Frequency)
	if err != nil {
		return report.Report{}, err
	}
	kind, err := returns.ParseKind(opts.ReturnKind)
	if err != nil {
		return report.Report{}, err
	}
	defCur := opts.DefaultCurrency
	if defCur == "" {
		defCur = "USD"
	}

	all, err := series.Load(opts.PricesPath, defCur)
	if err != nil {
		return report.Report{}, err
	}
	if len(all) < 2 {
		return report.Report{}, fmt.Errorf("pipeline: need >=2 assets, found %d in %s", len(all), opts.PricesPath)
	}

	var notes []string
	converted := make([]bool, len(all))

	if opts.BaseCurrency != "" {
		var fx *fxconv.Table
		if opts.FXPath != "" {
			if fx, err = fxconv.LoadFX(opts.FXPath); err != nil {
				return report.Report{}, err
			}
		}
		for i := range all {
			was := all[i].Currency
			all[i], err = fxconv.Convert(all[i], opts.BaseCurrency, fx)
			if err != nil {
				return report.Report{}, err
			}
			converted[i] = was != opts.BaseCurrency
		}
	} else if curs := distinctCurrencies(all); len(curs) > 1 {
		notes = append(notes, fmt.Sprintf(
			"Native mode with mixed currencies (%s): these correlations blend asset and FX co-movement. Pass --base-currency (with --fx) to normalise to one currency.",
			join(curs)))
	}

	aligned, err := align.Build(all, freq)
	if err != nil {
		return report.Report{}, err
	}
	rets, err := returns.Compute(aligned, kind)
	if err != nil {
		return report.Report{}, err
	}
	res, err := stats.Compute(rets.Labels, rets.Series)
	if err != nil {
		return report.Report{}, err
	}

	if res.N < smallSample {
		notes = append(notes, fmt.Sprintf(
			"Small sample: only %d %s return observations. Correlations are noisy; prefer a longer window or a lower frequency.",
			res.N, freq))
	}

	ppy := math.Sqrt(freq.PeriodsPerYear())
	annVol := make([]float64, len(res.Stdev))
	for i, sd := range res.Stdev {
		annVol[i] = sd * ppy
	}

	assets := make([]report.Asset, len(all))
	for i, s := range all {
		assets[i] = report.Asset{Label: s.Label, Currency: s.Currency, Converted: converted[i]}
	}
	pairs := make([]report.Pair, len(res.Pairs))
	for i, p := range res.Pairs {
		pairs[i] = report.Pair{A: p.A, B: p.B, R: p.R, CI95Lo: p.CI95Lo, CI95Hi: p.CI95Hi}
	}

	var rolling report.Rolling
	if opts.RollingWindow > 0 {
		if opts.RollingWindow < 2 {
			return report.Report{}, fmt.Errorf("pipeline: rolling window must be >= 2, got %d", opts.RollingWindow)
		}
		if opts.RollingWindow > res.N {
			return report.Report{}, fmt.Errorf("pipeline: rolling window %d exceeds %d %s return observations; shorten the window, lower the frequency, or widen the date range",
				opts.RollingWindow, res.N, freq)
		}
		rollPairs, err := stats.Rolling(rets.Labels, rets.Series, opts.RollingWindow)
		if err != nil {
			return report.Report{}, err
		}
		rolling.Window = opts.RollingWindow
		if len(rollPairs) > 0 {
			for _, idx := range rollPairs[0].EndIdx {
				rolling.Dates = append(rolling.Dates, rets.EndDates[idx])
			}
		}
		for _, rp := range rollPairs {
			rolling.Pairs = append(rolling.Pairs, report.RollingPair{A: rp.A, B: rp.B, Values: rp.Values})
		}
		if rolling.Window < smallSample {
			notes = append(notes, fmt.Sprintf(
				"Rolling window is only %d observations: each rolling r is noisy and its jitter can look like real regime change. Read the trend, not single points.",
				rolling.Window))
		}
	}

	return report.Report{
		Meta: report.Meta{
			Frequency:    string(freq),
			ReturnKind:   string(kind),
			BaseCurrency: opts.BaseCurrency,
			Start:        aligned.Dates[0],
			End:          aligned.Dates[len(aligned.Dates)-1],
			Assets:       assets,
			Notes:        notes,
		},
		Labels:      res.Labels,
		Correlation: res.Correlation,
		Covariance:  res.Covariance,
		Mean:        res.Mean,
		Stdev:       res.Stdev,
		AnnVol:      annVol,
		Pairs:       pairs,
		Rolling:     rolling,
		N:           res.N,
	}, nil
}

func distinctCurrencies(all []series.Series) []string {
	set := map[string]struct{}{}
	for _, s := range all {
		set[s.Currency] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

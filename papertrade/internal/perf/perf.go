// Package perf turns a paper account's recorded equity snapshots into the
// performance metrics used to judge it, and compares them against a buy-and-hold
// benchmark computed from the same quotes over the same dates. Money and
// statistics are float64, like the sibling research tools.
package perf

import (
	"math"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/store"
)

const tradingDaysPerYear = 252.0

// Summary is the paper account's performance next to buy-and-hold over the same
// snapshot dates.
type Summary struct {
	Start       string
	End         string
	Snapshots   int
	StartEquity float64
	EndEquity   float64
	TotalReturn float64
	CAGR        float64
	AnnVol      float64
	Sharpe      float64
	MaxDrawdown float64
	BenchReturn float64 // buy-and-hold over the same quotes/dates
	BenchMaxDD  float64
}

// Summarize computes the metrics from equity snapshots (ascending by date). It
// needs at least two snapshots. Snapshots sharing a date keep the last (a forced
// re-step overwrites that day).
func Summarize(snaps []store.EquitySnapshot) (Summary, bool) {
	snaps = dedupByDate(snaps)
	if len(snaps) < 2 {
		return Summary{}, false
	}
	equity := make([]float64, len(snaps))
	quotes := make([]float64, len(snaps))
	for i, s := range snaps {
		equity[i] = s.Equity
		quotes[i] = s.Quote
	}

	su := Summary{
		Start:       snaps[0].Date,
		End:         snaps[len(snaps)-1].Date,
		Snapshots:   len(snaps),
		StartEquity: equity[0],
		EndEquity:   equity[len(equity)-1],
	}
	if equity[0] > 0 {
		su.TotalReturn = equity[len(equity)-1]/equity[0] - 1
	}
	rets := dailyReturns(equity)
	mean, sd := meanStdev(rets)
	su.AnnVol = sd * math.Sqrt(tradingDaysPerYear)
	if sd > 0 {
		su.Sharpe = mean / sd * math.Sqrt(tradingDaysPerYear)
	}
	if years := spanYears(su.Start, su.End); years > 0 && equity[0] > 0 {
		su.CAGR = math.Pow(equity[len(equity)-1]/equity[0], 1.0/years) - 1
	}
	su.MaxDrawdown = maxDrawdown(equity)

	// Buy-and-hold benchmark over the same quotes.
	if quotes[0] > 0 {
		su.BenchReturn = quotes[len(quotes)-1]/quotes[0] - 1
	}
	su.BenchMaxDD = maxDrawdown(quotes)
	return su, true
}

func dedupByDate(snaps []store.EquitySnapshot) []store.EquitySnapshot {
	if len(snaps) == 0 {
		return snaps
	}
	out := make([]store.EquitySnapshot, 0, len(snaps))
	for _, s := range snaps {
		if len(out) > 0 && out[len(out)-1].Date == s.Date {
			out[len(out)-1] = s // last wins for a repeated date
			continue
		}
		out = append(out, s)
	}
	return out
}

func dailyReturns(equity []float64) []float64 {
	out := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] > 0 {
			out = append(out, equity[i]/equity[i-1]-1)
		}
	}
	return out
}

func meanStdev(xs []float64) (mean, sd float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	if len(xs) < 2 {
		return mean, 0
	}
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}

func maxDrawdown(series []float64) float64 {
	if len(series) == 0 {
		return 0
	}
	peak := series[0]
	worst := 0.0
	for _, v := range series {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			if dd := (peak - v) / peak; dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

func spanYears(start, end string) float64 {
	const layout = "2006-01-02"
	s, err1 := time.Parse(layout, start)
	e, err2 := time.Parse(layout, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	return e.Sub(s).Hours() / 24.0 / 365.25
}

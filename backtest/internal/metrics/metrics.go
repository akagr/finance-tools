// Package metrics turns an equity curve into the performance statistics used to
// judge a strategy: total return, CAGR, annualised volatility, Sharpe ratio,
// maximum drawdown and exposure. Returns are simple daily returns of the curve;
// annualisation assumes 252 trading days. The risk-free rate is taken as zero,
// so Sharpe here is a pure risk-adjusted-return ratio, not a spread over T-bills.
package metrics

import (
	"math"
	"time"
)

const tradingDaysPerYear = 252.0

// Stats summarises one equity curve.
type Stats struct {
	Start        string
	End          string
	Days         int
	InitialValue float64
	FinalValue   float64
	TotalReturn  float64 // FinalValue/InitialValue - 1
	CAGR         float64 // annualised compound growth over the calendar span
	AnnVol       float64 // annualised stdev of daily returns
	Sharpe       float64 // AnnReturn/AnnVol proxy: mean daily / stdev daily * sqrt(252)
	MaxDrawdown  float64 // worst peak-to-trough decline, as a positive fraction
	Trades       int
	Turnover     float64 // total traded notional
	TotalCost    float64 // total costs paid
	Exposure     float64 // fraction of bars holding a non-zero position
}

// Compute derives statistics from a dated equity curve and per-bar weights.
// dates and equity must be the same length (>= 2); weights may be nil.
func Compute(dates []string, equity, weights []float64, trades int, turnover, totalCost float64) Stats {
	n := len(equity)
	st := Stats{
		Trades:    trades,
		Turnover:  turnover,
		TotalCost: totalCost,
		Days:      n,
	}
	if n == 0 {
		return st
	}
	st.Start = dates[0]
	st.End = dates[n-1]
	st.InitialValue = equity[0]
	st.FinalValue = equity[n-1]
	if n < 2 || equity[0] <= 0 {
		return st
	}

	st.TotalReturn = equity[n-1]/equity[0] - 1

	// Daily simple returns.
	rets := make([]float64, 0, n-1)
	for i := 1; i < n; i++ {
		if equity[i-1] > 0 {
			rets = append(rets, equity[i]/equity[i-1]-1)
		}
	}
	mean, sd := meanStdev(rets)
	st.AnnVol = sd * math.Sqrt(tradingDaysPerYear)
	if sd > 0 {
		st.Sharpe = mean / sd * math.Sqrt(tradingDaysPerYear)
	}

	// CAGR over the actual calendar span.
	if years := spanYears(dates[0], dates[n-1]); years > 0 && equity[n-1] > 0 {
		st.CAGR = math.Pow(equity[n-1]/equity[0], 1.0/years) - 1
	}

	st.MaxDrawdown = maxDrawdown(equity)

	if len(weights) == n {
		held := 0
		for _, w := range weights {
			if math.Abs(w) > 1e-9 {
				held++
			}
		}
		st.Exposure = float64(held) / float64(n)
	}

	return st
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

func maxDrawdown(equity []float64) float64 {
	peak := equity[0]
	worst := 0.0
	for _, v := range equity {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (peak - v) / peak
			if dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

const dateLayout = "2006-01-02"

func spanYears(start, end string) float64 {
	s, err1 := time.Parse(dateLayout, start)
	e, err2 := time.Parse(dateLayout, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	return e.Sub(s).Hours() / 24.0 / 365.25
}

// Package analysis computes returns from valuations and cashflows.
//
// It exists to answer one question a balance-only spreadsheet cannot: how much
// of the growth was money added, and how much was the market? Two different
// return measures are needed to separate those, and reporting either one alone
// is misleading:
//
//   - Money-weighted (XIRR) is the return on the money actually invested. It is
//     what a personal balance changed by, but it moves when contributions are
//     timed well or badly, so it is not a verdict on the investments.
//   - Time-weighted (TWR) removes the effect of contribution timing and size, so
//     it measures the portfolio itself. It is what a fund would quote.
//
// The gap between them is precisely the contribution-timing effect - the number
// this package exists to expose.
package analysis

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/akagr/finance-tools/networth/internal/money"
)

// Cashflow is money moving into (positive) or out of (negative) the portfolio.
type Cashflow struct {
	Date   time.Time
	Amount money.Amount
}

// Period is one valuation-to-valuation interval with the flows that occurred
// inside it.
type Period struct {
	Start, End           time.Time
	StartValue, EndValue money.Amount
	Flows                []Cashflow
}

// ErrNoSolution is returned when a return cannot be determined from the data.
var ErrNoSolution = errors.New("no return can be determined from these cashflows")

const daysPerYear = 365.0

// XIRR is the annualised money-weighted return: the discount rate at which the
// contributions, withdrawals and the closing value net to the opening value.
//
// Sign convention, from the investor's point of view: the opening value and any
// contribution are outflows (negative), withdrawals and the closing value are
// inflows (positive).
//
// It is solved by bisection rather than Newton's method. Newton is faster but
// diverges on the irregular, sign-changing flow patterns a real portfolio
// produces; a bracketed bisection either converges or honestly reports that it
// could not, which is the behaviour wanted for a number someone will act on.
func XIRR(flows []Cashflow) (float64, error) {
	if len(flows) < 2 {
		return 0, ErrNoSolution
	}
	cf := append([]Cashflow(nil), flows...)
	sort.Slice(cf, func(i, j int) bool { return cf[i].Date.Before(cf[j].Date) })

	var pos, neg bool
	for _, f := range cf {
		switch {
		case f.Amount > 0:
			pos = true
		case f.Amount < 0:
			neg = true
		}
	}
	// Without flows in both directions there is no rate that makes the series
	// net to zero.
	if !pos || !neg {
		return 0, ErrNoSolution
	}

	t0 := cf[0].Date
	npv := func(rate float64) float64 {
		var sum float64
		for _, f := range cf {
			years := f.Date.Sub(t0).Hours() / 24 / daysPerYear
			sum += f.Amount.Float() / math.Pow(1+rate, years)
		}
		return sum
	}

	lo, hi := -0.9999, 10.0
	flo, fhi := npv(lo), npv(hi)
	if math.IsNaN(flo) || math.IsNaN(fhi) {
		return 0, ErrNoSolution
	}
	// Widen the upper bracket for extraordinary returns before giving up.
	for flo*fhi > 0 && hi < 1e6 {
		hi *= 4
		fhi = npv(hi)
	}
	if flo*fhi > 0 {
		return 0, ErrNoSolution
	}
	for range 200 {
		mid := (lo + hi) / 2
		fmid := npv(mid)
		if math.Abs(fmid) < 1e-9 || hi-lo < 1e-12 {
			return mid, nil
		}
		if flo*fmid < 0 {
			hi = mid
		} else {
			lo, flo = mid, fmid
		}
	}
	return (lo + hi) / 2, nil
}

// ModifiedDietz is the return over a single period, weighting each flow by the
// fraction of the period it was invested for.
//
// It is the right tool when valuations are infrequent. A true time-weighted
// return needs a valuation on every flow date; with balances updated twice a
// year that data does not exist, and pretending otherwise would invent
// precision. Modified Dietz approximates it using only what is actually known,
// and its error is small when flows are modest relative to the portfolio.
func ModifiedDietz(p Period) (float64, error) {
	if !p.End.After(p.Start) {
		return 0, ErrNoSolution
	}
	total := p.End.Sub(p.Start).Hours() / 24
	if total <= 0 {
		return 0, ErrNoSolution
	}
	var netFlow, weighted float64
	for _, f := range p.Flows {
		if f.Date.Before(p.Start) || f.Date.After(p.End) {
			continue
		}
		amt := f.Amount.Float()
		netFlow += amt
		elapsed := f.Date.Sub(p.Start).Hours() / 24
		weighted += amt * (total - elapsed) / total
	}
	denom := p.StartValue.Float() + weighted
	if denom == 0 {
		return 0, ErrNoSolution
	}
	gain := p.EndValue.Float() - p.StartValue.Float() - netFlow
	return gain / denom, nil
}

// TWRResult reports a chained time-weighted return together with the evidence
// behind it, so a caller can never quote the number without also knowing how
// thin the data underneath it was.
type TWRResult struct {
	Total      float64 // cumulative return over the whole span
	Annualised float64
	Periods    int // sub-periods actually used
	Days       float64
	Skipped    int // sub-periods with no usable return
}

// TWR chains per-period returns so that the size and timing of contributions
// cancel out, leaving the performance of the investments themselves.
func TWR(periods []Period) (TWRResult, error) {
	if len(periods) == 0 {
		return TWRResult{}, ErrNoSolution
	}
	sorted := append([]Period(nil), periods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })

	growth := 1.0
	res := TWRResult{}
	for _, p := range sorted {
		r, err := ModifiedDietz(p)
		if err != nil {
			res.Skipped++
			continue
		}
		growth *= 1 + r
		res.Periods++
		res.Days += p.End.Sub(p.Start).Hours() / 24
	}
	if res.Periods == 0 {
		return res, ErrNoSolution
	}
	res.Total = growth - 1
	if res.Days > 0 && growth > 0 {
		res.Annualised = math.Pow(growth, daysPerYear/res.Days) - 1
	}
	return res, nil
}

// Annualise converts a cumulative return over a span of days to a yearly rate.
// Spans under a year are left alone: extrapolating six weeks of performance to a
// yearly figure produces impressive nonsense.
func Annualise(total float64, days float64) (float64, bool) {
	if days <= 0 || 1+total <= 0 {
		return 0, false
	}
	if days < daysPerYear {
		return total, false
	}
	return math.Pow(1+total, daysPerYear/days) - 1, true
}

// Package peak computes the "peak value of investment during the period" for
// each security — the hardest figure in Schedule FA.
//
//   - Mode C (M3): approximate. Without a daily price series, the position is
//     valued at every point where a real market price is known — each trade
//     (pre- and post-fill) and the 31-Dec close — and the max INR value is taken.
//     Exact for buy-and-hold; a documented approximation otherwise.
//   - Mode B (M4): exact. Reconstruct a daily share-count series from trades and
//     value it against a daily price series and that day's TTBR, maximising INR
//     over the calendar year. ComputeExact additionally returns the true
//     portfolio (Table A2) peak: the maximum daily NAV across all holdings.
//
// Peak is maximised in INR (units × price × TTBR), not USD, because the rupee can
// move independently of the share price.
package peak

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

// Mode selects the computation strategy.
type Mode int

const (
	ModeApprox Mode = iota // C — M3 default
	ModeExact              // B — M4, needs a PriceProvider
)

// Result is the peak valuation for one security.
type Result struct {
	Instrument   model.Instrument
	Peak         fx.Conversion // the winning candidate, with its audit trail
	HasValue     bool          // false if no candidate could be valued (no price/rate)
	Approximated bool          // mode C, or mode B with missing-price days → manual-review flag
}

// PriceProvider yields the per-unit close for an instrument on a date (Mode B).
type PriceProvider interface {
	PriceOn(inst model.Instrument, date time.Time) (model.Money, error)
}

// Compute returns the peak value per security for the statement's year.
func Compute(s *model.Statement, store fx.Store, mode Mode, prices PriceProvider) ([]Result, error) {
	if mode == ModeExact {
		secs, _, _, err := ComputeExact(s, store, prices)
		return secs, err
	}
	return computeApprox(s, store), nil
}

// ComputeExact (mode B) returns the per-security peaks and the true portfolio
// peak — the maximum daily NAV (Σ holdings × price × TTBR) over the year, with
// portfolioExact false if any held day was missing a price/rate.
func ComputeExact(s *model.Statement, store fx.Store, prices PriceProvider) (securities []Result, portfolio fx.Conversion, portfolioExact bool, err error) {
	if prices == nil {
		return nil, fx.Conversion{}, false, fmt.Errorf("peak: exact mode (B) requires a price provider")
	}
	holdings, order := groupHoldings(s)
	jan1 := time.Date(s.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(s.Year, time.December, 31, 0, 0, 0, 0, time.UTC)

	// Earliest acquisition date per instrument (from lots): a holding acquired
	// mid-year via a non-trade event (reward, transfer-in) cannot be unwound from
	// trades, so we must not value it before it was actually held.
	acquired := map[string]time.Time{}
	for _, l := range s.Lots {
		k := instKey(l.Instrument)
		a := l.AcquiredOn()
		if a.IsZero() {
			continue
		}
		if e, ok := acquired[k]; !ok || a.Before(e) {
			acquired[k] = a
		}
	}

	type state struct {
		pos     *big.Rat
		ti      int
		best    fx.Conversion
		have    bool
		missing int
	}
	states := make(map[string]*state, len(order))
	for _, k := range order {
		states[k] = &state{pos: new(big.Rat).Set(holdings[k].start)}
	}

	var portBest fx.Conversion
	havePort, portMissing := false, false

	for d := jan1; !d.After(dec31); d = d.AddDate(0, 0, 1) {
		dayNAV := new(big.Rat)
		dayHeld, dayComplete := false, true
		for _, k := range order {
			h := holdings[k]
			st := states[k]
			for st.ti < len(h.trades) && !h.trades[st.ti].Date.After(d) {
				st.pos.Add(st.pos, signedQty(h.trades[st.ti]))
				st.ti++
			}
			if st.pos.Sign() <= 0 {
				continue
			}
			if a, ok := acquired[k]; ok && d.Before(a) {
				continue // not yet held on this day
			}
			dayHeld = true
			price, err := prices.PriceOn(h.inst, d)
			if err != nil || price.Amount == nil || price.Amount.Sign() <= 0 {
				st.missing++
				dayComplete = false
				continue
			}
			val := new(big.Rat).Mul(st.pos, price.Amount)
			conv, cerr := fx.Convert(store, model.NewMoney(price.Currency, val), d)
			if cerr != nil {
				st.missing++
				dayComplete = false
				continue
			}
			if !st.have || conv.Result.Amount.Cmp(st.best.Result.Amount) > 0 {
				st.best, st.have = conv, true
			}
			dayNAV.Add(dayNAV, conv.Result.Amount)
		}
		if dayHeld && !dayComplete {
			portMissing = true
		}
		if dayHeld && dayComplete {
			if !havePort || dayNAV.Cmp(ratOf(portBest.Result)) > 0 {
				portBest = fx.Conversion{Result: model.NewMoney(model.INR, new(big.Rat).Set(dayNAV)), RateDate: d}
				havePort = true
			}
		}
	}

	for _, k := range order {
		st := states[k]
		securities = append(securities, Result{
			Instrument:   holdings[k].inst,
			Peak:         st.best,
			HasValue:     st.have,
			Approximated: st.missing > 0,
		})
	}
	return securities, portBest, havePort && !portMissing, nil
}

func computeApprox(s *model.Statement, store fx.Store) []Result {
	holdings, order := groupHoldings(s)
	yearEnd := time.Date(s.Year, time.December, 31, 0, 0, 0, 0, time.UTC)

	type candidate struct {
		date  time.Time
		units *big.Rat
		price model.Money
	}
	var out []Result
	for _, k := range order {
		h := holdings[k]
		var cands []candidate
		pos := new(big.Rat).Set(h.start)
		for _, t := range h.trades {
			pre := new(big.Rat).Set(pos) // held just before the fill
			pos = new(big.Rat).Add(pos, signedQty(t))
			post := new(big.Rat).Set(pos) // held just after the fill
			cands = append(cands, candidate{t.Date, pre, t.Price}, candidate{t.Date, post, t.Price})
		}
		if h.hasClosing {
			cands = append(cands, candidate{yearEnd, h.closingPos, h.markPrice})
		}
		var best fx.Conversion
		have := false
		for _, c := range cands {
			if c.units.Sign() <= 0 || c.price.Amount == nil || c.price.Amount.Sign() <= 0 {
				continue
			}
			val := new(big.Rat).Mul(c.units, c.price.Amount)
			conv, err := fx.Convert(store, model.NewMoney(c.price.Currency, val), c.date)
			if err != nil {
				continue // no TTBR for this date; mode C is approximate anyway
			}
			if !have || conv.Result.Amount.Cmp(best.Result.Amount) > 0 {
				best, have = conv, true
			}
		}
		out = append(out, Result{Instrument: h.inst, Peak: best, HasValue: have, Approximated: true})
	}
	return out
}

// --- grouping ---

type holding struct {
	inst       model.Instrument
	trades     []model.Trade // sorted ascending by date
	start      *big.Rat      // position as on Jan 1
	closingPos *big.Rat
	markPrice  model.Money
	hasClosing bool
}

// groupHoldings groups positions and trades by instrument and reconstructs each
// holding's Jan-1 opening position by unwinding the year's trades from the
// authoritative year-end snapshot.
func groupHoldings(s *model.Statement) (map[string]*holding, []string) {
	groups := map[string]*holding{}
	var order []string
	get := func(inst model.Instrument) *holding {
		k := instKey(inst)
		h := groups[k]
		if h == nil {
			h = &holding{inst: inst, closingPos: new(big.Rat)}
			groups[k] = h
			order = append(order, k)
		}
		return h
	}
	for _, p := range s.OpenPositions {
		h := get(p.Instrument)
		h.closingPos = p.Quantity
		h.markPrice = p.MarkPrice
		h.hasClosing = true
	}
	for _, t := range s.Trades {
		h := get(t.Instrument)
		h.trades = append(h.trades, t)
	}
	sort.Strings(order)
	for _, k := range order {
		h := groups[k]
		sort.SliceStable(h.trades, func(i, j int) bool { return h.trades[i].Date.Before(h.trades[j].Date) })
		start := new(big.Rat).Set(h.closingPos)
		for _, t := range h.trades {
			start.Sub(start, signedQty(t))
		}
		h.start = start
	}
	return groups, order
}

func signedQty(t model.Trade) *big.Rat {
	q := new(big.Rat).Abs(t.Quantity)
	if t.Side == model.Sell {
		return q.Neg(q)
	}
	return q
}

func instKey(in model.Instrument) string {
	if in.ISIN != "" {
		return "isin:" + in.ISIN
	}
	return "sym:" + in.Symbol
}

func ratOf(m model.Money) *big.Rat {
	if m.Amount == nil {
		return new(big.Rat)
	}
	return m.Amount
}

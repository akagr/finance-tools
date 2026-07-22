// Package session orchestrates one paper-trading step: given an account and the
// latest price history, it asks the strategy for a target weight, rebalances the
// paper portfolio toward it through the paper broker, and records the fill. It is
// the forward-in-time analogue of the backtester's inner loop, but stateful and
// driven one bar at a time by real, freshly-fetched data.
package session

import (
	"fmt"
	"math"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/broker"
	"github.com/akagr/finance-tools/papertrade/internal/store"
)

// Bar is one dated close price.
type Bar struct {
	Date  string
	Close float64
}

// minTradeFraction mirrors the backtester's no-trade band: skip a rebalance
// smaller than 1% of equity, so tiny drifts don't churn.
const minTradeFraction = 0.01

// StepResult summarises what a step did.
type StepResult struct {
	Traded      bool
	Skipped     bool   // no new bar since the last step
	Reason      string // why skipped, when Skipped
	Date        string
	Quote       float64
	TargetWt    float64
	PriorWt     float64
	Fill        *broker.Fill
	EquityAfter float64
}

// Step advances the account using bars (ascending by date, last = latest). It
// mutates a and, when a trade occurs, appends to the log via st. If the latest
// bar has already been processed (same date as a.LastBarDate) it is a no-op
// unless force is set — this makes running Step idempotent within a day.
func Step(a *account.Account, bars []Bar, force bool, st *store.Store, now time.Time) (StepResult, error) {
	if len(bars) < 2 {
		return StepResult{}, fmt.Errorf("session: need >=2 bars, got %d", len(bars))
	}
	latest := bars[len(bars)-1]
	if latest.Close <= 0 {
		return StepResult{}, fmt.Errorf("session: latest bar has non-positive close")
	}
	if !force && a.LastBarDate == latest.Date {
		return StepResult{Skipped: true, Reason: "no new bar since last step", Date: latest.Date, Quote: latest.Close}, nil
	}

	strat, err := a.BuildStrategy()
	if err != nil {
		return StepResult{}, err
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	target := strat.Target(closes)
	if target < 0 {
		target = 0
	} else if target > 1 {
		target = 1
	}

	price := latest.Close
	priorWt := a.Portfolio.Weight(price)
	equity := a.Portfolio.Equity(price)
	targetShares := target * equity / price
	delta := targetShares - a.Portfolio.Shares

	res := StepResult{Date: latest.Date, Quote: price, TargetWt: target, PriorWt: priorWt}

	if math.Abs(delta*price) <= minTradeFraction*equity {
		// Within the no-trade band: just advance the processed-bar marker.
		a.LastBarDate = latest.Date
		a.UpdatedAt = now
		res.EquityAfter = a.Portfolio.Equity(price)
		return res, nil
	}

	br := broker.New(a.Costs)
	var fill broker.Fill
	if delta > 0 {
		// Affordability cap: buying pays slippage and fees on top of the notional,
		// so a 100% target can't spend 100% of cash. Limit the buy to what cash
		// covers net of those frictions, leaving the account non-negative.
		slip := br.Costs.SlippageBps / 10000.0
		feeFrac := (br.Costs.BrokerageBps + br.Costs.STTBps) / 10000.0
		perShare := price * (1 + slip) * (1 + feeFrac)
		if perShare > 0 {
			if maxAffordable := a.Portfolio.Cash / perShare; delta > maxAffordable {
				delta = maxAffordable
			}
		}
		if delta <= 0 {
			a.LastBarDate = latest.Date
			a.UpdatedAt = now
			res.EquityAfter = a.Portfolio.Equity(price)
			return res, nil
		}
		fill = br.Execute(broker.Buy, delta, price)
		a.Portfolio.Buy(fill.Shares, fill.Price, fill.Cost)
	} else {
		fill = br.Execute(broker.Sell, -delta, price)
		a.Portfolio.Sell(fill.Shares, fill.Price, fill.Cost)
	}

	a.LastBarDate = latest.Date
	a.UpdatedAt = now
	equityAfter := a.Portfolio.Equity(price)

	res.Traded = true
	res.Fill = &fill
	res.EquityAfter = equityAfter

	if st != nil {
		if err := st.AppendLog(store.LogEntry{
			Date:        latest.Date,
			Time:        now,
			Fill:        fill,
			Quote:       price,
			TargetWt:    target,
			CashAfter:   a.Portfolio.Cash,
			SharesAfter: a.Portfolio.Shares,
			EquityAfter: equityAfter,
		}); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Package broker simulates order execution for paper trading. A PaperBroker
// fills market orders immediately at the quoted price plus a slippage penalty,
// and charges the same brokerage/STT/slippage costs the backtester models, so
// paper results line up with backtests. This is the seam a real broker
// implementation would slot into for Phase 4 (live trading).
package broker

import "math"

// Costs are per-trade frictions in basis points (1 bp = 0.01%), charged on the
// absolute notional of each fill. Mirrors the backtest engine's cost model.
type Costs struct {
	BrokerageBps float64 `json:"brokerage_bps"`
	STTBps       float64 `json:"stt_bps"`
	SlippageBps  float64 `json:"slippage_bps"`
}

// DefaultCosts is the conservative NSE cash-delivery approximation used across
// the tools: no flat brokerage, 0.1% STT and 5 bps slippage per side.
func DefaultCosts() Costs {
	return Costs{BrokerageBps: 0, STTBps: 10, SlippageBps: 5}
}

// Side is the direction of a fill.
type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Fill is the result of executing one order.
type Fill struct {
	Side     Side    `json:"side"`
	Shares   float64 `json:"shares"`
	Price    float64 `json:"price"`    // effective fill price incl. slippage
	Notional float64 `json:"notional"` // shares * price
	Cost     float64 `json:"cost"`     // brokerage + STT charged (slippage is in price)
}

// PaperBroker fills orders against a quoted price with modelled friction.
type PaperBroker struct {
	Costs Costs
}

// New returns a PaperBroker with the given costs (zero value uses DefaultCosts).
func New(c Costs) *PaperBroker {
	if c == (Costs{}) {
		c = DefaultCosts()
	}
	return &PaperBroker{Costs: c}
}

// Execute fills a market order for `shares` (always positive) on the given side
// at the quoted price. Slippage moves the fill price against the trader (up when
// buying, down when selling); brokerage and STT are charged on the notional.
func (b *PaperBroker) Execute(side Side, shares, quote float64) Fill {
	slip := b.Costs.SlippageBps / 10000.0
	price := quote
	switch side {
	case Buy:
		price = quote * (1 + slip)
	case Sell:
		price = quote * (1 - slip)
	}
	notional := shares * price
	feeBps := b.Costs.BrokerageBps + b.Costs.STTBps
	cost := math.Abs(notional) * feeBps / 10000.0
	return Fill{Side: side, Shares: shares, Price: price, Notional: notional, Cost: cost}
}

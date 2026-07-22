// Package portfolio tracks a paper-trading account's cash and single-asset
// position, marks it to market, and reports profit and loss. Money is float64,
// like the sibling backtest/correlation research tools — a paisa of rounding in a
// *simulated* account is immaterial (this is not the exact-money tax path).
package portfolio

// Portfolio is cash plus a fractional position in one asset.
type Portfolio struct {
	Cash   float64 `json:"cash"`
	Shares float64 `json:"shares"`
	// CostBasis is the total cash spent acquiring the current shares (net of
	// what has been recovered by sells), used to split P&L into realised and
	// unrealised. It is 0 when flat.
	CostBasis float64 `json:"cost_basis"`
	// Realised accumulates profit locked in by sells over the account's life.
	Realised float64 `json:"realised_pnl"`
}

// New returns a portfolio holding only cash.
func New(cash float64) Portfolio {
	return Portfolio{Cash: cash}
}

// Equity is the mark-to-market value at the given price: cash plus position.
func (p Portfolio) Equity(price float64) float64 {
	return p.Cash + p.Shares*price
}

// Weight is the fraction of equity currently in the asset (0..1 for long-only).
func (p Portfolio) Weight(price float64) float64 {
	eq := p.Equity(price)
	if eq <= 0 {
		return 0
	}
	return p.Shares * price / eq
}

// Unrealised is the open position's paper gain at the given price.
func (p Portfolio) Unrealised(price float64) float64 {
	return p.Shares*price - p.CostBasis
}

// AvgCost is the average price paid for the current shares, or 0 when flat.
func (p Portfolio) AvgCost() float64 {
	if p.Shares <= 0 {
		return 0
	}
	return p.CostBasis / p.Shares
}

// Buy adds shares at price and charges cost from cash, growing the cost basis.
func (p *Portfolio) Buy(shares, price, cost float64) {
	p.Cash -= shares*price + cost
	p.Shares += shares
	p.CostBasis += shares * price
}

// Sell removes shares at price, crediting cash net of cost. The realised P&L is
// the proceeds minus the average-cost portion of the shares sold; the cost basis
// shrinks proportionally.
func (p *Portfolio) Sell(shares, price, cost float64) {
	avg := p.AvgCost()
	p.Cash += shares*price - cost
	p.Realised += shares*(price-avg) - cost
	p.CostBasis -= shares * avg
	p.Shares -= shares
	if p.Shares < 1e-9 {
		// Clean up floating dust when fully flat.
		p.Shares = 0
		p.CostBasis = 0
	}
}

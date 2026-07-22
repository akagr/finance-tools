// Package strategy defines the pluggable trading-rule interface and the built-in
// strategies. A strategy returns a target portfolio weight in [0, 1] (long-only)
// for the current bar; the engine translates that into trades and applies costs.
//
// The engine calls Target once per bar in chronological order. Most strategies
// are pure functions of the price history handed to them; a few (e.g. Donchian)
// carry position state across those calls to model entry/exit hysteresis. Either
// way a strategy must never read beyond the slice it is given — no lookahead is
// what keeps a backtest honest. Stateful strategies are single-use: run one
// through the engine exactly once and discard it.
package strategy

// Strategy decides how much of the portfolio to hold in the asset.
type Strategy interface {
	// Name identifies the strategy in reports.
	Name() string
	// Target returns the desired weight in [0, 1] given closes[0..now], where
	// the last element is the current bar's close. It is invoked once per bar in
	// order and must never look past the end of the slice.
	Target(closes []float64) float64
}

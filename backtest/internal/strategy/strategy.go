// Package strategy defines the pluggable trading-rule interface and the starter
// strategies. A strategy is a pure function of price history up to and including
// the current bar; it returns a target portfolio weight in [0, 1] (long-only).
// The engine translates target weight into trades and applies costs. Keeping
// strategies pure — no access to future bars — is what prevents lookahead bias.
package strategy

import "fmt"

// Strategy decides how much of the portfolio to hold in the asset.
type Strategy interface {
	// Name identifies the strategy in reports.
	Name() string
	// Target returns the desired weight in [0, 1] given closes[0..now], where
	// the last element is the current bar's close. It must never look past the
	// end of the slice.
	Target(closes []float64) float64
}

// BuyHold is fully invested from the first bar: the benchmark every active
// strategy must beat after costs to be worth running.
type BuyHold struct{}

func (BuyHold) Name() string                    { return "buy-hold" }
func (BuyHold) Target(closes []float64) float64 { return 1.0 }

// SMACross is a long/flat moving-average crossover: hold the asset while the
// fast simple moving average is above the slow one, otherwise sit in cash. It is
// the textbook trend-following rule — useful precisely because it so often fails
// to beat buy-and-hold after costs.
type SMACross struct {
	Fast int
	Slow int
}

// NewSMACross validates the windows (fast must be a positive number strictly
// below slow) and returns the strategy.
func NewSMACross(fast, slow int) (*SMACross, error) {
	if fast < 1 || slow < 1 {
		return nil, fmt.Errorf("strategy: SMA windows must be >= 1 (got fast=%d slow=%d)", fast, slow)
	}
	if fast >= slow {
		return nil, fmt.Errorf("strategy: fast window (%d) must be < slow window (%d)", fast, slow)
	}
	return &SMACross{Fast: fast, Slow: slow}, nil
}

func (s *SMACross) Name() string { return fmt.Sprintf("sma-cross(%d/%d)", s.Fast, s.Slow) }

func (s *SMACross) Target(closes []float64) float64 {
	// Not enough history to form the slow average yet: stay flat.
	if len(closes) < s.Slow {
		return 0.0
	}
	if sma(closes, s.Fast) > sma(closes, s.Slow) {
		return 1.0
	}
	return 0.0
}

// sma is the mean of the last n closes.
func sma(closes []float64, n int) float64 {
	if n <= 0 || len(closes) < n {
		return 0
	}
	sum := 0.0
	for _, c := range closes[len(closes)-n:] {
		sum += c
	}
	return sum / float64(n)
}

package strategy

import "fmt"

// Momentum is time-series (absolute) momentum: hold the asset while it is above
// its own price Lookback bars ago, otherwise sit in cash. It is the simplest
// expression of "the trend is your friend" and, unlike a moving-average cross,
// keys off a single past price rather than a smoothed average.
type Momentum struct {
	Lookback int
}

// NewMomentum validates the lookback window.
func NewMomentum(lookback int) (*Momentum, error) {
	if lookback < 1 {
		return nil, fmt.Errorf("strategy: momentum lookback must be >= 1 (got %d)", lookback)
	}
	return &Momentum{Lookback: lookback}, nil
}

func (s *Momentum) Name() string { return fmt.Sprintf("momentum(%d)", s.Lookback) }

func (s *Momentum) Target(closes []float64) float64 {
	if len(closes) <= s.Lookback {
		return 0.0
	}
	last := len(closes) - 1
	if closes[last] > closes[last-s.Lookback] {
		return 1.0
	}
	return 0.0
}

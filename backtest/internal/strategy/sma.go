package strategy

import "fmt"

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

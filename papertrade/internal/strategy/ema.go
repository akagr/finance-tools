package strategy

import "fmt"

// EMACross is the exponential-moving-average analogue of SMACross: hold the
// asset while the fast EMA is above the slow EMA. EMAs weight recent prices more
// heavily than SMAs, so the rule turns a little sooner — at the cost of more
// whipsaws in choppy markets.
type EMACross struct {
	Fast int
	Slow int
}

// NewEMACross validates the windows (fast positive and strictly below slow).
func NewEMACross(fast, slow int) (*EMACross, error) {
	if fast < 1 || slow < 1 {
		return nil, fmt.Errorf("strategy: EMA windows must be >= 1 (got fast=%d slow=%d)", fast, slow)
	}
	if fast >= slow {
		return nil, fmt.Errorf("strategy: fast window (%d) must be < slow window (%d)", fast, slow)
	}
	return &EMACross{Fast: fast, Slow: slow}, nil
}

func (s *EMACross) Name() string { return fmt.Sprintf("ema-cross(%d/%d)", s.Fast, s.Slow) }

func (s *EMACross) Target(closes []float64) float64 {
	if len(closes) < s.Slow {
		return 0.0
	}
	if ema(closes, s.Fast) > ema(closes, s.Slow) {
		return 1.0
	}
	return 0.0
}

// ema is the exponential moving average of closes with span n (smoothing factor
// 2/(n+1)), seeded at the first close.
func ema(closes []float64, n int) float64 {
	if n <= 0 || len(closes) == 0 {
		return 0
	}
	k := 2.0 / (float64(n) + 1.0)
	e := closes[0]
	for _, c := range closes[1:] {
		e = c*k + e*(1-k)
	}
	return e
}

package strategy

import "fmt"

// RSI is a contrarian mean-reversion rule built on the Relative Strength Index:
// buy when the asset is oversold (RSI below Threshold) and step aside otherwise.
// Where the trend strategies buy strength, this buys weakness — a deliberately
// different edge for seeing how strategy style interacts with a market's
// character.
type RSI struct {
	Period    int
	Threshold float64
}

// NewRSI validates the period and the (0, 100) threshold.
func NewRSI(period int, threshold float64) (*RSI, error) {
	if period < 1 {
		return nil, fmt.Errorf("strategy: RSI period must be >= 1 (got %d)", period)
	}
	if threshold <= 0 || threshold >= 100 {
		return nil, fmt.Errorf("strategy: RSI threshold must be in (0, 100) (got %v)", threshold)
	}
	return &RSI{Period: period, Threshold: threshold}, nil
}

func (s *RSI) Name() string { return fmt.Sprintf("rsi(%d<%g)", s.Period, s.Threshold) }

func (s *RSI) Target(closes []float64) float64 {
	if len(closes) <= s.Period {
		return 0.0
	}
	if rsi(closes, s.Period) < s.Threshold {
		return 1.0
	}
	return 0.0
}

// rsi computes the Relative Strength Index over the last `period` price changes
// using a simple average of gains and losses (Cutler's RSI). It returns a value
// in [0, 100]: 0 when every change was a loss, 100 when every change was a gain.
func rsi(closes []float64, period int) float64 {
	n := len(closes)
	if period < 1 || n <= period {
		return 50 // neutral when undefined
	}
	var gain, loss float64
	for i := n - period; i < n; i++ {
		ch := closes[i] - closes[i-1]
		if ch >= 0 {
			gain += ch
		} else {
			loss -= ch
		}
	}
	if loss == 0 {
		if gain == 0 {
			return 50
		}
		return 100
	}
	rs := gain / loss
	return 100 - 100/(1+rs)
}

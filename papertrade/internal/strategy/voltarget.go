package strategy

import (
	"fmt"
	"math"
)

// annualisation assumes 252 trading days, matching the metrics package.
const tradingDaysPerYear = 252.0

// VolTarget wraps another strategy and scales its target weight so the position's
// trailing realised volatility approaches TargetVol. It is a risk-management
// overlay, not a signal: the inner strategy decides *whether* to be in the
// market; VolTarget decides *how much*, trimming the position when the asset is
// turbulent and restoring it when calm.
//
// It is long-only and never levers up: the scale is capped at 1, so VolTarget
// can only *reduce* the inner weight (matching a retail cash account with no
// margin). In calm markets, where realised vol is already below target, it holds
// the inner weight unchanged. Because the buy-and-hold benchmark is left
// unwrapped, a vol-targeted strategy is compared honestly against plain 100%.
//
// Like the inner strategy, VolTarget reads only the closes it is given (no
// lookahead): realised vol is estimated from the trailing Lookback returns. It
// is stateless — how often the resulting fractional weight is actually traded is
// the engine's rebalance policy, not the overlay's concern.
type VolTarget struct {
	Inner     Strategy
	TargetVol float64 // annualised target, e.g. 0.10 for 10%
	Lookback  int     // trailing return observations used to estimate realised vol
}

// NewVolTarget validates the overlay parameters and wraps inner.
func NewVolTarget(inner Strategy, targetVol float64, lookback int) (*VolTarget, error) {
	if inner == nil {
		return nil, fmt.Errorf("strategy: VolTarget needs an inner strategy")
	}
	if targetVol <= 0 {
		return nil, fmt.Errorf("strategy: vol target must be > 0 (got %v)", targetVol)
	}
	if lookback < 2 {
		return nil, fmt.Errorf("strategy: vol lookback must be >= 2 (got %d)", lookback)
	}
	return &VolTarget{Inner: inner, TargetVol: targetVol, Lookback: lookback}, nil
}

func (v *VolTarget) Name() string {
	return fmt.Sprintf("%s+voltgt(%g%%/%d)", v.Inner.Name(), v.TargetVol*100, v.Lookback)
}

func (v *VolTarget) Target(closes []float64) float64 {
	w := v.Inner.Target(closes)
	if w <= 0 {
		return 0
	}
	rv := realisedVol(closes, v.Lookback)
	if rv <= 0 {
		// Not enough history, or a perfectly flat window: leave the weight as-is
		// rather than divide by zero.
		return w
	}
	scale := v.TargetVol / rv
	if scale > 1 {
		scale = 1 // no leverage: only ever trim the position
	}
	return w * scale
}

// realisedVol is the annualised standard deviation of the last `lookback` daily
// simple returns of closes, or 0 when there is not enough history.
func realisedVol(closes []float64, lookback int) float64 {
	if lookback < 2 || len(closes) <= lookback {
		return 0
	}
	rets := make([]float64, 0, lookback)
	for i := len(closes) - lookback; i < len(closes); i++ {
		if closes[i-1] > 0 {
			rets = append(rets, closes[i]/closes[i-1]-1)
		}
	}
	if len(rets) < 2 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var ss float64
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	sd := math.Sqrt(ss / float64(len(rets)-1))
	return sd * math.Sqrt(tradingDaysPerYear)
}

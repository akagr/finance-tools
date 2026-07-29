package strategy

import "fmt"

// Dip is a contrarian "buy the dip" rule: hold the asset whenever the current
// close sits at least Drop percent below its running all-time high, otherwise
// sit in cash. The idea is to be invested only after a pullback from the peak
// and to step aside once price has recovered back to (or near) a fresh high.
//
// Unlike RSI it measures weakness against the all-time high rather than recent
// average gains/losses, so it stays long through an extended drawdown until a
// new high is reclaimed. The running peak is computed only from prices up to the
// current bar, so there is no lookahead.
type Dip struct {
	Drop float64 // percent below the running peak that triggers a buy (e.g. 1 = 1%)
}

// NewDip validates the drop threshold, which must be a positive percentage.
func NewDip(drop float64) (*Dip, error) {
	if drop <= 0 {
		return nil, fmt.Errorf("strategy: dip drop must be > 0 (got %v)", drop)
	}
	return &Dip{Drop: drop}, nil
}

func (s *Dip) Name() string { return fmt.Sprintf("dip(%g%%)", s.Drop) }

func (s *Dip) Target(closes []float64) float64 {
	if len(closes) == 0 {
		return 0.0
	}
	peak := maxOf(closes)
	last := closes[len(closes)-1]
	// Long once the close is at least Drop% below the running all-time high.
	if last <= peak*(1-s.Drop/100) {
		return 1.0
	}
	return 0.0
}

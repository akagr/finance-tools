package strategy

import "fmt"

// Donchian is a channel-breakout trend rule (the classic "Turtle" entry): go
// long when the close breaks above the highest close of the prior Entry bars,
// and exit to cash when it breaks below the lowest close of the prior Exit bars.
// Between those events it holds its position, so — unlike the stateless rules —
// it must remember whether it is currently in or out. The engine calls Target
// once per bar in order, which lets it carry that state across calls.
//
// A Donchian value is therefore single-use: run it through the engine exactly
// once and discard it; reusing an instance would leak stale position state.
type Donchian struct {
	Entry int
	Exit  int
	pos   float64 // current weight (0 or 1), carried across bars
}

// NewDonchian validates the entry/exit windows.
func NewDonchian(entry, exit int) (*Donchian, error) {
	if entry < 1 || exit < 1 {
		return nil, fmt.Errorf("strategy: Donchian windows must be >= 1 (got entry=%d exit=%d)", entry, exit)
	}
	return &Donchian{Entry: entry, Exit: exit}, nil
}

func (s *Donchian) Name() string { return fmt.Sprintf("donchian(%d/%d)", s.Entry, s.Exit) }

func (s *Donchian) Target(closes []float64) float64 {
	i := len(closes) - 1
	// Enter on a breakout above the prior Entry-bar high.
	if i >= s.Entry && closes[i] > maxOf(closes[i-s.Entry:i]) {
		s.pos = 1
	}
	// Exit on a breakdown below the prior Exit-bar low (evaluated last, so a bar
	// that is somehow both a breakout and a breakdown ends flat).
	if i >= s.Exit && closes[i] < minOf(closes[i-s.Exit:i]) {
		s.pos = 0
	}
	return s.pos
}

func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

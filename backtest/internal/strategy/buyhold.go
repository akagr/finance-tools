package strategy

// BuyHold is fully invested from the first bar: the benchmark every active
// strategy must beat after costs to be worth running.
type BuyHold struct{}

func (BuyHold) Name() string                    { return "buy-hold" }
func (BuyHold) Target(closes []float64) float64 { return 1.0 }

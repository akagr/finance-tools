package metrics

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestTotalReturnAndFinal(t *testing.T) {
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03"}
	equity := []float64{100, 110, 121}
	st := Compute(dates, equity, nil, 1, 0, 0)
	if !approx(st.TotalReturn, 0.21, 1e-9) {
		t.Errorf("TotalReturn = %v, want 0.21", st.TotalReturn)
	}
	if st.FinalValue != 121 || st.InitialValue != 100 {
		t.Errorf("Initial/Final = %v/%v", st.InitialValue, st.FinalValue)
	}
	if st.Start != dates[0] || st.End != dates[2] {
		t.Errorf("Start/End = %s/%s", st.Start, st.End)
	}
}

func TestMaxDrawdown(t *testing.T) {
	// Peak 120, trough 90 → drawdown 25%.
	equity := []float64{100, 120, 90, 100}
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04"}
	st := Compute(dates, equity, nil, 0, 0, 0)
	if !approx(st.MaxDrawdown, 0.25, 1e-9) {
		t.Errorf("MaxDrawdown = %v, want 0.25", st.MaxDrawdown)
	}
}

func TestExposure(t *testing.T) {
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04"}
	equity := []float64{100, 101, 102, 103}
	weights := []float64{0, 1, 1, 0} // invested on 2 of 4 bars
	st := Compute(dates, equity, weights, 2, 0, 0)
	if !approx(st.Exposure, 0.5, 1e-9) {
		t.Errorf("Exposure = %v, want 0.5", st.Exposure)
	}
}

func TestCAGRDoublingInOneYear(t *testing.T) {
	// Value doubles over ~one calendar year → CAGR ≈ 100%.
	dates := []string{"2023-01-01", "2024-01-01"}
	equity := []float64{100, 200}
	st := Compute(dates, equity, nil, 1, 0, 0)
	if !approx(st.CAGR, 1.0, 0.02) {
		t.Errorf("CAGR = %v, want ~1.0", st.CAGR)
	}
}

func TestConstantCurveHasZeroVolAndSharpe(t *testing.T) {
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03"}
	equity := []float64{100, 100, 100}
	st := Compute(dates, equity, nil, 0, 0, 0)
	if st.AnnVol != 0 || st.Sharpe != 0 {
		t.Errorf("flat curve: AnnVol=%v Sharpe=%v, want 0/0", st.AnnVol, st.Sharpe)
	}
}

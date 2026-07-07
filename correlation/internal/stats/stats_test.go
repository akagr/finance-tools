package stats

import (
	"math"
	"testing"
)

const tol = 1e-9

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputePerfectPositive(t *testing.T) {
	// y = 2x + 1 is a perfect positive linear relationship => r = 1.
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{3, 5, 7, 9, 11}
	res, err := Compute([]string{"x", "y"}, [][]float64{x, y})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, res.Correlation[0][1], 1)
	approx(t, res.Correlation[1][0], 1)
	approx(t, res.Correlation[0][0], 1)
	if res.N != 5 {
		t.Fatalf("N = %d, want 5", res.N)
	}
}

func TestComputePerfectNegative(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{10, 8, 6, 4, 2}
	res, err := Compute([]string{"x", "y"}, [][]float64{x, y})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, res.Correlation[0][1], -1)
}

func TestComputeKnownValue(t *testing.T) {
	// x={1,2,3,4,5}, y={2,4,5,4,5}: cov num = 6, Σdx²=10, Σdy²=6,
	// so r = 6/√60 = 0.77459666...
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{2, 4, 5, 4, 5}
	res, err := Compute([]string{"x", "y"}, [][]float64{x, y})
	if err != nil {
		t.Fatal(err)
	}
	want := 6.0 / math.Sqrt(60)
	if math.Abs(res.Correlation[0][1]-want) > 1e-9 {
		t.Fatalf("r = %v, want ~%v", res.Correlation[0][1], want)
	}
}

func TestComputeConstantSeriesIsNaN(t *testing.T) {
	x := []float64{1, 2, 3, 4}
	c := []float64{7, 7, 7, 7}
	res, err := Compute([]string{"x", "c"}, [][]float64{x, c})
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(res.Correlation[0][1]) {
		t.Fatalf("want NaN correlation with a constant series, got %v", res.Correlation[0][1])
	}
}

func TestComputeMatrixSymmetryThreeAssets(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5, 6}
	b := []float64{2, 1, 4, 3, 6, 5}
	c := []float64{6, 5, 4, 3, 2, 1}
	res, err := Compute([]string{"a", "b", "c"}, [][]float64{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		approx(t, res.Correlation[i][i], 1)
		for j := 0; j < 3; j++ {
			if math.Abs(res.Correlation[i][j]-res.Correlation[j][i]) > tol {
				t.Errorf("matrix not symmetric at (%d,%d)", i, j)
			}
		}
	}
	if len(res.Pairs) != 3 { // ab, ac, bc
		t.Fatalf("pairs = %d, want 3", len(res.Pairs))
	}
}

func TestFisherCI95BracketsR(t *testing.T) {
	// With enough observations the CI should bracket r and be finite.
	x := make([]float64, 30)
	y := make([]float64, 30)
	for i := range x {
		x[i] = float64(i)
		y[i] = float64(i)*0.8 + float64((i*7)%5) // noisy positive relationship
	}
	res, err := Compute([]string{"x", "y"}, [][]float64{x, y})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Pairs[0]
	if math.IsNaN(p.CI95Lo) || math.IsNaN(p.CI95Hi) {
		t.Fatalf("expected finite CI, got [%v,%v]", p.CI95Lo, p.CI95Hi)
	}
	if !(p.CI95Lo <= p.R && p.R <= p.CI95Hi) {
		t.Fatalf("CI [%v,%v] does not bracket r=%v", p.CI95Lo, p.CI95Hi, p.R)
	}
}

func TestComputeValidation(t *testing.T) {
	if _, err := Compute([]string{"x"}, [][]float64{{1, 2}}); err == nil {
		t.Error("want error for <2 assets")
	}
	if _, err := Compute([]string{"x", "y"}, [][]float64{{1}, {2}}); err == nil {
		t.Error("want error for <2 observations")
	}
	if _, err := Compute([]string{"x", "y"}, [][]float64{{1, 2, 3}, {2, 3}}); err == nil {
		t.Error("want error for unequal length")
	}
}

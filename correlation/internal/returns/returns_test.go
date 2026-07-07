package returns

import (
	"math"
	"testing"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/align"
)

func date(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func TestComputeLog(t *testing.T) {
	a := align.Aligned{
		Labels: []string{"A", "B"},
		Dates:  []time.Time{date("2024-01-01"), date("2024-01-08"), date("2024-01-15")},
		Closes: [][]float64{
			{100, 110, 121},
			{50, 40, 60},
		},
	}
	got, err := Compute(a, Log)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Series[0]) != 2 || len(got.EndDates) != 2 {
		t.Fatalf("lengths = %d/%d, want 2", len(got.Series[0]), len(got.EndDates))
	}
	if math.Abs(got.Series[0][0]-math.Log(1.1)) > 1e-12 {
		t.Fatalf("log return = %v, want %v", got.Series[0][0], math.Log(1.1))
	}
}

func TestComputeSimple(t *testing.T) {
	a := align.Aligned{
		Labels: []string{"A"},
		Dates:  []time.Time{date("2024-01-01"), date("2024-01-08")},
		Closes: [][]float64{{100, 110}},
	}
	got, err := Compute(a, Simple)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Series[0][0]-0.1) > 1e-12 {
		t.Fatalf("simple return = %v, want 0.1", got.Series[0][0])
	}
}

func TestComputeNeedsTwoPeriods(t *testing.T) {
	a := align.Aligned{
		Labels: []string{"A"},
		Dates:  []time.Time{date("2024-01-01")},
		Closes: [][]float64{{100}},
	}
	if _, err := Compute(a, Log); err == nil {
		t.Fatal("want error for <2 periods")
	}
}

func TestParseKind(t *testing.T) {
	if _, err := ParseKind("log"); err != nil {
		t.Error(err)
	}
	if _, err := ParseKind("simple"); err != nil {
		t.Error(err)
	}
	if _, err := ParseKind("geometric"); err == nil {
		t.Error("want error for unknown kind")
	}
}

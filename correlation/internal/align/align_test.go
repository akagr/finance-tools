package align

import (
	"testing"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/series"
)

func date(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func mkSeries(label, cur string, pts ...[2]interface{}) series.Series {
	s := series.Series{Label: label, Currency: cur}
	for _, p := range pts {
		s.Points = append(s.Points, series.Point{Date: date(p[0].(string)), Close: p[1].(float64)})
	}
	return s
}

func TestBuildWeeklyResampleAndIntersect(t *testing.T) {
	// Two ISO weeks; weekly resample keeps the last close per week.
	a := mkSeries("A", "USD",
		[2]interface{}{"2024-01-02", 10.0}, // week 1
		[2]interface{}{"2024-01-04", 11.0}, // week 1 (last)
		[2]interface{}{"2024-01-09", 12.0}, // week 2
	)
	b := mkSeries("B", "USD",
		[2]interface{}{"2024-01-03", 20.0}, // week 1
		[2]interface{}{"2024-01-05", 22.0}, // week 1 (last)
		[2]interface{}{"2024-01-10", 24.0}, // week 2
	)
	got, err := Build([]series.Series{a, b}, Weekly)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dates) != 2 {
		t.Fatalf("periods = %d, want 2", len(got.Dates))
	}
	// Week 1 last closes: A=11, B=22.
	if got.Closes[0][0] != 11 || got.Closes[1][0] != 22 {
		t.Fatalf("week1 closes = %v/%v", got.Closes[0][0], got.Closes[1][0])
	}
	if got.Closes[0][1] != 12 || got.Closes[1][1] != 24 {
		t.Fatalf("week2 closes = %v/%v", got.Closes[0][1], got.Closes[1][1])
	}
}

func TestBuildIntersectsToCommonPeriods(t *testing.T) {
	a := mkSeries("A", "USD",
		[2]interface{}{"2024-01-02", 10.0},
		[2]interface{}{"2024-01-09", 12.0},
		[2]interface{}{"2024-01-16", 13.0},
	)
	b := mkSeries("B", "USD",
		[2]interface{}{"2024-01-09", 20.0},
		[2]interface{}{"2024-01-16", 21.0}, // no week-1 data for B
	)
	got, err := Build([]series.Series{a, b}, Weekly)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dates) != 2 { // only weeks 2 and 3 are common
		t.Fatalf("periods = %d, want 2", len(got.Dates))
	}
	// Dates must be ascending.
	if !got.Dates[0].Before(got.Dates[1]) {
		t.Fatal("dates not ascending")
	}
}

func TestBuildMonthly(t *testing.T) {
	a := mkSeries("A", "USD",
		[2]interface{}{"2024-01-10", 10.0},
		[2]interface{}{"2024-01-31", 11.0}, // Jan last
		[2]interface{}{"2024-02-15", 12.0}, // Feb last
	)
	b := mkSeries("B", "USD",
		[2]interface{}{"2024-01-05", 20.0},
		[2]interface{}{"2024-02-20", 22.0},
	)
	got, err := Build([]series.Series{a, b}, Monthly)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dates) != 2 {
		t.Fatalf("periods = %d, want 2", len(got.Dates))
	}
	if got.Closes[0][0] != 11 { // Jan last for A
		t.Fatalf("Jan close A = %v, want 11", got.Closes[0][0])
	}
}

func TestBuildNoCommonPeriods(t *testing.T) {
	a := mkSeries("A", "USD", [2]interface{}{"2024-01-02", 10.0})
	b := mkSeries("B", "USD", [2]interface{}{"2024-03-02", 20.0})
	if _, err := Build([]series.Series{a, b}, Weekly); err == nil {
		t.Fatal("want error for no common periods")
	}
}

func TestParseFrequency(t *testing.T) {
	for _, ok := range []string{"daily", "weekly", "monthly"} {
		if _, err := ParseFrequency(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if _, err := ParseFrequency("yearly"); err == nil {
		t.Error("want error for yearly")
	}
}

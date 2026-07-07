// Package fxconv converts price series into a common base currency so that
// correlations reflect the return an investor in that base currency actually
// experiences (asset co-movement plus the FX co-movement that rides along).
//
// FX CSV columns (header required; order-independent):
//
//	date,currency,rate
//	2024-06-14,USD,83.55
//
// `rate` is the value of one unit of `currency` in the base currency (e.g. INR
// per USD). Lookups use preceding-available-day fallback, mirroring the TTBR/
// price lookups in schedule-fa (markets are shut on weekends/holidays).
package fxconv

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/series"
)

const dateLayout = "2006-01-02"

type point struct {
	date time.Time
	rate float64
}

// Table holds dated conversion rates per source currency.
type Table struct {
	byCurrency map[string][]point // currency -> points sorted ascending by date
}

// LoadFX reads an FX CSV file into a Table.
func LoadFX(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err == io.EOF {
		return &Table{byCurrency: map[string][]point{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fxconv: %s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	dateI, ok1 := col["date"]
	curI, ok2 := col["currency"]
	rateI, ok3 := col["rate"]
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("fxconv: %s: CSV needs 'date', 'currency' and 'rate' columns", path)
	}

	t := &Table{byCurrency: map[string][]point{}}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fxconv: %s: %w", path, err)
		}
		if dateI >= len(rec) || curI >= len(rec) || rateI >= len(rec) {
			continue
		}
		d, err := time.Parse(dateLayout, strings.TrimSpace(rec[dateI]))
		if err != nil {
			continue
		}
		rate, err := strconv.ParseFloat(strings.TrimSpace(rec[rateI]), 64)
		if err != nil || rate <= 0 {
			continue
		}
		cur := strings.TrimSpace(rec[curI])
		t.byCurrency[cur] = append(t.byCurrency[cur], point{date: d, rate: rate})
	}
	for k := range t.byCurrency {
		sort.Slice(t.byCurrency[k], func(i, j int) bool { return t.byCurrency[k][i].date.Before(t.byCurrency[k][j].date) })
	}
	return t, nil
}

// rateOn returns the rate for a currency on date d, falling back to the most
// recent earlier date if d itself is absent.
func (t *Table) rateOn(currency string, d time.Time) (float64, bool) {
	pts := t.byCurrency[currency]
	if len(pts) == 0 {
		return 0, false
	}
	// Largest index with date <= d.
	i := sort.Search(len(pts), func(i int) bool { return pts[i].date.After(d) })
	if i == 0 {
		return 0, false
	}
	return pts[i-1].rate, true
}

// Convert returns s expressed in base currency. A series already in base is
// returned unchanged. Every point needs a rate (with fallback); a gap is an
// error so silent mis-conversion can't happen.
func Convert(s series.Series, base string, t *Table) (series.Series, error) {
	if s.Currency == base {
		return s, nil
	}
	if t == nil {
		return series.Series{}, fmt.Errorf("fxconv: %q is in %s but no FX table was provided", s.Label, s.Currency)
	}
	out := series.Series{Label: s.Label, Currency: base, Points: make([]series.Point, len(s.Points))}
	for i, p := range s.Points {
		rate, ok := t.rateOn(s.Currency, p.Date)
		if !ok {
			return series.Series{}, fmt.Errorf("fxconv: no %s->%s rate on or before %s (for %q)", s.Currency, base, p.Date.Format(dateLayout), s.Label)
		}
		out.Points[i] = series.Point{Date: p.Date, Close: p.Close * rate}
	}
	return out, nil
}

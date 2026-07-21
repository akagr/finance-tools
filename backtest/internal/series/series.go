// Package series loads a dated close-price series for one asset from CSV.
//
// CSV columns (header required; order-independent; extra columns ignored):
//
//	date,symbol,close
//	2024-06-14,^NSEI,23465.60
//
// A file may hold many symbols; Load returns one Series per distinct symbol.
// Points are de-duplicated by date (last wins) and sorted ascending.
package series

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Point is a single dated close price.
type Point struct {
	Date  time.Time
	Close float64
}

// Series is one asset's price history.
type Series struct {
	Label  string
	Points []Point // sorted ascending by date, de-duplicated
}

const dateLayout = "2006-01-02"

// Load reads a CSV file into one Series per distinct symbol.
func Load(path string) ([]Series, error) {
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
		return nil, fmt.Errorf("series: %s: empty file", path)
	}
	if err != nil {
		return nil, fmt.Errorf("series: %s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	dateI, ok1 := col["date"]
	symI, ok2 := col["symbol"]
	closeI, ok3 := col["close"]
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("series: %s: CSV needs 'date', 'symbol' and 'close' columns", path)
	}

	get := func(rec []string, i int) string {
		if i >= 0 && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	order := []string{}
	byLabel := map[string]*Series{}
	seen := map[string]map[string]struct{}{}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("series: %s: %w", path, err)
		}
		label := get(rec, symI)
		dateStr := get(rec, dateI)
		if label == "" || dateStr == "" {
			continue
		}
		d, err := time.Parse(dateLayout, dateStr)
		if err != nil {
			continue
		}
		close, err := strconv.ParseFloat(get(rec, closeI), 64)
		if err != nil || close <= 0 {
			continue
		}
		s, ok := byLabel[label]
		if !ok {
			s = &Series{Label: label}
			byLabel[label] = s
			seen[label] = map[string]struct{}{}
			order = append(order, label)
		}
		if _, dup := seen[label][dateStr]; dup {
			for i := range s.Points {
				if s.Points[i].Date.Equal(d) {
					s.Points[i].Close = close
					break
				}
			}
			continue
		}
		seen[label][dateStr] = struct{}{}
		s.Points = append(s.Points, Point{Date: d, Close: close})
	}

	out := make([]Series, 0, len(order))
	for _, label := range order {
		s := byLabel[label]
		sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Date.Before(s.Points[j].Date) })
		out = append(out, *s)
	}
	return out, nil
}

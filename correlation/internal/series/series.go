// Package series loads dated close-price series for one or more assets from CSV.
//
// CSV columns (header required; order-independent; extra columns ignored):
//
//	date,symbol,close,currency
//	2024-06-14,VWRA,118.42,USD
//	2024-06-14,^NSEI,23465.60,INR
//
// One file may hold many symbols. `currency` is optional and defaults to the
// value passed to Load. Points are de-duplicated by date (last wins) and sorted
// ascending. Asset order follows first appearance in the file(s).
package series

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Series is one asset's price history in a single currency.
type Series struct {
	Label    string
	Currency string
	Points   []Point // sorted ascending by date, de-duplicated
}

const dateLayout = "2006-01-02"

// Load reads a CSV file, or every *.csv in a directory, into one Series per
// distinct symbol. defaultCurrency fills rows that omit a currency.
func Load(path, defaultCurrency string) ([]Series, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		if files, err = filepath.Glob(filepath.Join(path, "*.csv")); err != nil {
			return nil, err
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}

	order := []string{}                      // labels in first-seen order
	byLabel := map[string]*Series{}          // label -> series
	seen := map[string]map[string]struct{}{} // label -> set of date strings (dedup, last wins)

	for _, f := range files {
		if err := loadFile(f, defaultCurrency, &order, byLabel, seen); err != nil {
			return nil, err
		}
	}

	out := make([]Series, 0, len(order))
	for _, label := range order {
		s := byLabel[label]
		sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Date.Before(s.Points[j].Date) })
		out = append(out, *s)
	}
	return out, nil
}

func loadFile(path, defaultCurrency string, order *[]string, byLabel map[string]*Series, seen map[string]map[string]struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("series: %s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	dateI, ok1 := col["date"]
	symI, ok2 := col["symbol"]
	closeI, ok3 := col["close"]
	if !ok1 || !ok2 || !ok3 {
		return fmt.Errorf("series: %s: CSV needs 'date', 'symbol' and 'close' columns", path)
	}
	curI, hasCur := col["currency"]

	get := func(rec []string, i int) string {
		if i >= 0 && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("series: %s: %w", path, err)
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
		cur := defaultCurrency
		if hasCur {
			if c := get(rec, curI); c != "" {
				cur = c
			}
		}

		s, ok := byLabel[label]
		if !ok {
			s = &Series{Label: label, Currency: cur}
			byLabel[label] = s
			seen[label] = map[string]struct{}{}
			*order = append(*order, label)
		}
		if _, dup := seen[label][dateStr]; dup {
			// Last value for a date wins: overwrite the existing point.
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
	return nil
}

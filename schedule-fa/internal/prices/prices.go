// Package prices provides a daily per-unit close price for each instrument,
// used by the exact peak engine (mode B). Prices are loaded from CSV and looked
// up with preceding-trading-day fallback (markets are closed on weekends and
// holidays), mirroring the TTBR lookup in package fx.
//
// CSV columns (header required; order-independent; extra columns ignored):
//
//	date,symbol,isin,close,currency
//	2024-06-14,AAPL,US0378331005,212.50,USD
//
// `isin` and `currency` are optional (currency defaults to USD). A row is keyed
// by both its ISIN and its symbol so either identifies it at lookup time.
package prices

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

type point struct {
	date  time.Time
	price model.Money
}

// CSVStore is an in-memory daily price store keyed by ISIN and symbol.
type CSVStore struct {
	series map[string][]point // key ("isin:"/"sym:") -> points sorted ascending by date
}

// NewCSVStore returns an empty store.
func NewCSVStore() *CSVStore { return &CSVStore{series: map[string][]point{}} }

// Load reads a CSV file or every *.csv in a directory.
func Load(path string) (*CSVStore, error) {
	s := NewCSVStore()
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		if files, err = filepath.Glob(filepath.Join(path, "*.csv")); err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}
	for _, f := range files {
		if err := s.loadFile(f); err != nil {
			return nil, err
		}
	}
	for k := range s.series {
		sort.Slice(s.series[k], func(i, j int) bool { return s.series[k][i].date.Before(s.series[k][j].date) })
	}
	return s, nil
}

func (s *CSVStore) loadFile(path string) error {
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
		return fmt.Errorf("prices: %s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	dateI, ok1 := col["date"]
	closeI, ok2 := col["close"]
	if !ok1 || !ok2 {
		return fmt.Errorf("prices: %s: CSV needs 'date' and 'close' columns", path)
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
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
			return fmt.Errorf("prices: %s: %w", path, err)
		}
		if dateI >= len(rec) || closeI >= len(rec) {
			continue
		}
		d, err := parseDate(rec[dateI])
		if err != nil {
			continue
		}
		price, ok := new(big.Rat).SetString(strings.TrimSpace(rec[closeI]))
		if !ok || price.Sign() < 0 {
			continue
		}
		cur := model.Currency(firstNonEmpty(get(rec, "currency"), "USD"))
		pt := point{date: d, price: model.NewMoney(cur, price)}
		if isin := get(rec, "isin"); isin != "" {
			s.series["isin:"+strings.ToUpper(isin)] = append(s.series["isin:"+strings.ToUpper(isin)], pt)
		}
		if sym := get(rec, "symbol"); sym != "" {
			s.series["sym:"+strings.ToUpper(sym)] = append(s.series["sym:"+strings.ToUpper(sym)], pt)
		}
	}
	return nil
}

// PriceOn returns the close for an instrument on a date, falling back to the
// nearest preceding day present in the data. Errors if the instrument is unknown
// or no price exists on or before the date.
func (s *CSVStore) PriceOn(inst model.Instrument, date time.Time) (model.Money, error) {
	pts := s.lookupSeries(inst)
	if pts == nil {
		return model.Money{}, fmt.Errorf("prices: no series for %s/%s", inst.Symbol, inst.ISIN)
	}
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	i := sort.Search(len(pts), func(i int) bool { return pts[i].date.After(day) })
	if i == 0 {
		return model.Money{}, fmt.Errorf("prices: no %s price on or before %s", inst.Symbol, day.Format("2006-01-02"))
	}
	return pts[i-1].price, nil
}

func (s *CSVStore) lookupSeries(inst model.Instrument) []point {
	if inst.ISIN != "" {
		if p := s.series["isin:"+strings.ToUpper(inst.ISIN)]; p != nil {
			return p
		}
	}
	if inst.Symbol != "" {
		if p := s.series["sym:"+strings.ToUpper(inst.Symbol)]; p != nil {
			return p
		}
	}
	return nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "; "); i >= 0 {
		s = s[:i]
	}
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("prices: bad date %q", s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

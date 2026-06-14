// Package fx converts foreign-currency amounts to INR using the SBI TT Buying
// Rate (TTBR), and records an audit trail for every conversion.
//
// Rule: use the TTBR published for the valuation date; if none was published
// (weekend/holiday/SBI gap), fall back to the nearest PRECEDING published day.
// The date actually used is captured in Conversion.RateDate so the report can
// show its work.
//
// The native rate source is the community "SBI FX RateKeeper" dataset
// (github.com/sahilgupta/sbi-fx-ratekeeper): one CSV per currency with columns
//
//	DATE,PDF FILE,TT BUY,TT SELL,BILL BUY,BILL SELL,...
//	2024-12-31 09:00,<url>,85.55,86.40,...
//
// We read the DATE and TT BUY columns by name; the currency comes from the
// filename (SBI_REFERENCE_RATES_USD.csv -> USD). Rows with a non-positive TT BUY
// (SBI publishes 0.00 on some non-working days) are skipped.
package fx

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

// Rate is a single day's TTBR for one currency, expressed as INR per 1 unit.
type Rate struct {
	Currency   model.Currency
	Date       time.Time // the date this rate was published for (date-only, UTC)
	INRPerUnit *big.Rat
}

// Conversion is the audit record behind one INR figure in the report.
type Conversion struct {
	Source   model.Money // the original foreign-currency amount
	Rate     Rate        // the rate applied
	RateDate time.Time   // date actually used (== Rate.Date; differs from the valuation date when a fallback was used)
	Result   model.Money // the resulting INR amount
}

// Store provides TTBR lookups with preceding-working-day fallback.
type Store interface {
	// RateOn returns the TTBR for cur to apply to a value dated `date`.
	RateOn(cur model.Currency, date time.Time) (Rate, error)
}

// Convert applies the TTBR for `date` to `amount` and returns both the INR
// result and the audit record.
func Convert(s Store, amount model.Money, date time.Time) (Conversion, error) {
	if amount.Currency == model.INR {
		return Conversion{Source: amount, Result: amount, RateDate: dayOf(date)}, nil
	}
	r, err := s.RateOn(amount.Currency, date)
	if err != nil {
		return Conversion{}, err
	}
	src := amount.Amount
	if src == nil {
		src = new(big.Rat)
	}
	inr := new(big.Rat).Mul(src, r.INRPerUnit)
	return Conversion{
		Source:   amount,
		Rate:     r,
		RateDate: r.Date,
		Result:   model.NewMoney(model.INR, inr),
	}, nil
}

// CSVStore is an in-memory TTBR store backed by RateKeeper-format CSV data.
type CSVStore struct {
	byDate map[model.Currency]map[string]Rate // currency -> "2006-01-02" -> rate (last row of the day wins)
	sorted map[model.Currency][]Rate          // currency -> rates sorted ascending by Date
}

// NewCSVStore returns an empty store; load data with LoadRateKeeperFile/Dir.
func NewCSVStore() *CSVStore {
	return &CSVStore{
		byDate: map[model.Currency]map[string]Rate{},
		sorted: map[model.Currency][]Rate{},
	}
}

// LoadRateKeeperDir loads every *.csv in dir whose filename encodes a 3-letter
// currency (e.g. SBI_REFERENCE_RATES_USD.csv). Files without a recognizable
// currency suffix are skipped.
func LoadRateKeeperDir(dir string) (*CSVStore, error) {
	s := NewCSVStore()
	matches, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		return nil, err
	}
	for _, p := range matches {
		cur := currencyFromFilename(p)
		if cur == "" {
			continue
		}
		if err := s.LoadRateKeeperFile(cur, p); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// LoadRateKeeperFile loads one RateKeeper-format CSV for the given currency.
func (c *CSVStore) LoadRateKeeperFile(cur model.Currency, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.loadRateKeeper(cur, f)
}

func (c *CSVStore) loadRateKeeper(cur model.Currency, r io.Reader) error {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return fmt.Errorf("fx: %s: read header: %w", cur, err)
	}
	dateCol, ttBuyCol := -1, -1
	for i, h := range header {
		switch strings.ToUpper(strings.TrimSpace(h)) {
		case "DATE":
			dateCol = i
		case "TT BUY":
			ttBuyCol = i
		}
	}
	if dateCol < 0 || ttBuyCol < 0 {
		return fmt.Errorf("fx: %s: CSV missing DATE or 'TT BUY' column", cur)
	}

	if c.byDate[cur] == nil {
		c.byDate[cur] = map[string]Rate{}
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("fx: %s: read row: %w", cur, err)
		}
		if dateCol >= len(rec) || ttBuyCol >= len(rec) {
			continue
		}
		rate, ok := new(big.Rat).SetString(strings.TrimSpace(rec[ttBuyCol]))
		if !ok || rate.Sign() <= 0 {
			continue // 0.00 / blank => not published that day
		}
		d, err := parseRateKeeperDate(rec[dateCol])
		if err != nil {
			continue
		}
		// Rows are chronological, so the last row for a date overwrites earlier
		// intraday revisions — i.e. we keep the latest published rate of the day.
		c.byDate[cur][d.Format("2006-01-02")] = Rate{Currency: cur, Date: d, INRPerUnit: rate}
	}
	c.rebuild(cur)
	return nil
}

func (c *CSVStore) rebuild(cur model.Currency) {
	rs := make([]Rate, 0, len(c.byDate[cur]))
	for _, r := range c.byDate[cur] {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Date.Before(rs[j].Date) })
	c.sorted[cur] = rs
}

// RateOn implements Store: the TTBR for cur on `date`, or the nearest preceding
// published day. Errors if no rate exists on or before `date`.
func (c *CSVStore) RateOn(cur model.Currency, date time.Time) (Rate, error) {
	rs := c.sorted[cur]
	if len(rs) == 0 {
		return Rate{}, fmt.Errorf("fx: no rates loaded for %s", cur)
	}
	day := dayOf(date)
	// rightmost index with rs[i].Date <= day
	i := sort.Search(len(rs), func(i int) bool { return rs[i].Date.After(day) })
	if i == 0 {
		return Rate{}, fmt.Errorf("fx: no %s TTBR on or before %s (earliest available is %s)",
			cur, day.Format("2006-01-02"), rs[0].Date.Format("2006-01-02"))
	}
	return rs[i-1], nil
}

// --- helpers ---

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func parseRateKeeperDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return dayOf(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("fx: unrecognized date %q", s)
}

// currencyFromFilename extracts a trailing 3-letter currency code, e.g.
// "SBI_REFERENCE_RATES_USD.csv" -> "USD". Returns "" if none is found.
func currencyFromFilename(path string) model.Currency {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	if len(parts) == 0 {
		return ""
	}
	last := strings.ToUpper(parts[len(parts)-1])
	if len(last) == 3 && isAlpha(last) {
		return model.Currency(last)
	}
	return ""
}

func isAlpha(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

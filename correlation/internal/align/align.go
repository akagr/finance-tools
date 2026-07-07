// Package align resamples per-asset price series to a common frequency and
// intersects them onto the set of periods for which every asset has data.
//
// Assets trade on different exchange calendars (LSE vs NSE, differing holidays,
// different time zones), so a raw daily join would drop or misalign many days.
// Resampling to weekly/monthly and intersecting yields comparable, contemporaneous
// observations. Within a period the last available close is used.
package align

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/series"
)

// Frequency is the resampling granularity.
type Frequency string

const (
	Daily   Frequency = "daily"
	Weekly  Frequency = "weekly"
	Monthly Frequency = "monthly"
)

// PeriodsPerYear is used to annualise volatility for a frequency.
func (f Frequency) PeriodsPerYear() float64 {
	switch f {
	case Daily:
		return 252
	case Weekly:
		return 52
	case Monthly:
		return 12
	default:
		return 252
	}
}

// ParseFrequency validates and normalises a frequency string.
func ParseFrequency(s string) (Frequency, error) {
	switch Frequency(s) {
	case Daily, Weekly, Monthly:
		return Frequency(s), nil
	default:
		return "", fmt.Errorf("align: unknown frequency %q (want daily|weekly|monthly)", s)
	}
}

// Aligned is a rectangular matrix of closes: Closes[i] is asset i's series over
// the shared Dates, so every row has len(Dates) entries.
type Aligned struct {
	Labels     []string
	Currencies []string
	Dates      []time.Time // representative date per shared period, ascending
	Closes     [][]float64 // Closes[asset][period]
}

// periodKey buckets a date into a period identifier for the given frequency.
func periodKey(d time.Time, f Frequency) string {
	switch f {
	case Weekly:
		y, w := d.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case Monthly:
		return d.Format("2006-01")
	default: // Daily
		return d.Format("2006-01-02")
	}
}

// bucket is the last observation seen within a period.
type bucket struct {
	last  time.Time
	close float64
}

// Build resamples each series to the frequency and intersects them onto the
// periods common to all assets.
func Build(all []series.Series, f Frequency) (Aligned, error) {
	if len(all) < 2 {
		return Aligned{}, errors.New("align: need at least two assets")
	}

	// Resample every asset: period key -> last close in that period.
	resampled := make([]map[string]bucket, len(all))
	for i, s := range all {
		m := map[string]bucket{}
		for _, p := range s.Points {
			k := periodKey(p.Date, f)
			if b, ok := m[k]; !ok || p.Date.After(b.last) {
				m[k] = bucket{last: p.Date, close: p.Close}
			}
		}
		resampled[i] = m
	}

	// Intersect period keys across all assets.
	common := map[string]struct{}{}
	for k := range resampled[0] {
		common[k] = struct{}{}
	}
	for i := 1; i < len(resampled); i++ {
		for k := range common {
			if _, ok := resampled[i][k]; !ok {
				delete(common, k)
			}
		}
	}
	if len(common) == 0 {
		return Aligned{}, errors.New("align: assets share no common periods (check dates/frequency)")
	}

	// Representative date per period = the latest last-close date across assets,
	// so ordering reflects actual observation timing.
	type pk struct {
		key  string
		date time.Time
	}
	keys := make([]pk, 0, len(common))
	for k := range common {
		var latest time.Time
		for i := range resampled {
			if d := resampled[i][k].last; d.After(latest) {
				latest = d
			}
		}
		keys = append(keys, pk{key: k, date: latest})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].date.Before(keys[j].date) })

	out := Aligned{
		Labels:     make([]string, len(all)),
		Currencies: make([]string, len(all)),
		Dates:      make([]time.Time, len(keys)),
		Closes:     make([][]float64, len(all)),
	}
	for i, s := range all {
		out.Labels[i] = s.Label
		out.Currencies[i] = s.Currency
		out.Closes[i] = make([]float64, len(keys))
	}
	for j, k := range keys {
		out.Dates[j] = k.date
		for i := range resampled {
			out.Closes[i][j] = resampled[i][k.key].close
		}
	}
	return out, nil
}

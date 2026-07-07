// Package returns turns an aligned matrix of closes into period returns, the
// series that correlation is actually computed on.
package returns

import (
	"fmt"
	"math"
	"time"

	"github.com/akagr/finance-tools/correlation/internal/align"
)

// Kind selects the return definition.
type Kind string

const (
	Log    Kind = "log"    // ln(P_t / P_{t-1}); additive, the statistical default
	Simple Kind = "simple" // P_t / P_{t-1} - 1
)

// ParseKind validates and normalises a return-kind string.
func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case Log, Simple:
		return Kind(s), nil
	default:
		return "", fmt.Errorf("returns: unknown kind %q (want log|simple)", s)
	}
}

// Returns holds per-asset return observations over EndDates. Each row has
// len(EndDates) entries, one fewer than the number of aligned closes.
type Returns struct {
	Labels   []string
	EndDates []time.Time // period-end date of each return
	Series   [][]float64 // Series[asset][period]
}

// Compute derives period returns from aligned closes. It needs at least two
// aligned periods (to produce one return); correlation downstream needs two
// returns, i.e. three aligned periods.
func Compute(a align.Aligned, kind Kind) (Returns, error) {
	nPeriods := len(a.Dates)
	if nPeriods < 2 {
		return Returns{}, fmt.Errorf("returns: need >=2 aligned periods, have %d", nPeriods)
	}
	out := Returns{
		Labels:   a.Labels,
		EndDates: a.Dates[1:],
		Series:   make([][]float64, len(a.Labels)),
	}
	for i := range a.Closes {
		row := make([]float64, nPeriods-1)
		for k := 1; k < nPeriods; k++ {
			prev := a.Closes[i][k-1]
			cur := a.Closes[i][k]
			if prev <= 0 {
				return Returns{}, fmt.Errorf("returns: non-positive price for %q on %s", a.Labels[i], a.Dates[k-1].Format("2006-01-02"))
			}
			switch kind {
			case Log:
				row[k-1] = math.Log(cur / prev)
			default:
				row[k-1] = cur/prev - 1
			}
		}
		out.Series[i] = row
	}
	return out, nil
}

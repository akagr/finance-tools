// Package stats computes Pearson correlation, covariance, and per-asset
// dispersion over aligned return series. This is the numeric heart of the tool.
//
// Everything here is float64, not math/big.Rat: this is statistics, not money
// accounting, and the standard deviation requires a square root (irrational),
// so exact rationals buy nothing. Prices/returns feeding in are already ratios.
package stats

import (
	"errors"
	"fmt"
	"math"
)

// Result holds a correlation analysis over aligned per-asset return series.
type Result struct {
	Labels      []string    // asset labels, length N
	N           int         // number of paired return observations
	Correlation [][]float64 // NxN, symmetric, diagonal 1 (NaN if an asset is constant)
	Covariance  [][]float64 // NxN sample covariance (denominator n-1)
	Mean        []float64   // per-asset mean return
	Stdev       []float64   // per-asset sample standard deviation (n-1)
	Pairs       []Pair      // one entry per unique i<j pair, sorted by (i, j)
}

// Pair is the correlation between two distinct assets with a 95% confidence
// interval derived from the Fisher z-transform (NaN bounds when N<=3 or |r|==1).
type Pair struct {
	I, J   int
	A, B   string
	R      float64
	CI95Lo float64
	CI95Hi float64
}

// Compute returns correlation/covariance and per-asset dispersion for the given
// return series. series[i] holds asset i's return observations; every series
// must be the same length and hold at least two observations.
func Compute(labels []string, series [][]float64) (Result, error) {
	n := len(series)
	if n < 2 {
		return Result{}, errors.New("stats: need at least two assets")
	}
	if len(labels) != n {
		return Result{}, errors.New("stats: labels and series length mismatch")
	}
	obs := len(series[0])
	if obs < 2 {
		return Result{}, errors.New("stats: need at least two return observations")
	}
	for i, s := range series {
		if len(s) != obs {
			return Result{}, errors.New("stats: return series have unequal length")
		}
		_ = i
	}

	mean := make([]float64, n)
	for i := range series {
		var sum float64
		for _, v := range series[i] {
			sum += v
		}
		mean[i] = sum / float64(obs)
	}

	// Sample covariance matrix (denominator obs-1).
	cov := make([][]float64, n)
	for i := range cov {
		cov[i] = make([]float64, n)
	}
	den := float64(obs - 1)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var acc float64
			for k := 0; k < obs; k++ {
				acc += (series[i][k] - mean[i]) * (series[j][k] - mean[j])
			}
			c := acc / den
			cov[i][j] = c
			cov[j][i] = c
		}
	}

	stdev := make([]float64, n)
	for i := range stdev {
		stdev[i] = math.Sqrt(cov[i][i])
	}

	corr := make([][]float64, n)
	for i := range corr {
		corr[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var r float64
			switch {
			case i == j:
				r = 1
			case stdev[i] == 0 || stdev[j] == 0:
				r = math.NaN()
			default:
				r = cov[i][j] / (stdev[i] * stdev[j])
				r = clampCorr(r)
			}
			corr[i][j] = r
			corr[j][i] = r
		}
	}

	var pairs []Pair
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			lo, hi := fisherCI95(corr[i][j], obs)
			pairs = append(pairs, Pair{
				I: i, J: j,
				A: labels[i], B: labels[j],
				R:      corr[i][j],
				CI95Lo: lo,
				CI95Hi: hi,
			})
		}
	}

	return Result{
		Labels:      labels,
		N:           obs,
		Correlation: corr,
		Covariance:  cov,
		Mean:        mean,
		Stdev:       stdev,
		Pairs:       pairs,
	}, nil
}

// RollingPair holds the time-varying correlation between two assets over a
// sliding window. Values[k] is the Pearson r computed on the window of returns
// ending at return index EndIdx[k] (inclusive), i.e. the last Window returns up
// to and including that observation. A NaN entry means the window was
// degenerate (an asset was constant across it).
type RollingPair struct {
	I, J   int
	A, B   string
	Window int
	EndIdx []int     // return-observation index at the right edge of each window
	Values []float64 // rolling Pearson r, one per window position
}

// Rolling computes a sliding-window Pearson correlation for every unique i<j
// pair. window is the number of return observations per window; it must be at
// least 2 and no larger than the number of observations. The first window ends
// at index window-1, then it slides forward one observation at a time, so the
// output has obs-window+1 positions per pair.
func Rolling(labels []string, series [][]float64, window int) ([]RollingPair, error) {
	n := len(series)
	if n < 2 {
		return nil, errors.New("stats: need at least two assets")
	}
	if len(labels) != n {
		return nil, errors.New("stats: labels and series length mismatch")
	}
	obs := len(series[0])
	for _, s := range series {
		if len(s) != obs {
			return nil, errors.New("stats: return series have unequal length")
		}
	}
	if window < 2 {
		return nil, errors.New("stats: rolling window must be >= 2")
	}
	if window > obs {
		return nil, fmt.Errorf("stats: rolling window %d exceeds %d observations", window, obs)
	}

	positions := obs - window + 1
	endIdx := make([]int, positions)
	for p := 0; p < positions; p++ {
		endIdx[p] = p + window - 1
	}

	var out []RollingPair
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			vals := make([]float64, positions)
			for p := 0; p < positions; p++ {
				start := p
				end := p + window // exclusive
				vals[p] = windowCorr(series[i][start:end], series[j][start:end])
			}
			out = append(out, RollingPair{
				I: i, J: j,
				A: labels[i], B: labels[j],
				Window: window,
				EndIdx: endIdx,
				Values: vals,
			})
		}
	}
	return out, nil
}

// windowCorr computes Pearson r over two equal-length slices, returning NaN if
// either slice is constant.
func windowCorr(a, b []float64) float64 {
	m := len(a)
	var ma, mb float64
	for k := 0; k < m; k++ {
		ma += a[k]
		mb += b[k]
	}
	ma /= float64(m)
	mb /= float64(m)
	var sab, saa, sbb float64
	for k := 0; k < m; k++ {
		da := a[k] - ma
		db := b[k] - mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	if saa == 0 || sbb == 0 {
		return math.NaN()
	}
	return clampCorr(sab / math.Sqrt(saa*sbb))
}

// clampCorr guards against tiny floating-point excursions past ±1.
func clampCorr(r float64) float64 {
	if r > 1 {
		return 1
	}
	if r < -1 {
		return -1
	}
	return r
}

// fisherCI95 returns the 95% confidence interval for a Pearson r using the
// Fisher z-transform. Bounds are NaN when the interval is undefined (n<=3 or a
// perfect ±1 correlation, where atanh diverges).
func fisherCI95(r float64, n int) (lo, hi float64) {
	if n <= 3 || math.IsNaN(r) || r <= -1 || r >= 1 {
		return math.NaN(), math.NaN()
	}
	z := math.Atanh(r)
	se := 1 / math.Sqrt(float64(n-3))
	const z95 = 1.959963984540054 // standard-normal 97.5th percentile
	return math.Tanh(z - z95*se), math.Tanh(z + z95*se)
}

// Package report renders a correlation analysis as Markdown, CSV, or JSON.
//
// Numbers are rounded to six decimals in every format so output is stable and
// diff-friendly; undefined values (e.g. correlation with a constant series, or
// an undefined confidence bound) render as "n/a" in text and null in JSON.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// Report is the render model produced by the pipeline.
type Report struct {
	Meta        Meta
	Labels      []string
	Correlation [][]float64
	Covariance  [][]float64
	Mean        []float64 // per-asset mean period return
	Stdev       []float64 // per-asset per-period stdev
	AnnVol      []float64 // per-asset annualised volatility
	Pairs       []Pair
	N           int // number of return observations
}

// Meta captures how the analysis was produced.
type Meta struct {
	Frequency    string
	ReturnKind   string
	BaseCurrency string // empty in native mode (no conversion)
	Start        time.Time
	End          time.Time
	Assets       []Asset
	Notes        []string
}

// Asset describes one input series.
type Asset struct {
	Label     string
	Currency  string
	Converted bool
}

// Pair is a pairwise correlation with a 95% confidence interval.
type Pair struct {
	A, B   string
	R      float64
	CI95Lo float64
	CI95Hi float64
}

const disclaimer = "NOTE: not investment advice. Correlations are backward-looking, sample-dependent, and unstable in crises (they often rise toward 1 exactly when diversification is needed most)."

// Render writes r to w in the given format: "md", "csv", or "json".
func Render(w io.Writer, r Report, format string) error {
	switch strings.ToLower(format) {
	case "md", "markdown":
		return renderMarkdown(w, r)
	case "csv":
		return renderCSV(w, r)
	case "json":
		return renderJSON(w, r)
	default:
		return fmt.Errorf("report: unknown format %q (want md|csv|json)", format)
	}
}

func round6(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	return math.Round(v*1e6) / 1e6
}

// f4 formats for text tables; "n/a" for undefined.
func f4(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/a"
	}
	return strconv.FormatFloat(v, 'f', 4, 64)
}

func pct(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/a"
	}
	return strconv.FormatFloat(v*100, 'f', 2, 64) + "%"
}

func renderMarkdown(w io.Writer, r Report) error {
	var b strings.Builder
	b.WriteString("# Asset correlation report\n\n")

	base := r.Meta.BaseCurrency
	if base == "" {
		base = "native (per-asset local currency, no FX conversion)"
	}
	fmt.Fprintf(&b, "- Period: %s → %s (%d %s returns)\n",
		r.Meta.Start.Format("2006-01-02"), r.Meta.End.Format("2006-01-02"), r.N, r.Meta.Frequency)
	fmt.Fprintf(&b, "- Returns: %s\n", r.Meta.ReturnKind)
	fmt.Fprintf(&b, "- Currency basis: %s\n\n", base)

	// Correlation matrix.
	b.WriteString("## Correlation matrix\n\n")
	b.WriteString("|            |")
	for _, l := range r.Labels {
		fmt.Fprintf(&b, " %s |", l)
	}
	b.WriteString("\n|------------|")
	for range r.Labels {
		b.WriteString("------|")
	}
	b.WriteByte('\n')
	for i, l := range r.Labels {
		fmt.Fprintf(&b, "| **%s** |", l)
		for j := range r.Labels {
			fmt.Fprintf(&b, " %s |", f4(r.Correlation[i][j]))
		}
		b.WriteByte('\n')
		_ = i
	}
	b.WriteByte('\n')

	// Per-asset dispersion.
	b.WriteString("## Per-asset stats\n\n")
	b.WriteString("| Asset | Currency | Mean return | Volatility (per period) | Volatility (annualised) |\n")
	b.WriteString("|-------|----------|-------------|-------------------------|-------------------------|\n")
	for i, a := range r.Meta.Assets {
		cur := a.Currency
		if a.Converted {
			cur += " (converted)"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", a.Label, cur, pct(r.Mean[i]), pct(r.Stdev[i]), pct(r.AnnVol[i]))
	}
	b.WriteByte('\n')

	// Pairwise detail.
	if len(r.Pairs) > 0 {
		b.WriteString("## Pairwise correlations (95% CI)\n\n")
		b.WriteString("| Pair | r | 95% CI |\n")
		b.WriteString("|------|---|--------|\n")
		for _, p := range r.Pairs {
			ci := "n/a"
			if !math.IsNaN(p.CI95Lo) && !math.IsNaN(p.CI95Hi) {
				ci = fmt.Sprintf("[%s, %s]", f4(p.CI95Lo), f4(p.CI95Hi))
			}
			fmt.Fprintf(&b, "| %s – %s | %s | %s |\n", p.A, p.B, f4(p.R), ci)
		}
		b.WriteByte('\n')
	}

	if len(r.Meta.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range r.Meta.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteByte('\n')
	}

	fmt.Fprintf(&b, "> %s\n", disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

func renderCSV(w io.Writer, r Report) error {
	cw := csv.NewWriter(w)
	header := append([]string{""}, r.Labels...)
	if err := cw.Write(header); err != nil {
		return err
	}
	for i, l := range r.Labels {
		row := make([]string, 0, len(r.Labels)+1)
		row = append(row, l)
		for j := range r.Labels {
			v := r.Correlation[i][j]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				row = append(row, "")
			} else {
				row = append(row, strconv.FormatFloat(round6(v), 'f', -1, 64))
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// jsonFloat marshals NaN/Inf as null and rounds finite values to six decimals.
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(round6(v), 'f', -1, 64)), nil
}

func jfMatrix(m [][]float64) [][]jsonFloat {
	out := make([][]jsonFloat, len(m))
	for i := range m {
		out[i] = make([]jsonFloat, len(m[i]))
		for j := range m[i] {
			out[i][j] = jsonFloat(m[i][j])
		}
	}
	return out
}

func jfSlice(s []float64) []jsonFloat {
	out := make([]jsonFloat, len(s))
	for i := range s {
		out[i] = jsonFloat(s[i])
	}
	return out
}

func renderJSON(w io.Writer, r Report) error {
	type jsonAsset struct {
		Label     string `json:"label"`
		Currency  string `json:"currency"`
		Converted bool   `json:"converted"`
	}
	type jsonPair struct {
		A      string    `json:"a"`
		B      string    `json:"b"`
		R      jsonFloat `json:"r"`
		CI95Lo jsonFloat `json:"ci95_lo"`
		CI95Hi jsonFloat `json:"ci95_hi"`
	}
	type jsonReport struct {
		Frequency    string        `json:"frequency"`
		ReturnKind   string        `json:"return_kind"`
		BaseCurrency string        `json:"base_currency,omitempty"`
		Start        string        `json:"start"`
		End          string        `json:"end"`
		Observations int           `json:"observations"`
		Assets       []jsonAsset   `json:"assets"`
		Labels       []string      `json:"labels"`
		Correlation  [][]jsonFloat `json:"correlation"`
		Covariance   [][]jsonFloat `json:"covariance"`
		Mean         []jsonFloat   `json:"mean_return"`
		Stdev        []jsonFloat   `json:"stdev"`
		AnnVol       []jsonFloat   `json:"annualised_volatility"`
		Pairs        []jsonPair    `json:"pairs"`
		Notes        []string      `json:"notes,omitempty"`
	}

	jr := jsonReport{
		Frequency:    r.Meta.Frequency,
		ReturnKind:   r.Meta.ReturnKind,
		BaseCurrency: r.Meta.BaseCurrency,
		Start:        r.Meta.Start.Format("2006-01-02"),
		End:          r.Meta.End.Format("2006-01-02"),
		Observations: r.N,
		Labels:       r.Labels,
		Correlation:  jfMatrix(r.Correlation),
		Covariance:   jfMatrix(r.Covariance),
		Mean:         jfSlice(r.Mean),
		Stdev:        jfSlice(r.Stdev),
		AnnVol:       jfSlice(r.AnnVol),
		Notes:        r.Meta.Notes,
	}
	for _, a := range r.Meta.Assets {
		jr.Assets = append(jr.Assets, jsonAsset{Label: a.Label, Currency: a.Currency, Converted: a.Converted})
	}
	for _, p := range r.Pairs {
		jr.Pairs = append(jr.Pairs, jsonPair{A: p.A, B: p.B, R: jsonFloat(p.R), CI95Lo: jsonFloat(p.CI95Lo), CI95Hi: jsonFloat(p.CI95Hi)})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jr)
}

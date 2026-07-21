package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// SweepMeta describes a parameter-sweep run.
type SweepMeta struct {
	Symbol   string   `json:"symbol"`
	Strategy string   `json:"strategy"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Bars     int      `json:"bars"`
	Metric   string   `json:"metric"`
	Notes    []string `json:"notes,omitempty"`
}

// SweepPoint is one parameter combination and the metrics it produced. Invalid
// combinations (e.g. a crossover with fast >= slow) carry Valid=false and no
// numbers, so a 2-D grid stays rectangular.
type SweepPoint struct {
	Coords      []float64 `json:"coords"`
	Valid       bool      `json:"valid"`
	Return      float64   `json:"return,omitempty"`
	CAGR        float64   `json:"cagr,omitempty"`
	Sharpe      float64   `json:"sharpe,omitempty"`
	Sortino     float64   `json:"sortino,omitempty"`
	MaxDrawdown float64   `json:"max_drawdown,omitempty"`
	Calmar      float64   `json:"calmar,omitempty"`
	Trades      int       `json:"trades,omitempty"`
	Metric      float64   `json:"metric_value,omitempty"`
}

// Sweep is the full parameter-sweep result.
type Sweep struct {
	Meta          SweepMeta    `json:"meta"`
	AxisNames     []string     `json:"axis_names"`
	AxisValues    [][]float64  `json:"axis_values"`
	Points        []SweepPoint `json:"points"`
	Best          float64      `json:"best"`
	Worst         float64      `json:"worst"`
	LowerIsBetter bool         `json:"lower_is_better"`
}

// RenderSweep writes sw to w as "md", "csv" or "json".
func RenderSweep(w io.Writer, sw Sweep, format string) error {
	switch format {
	case "md", "markdown", "":
		return renderSweepMarkdown(w, sw)
	case "csv":
		return renderSweepCSV(w, sw)
	case "json":
		return renderSweepJSON(w, sw)
	default:
		return fmt.Errorf("report: unknown format %q (want md|csv|json)", format)
	}
}

func renderSweepMarkdown(w io.Writer, sw Sweep) error {
	var b strings.Builder
	m := sw.Meta
	fmt.Fprintf(&b, "# Parameter sweep — %s (%s)\n\n", m.Symbol, m.Strategy)
	fmt.Fprintf(&b, "- Period: %s → %s (%d bars)\n", m.Start, m.End, m.Bars)
	fmt.Fprintf(&b, "- Metric: **%s** (best %s)\n\n", m.Metric, metricFmt(sw.Meta.Metric, sw.Best))

	if len(sw.AxisNames) == 1 {
		renderSweep1D(&b, sw)
	} else {
		renderSweep2D(&b, sw)
	}

	b.WriteByte('\n')
	for _, n := range m.Notes {
		fmt.Fprintf(&b, "> %s\n", n)
	}
	if len(m.Notes) > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "_%s_\n", disclaimer)

	_, err := io.WriteString(w, b.String())
	return err
}

func renderSweep1D(b *strings.Builder, sw Sweep) {
	header := []string{sw.AxisNames[0], "Return", "CAGR", "Sharpe", "Sortino", "Max DD", "Calmar", "Trades", ""}
	aligns := []mdAlign{alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignLeft}
	rows := make([][]string, 0, len(sw.Points))
	for _, p := range sw.Points {
		mark := ""
		if p.Valid && p.Metric == sw.Best {
			mark = "◄ best"
		}
		if !p.Valid {
			rows = append(rows, []string{numAxis(p.Coords[0]), "—", "—", "—", "—", "—", "—", "—", ""})
			continue
		}
		rows = append(rows, []string{
			numAxis(p.Coords[0]), pct(p.Return), pct(p.CAGR), num(p.Sharpe), num(p.Sortino),
			pct(p.MaxDrawdown), num(p.Calmar), strconv.Itoa(p.Trades), mark,
		})
	}
	mdTable(b, header, rows, aligns)
}

func renderSweep2D(b *strings.Builder, sw Sweep) {
	rowsAx, colsAx := sw.AxisValues[0], sw.AxisValues[1]
	// index points by (row,col) coordinate for O(1) lookup.
	cell := map[[2]float64]SweepPoint{}
	for _, p := range sw.Points {
		cell[[2]float64{p.Coords[0], p.Coords[1]}] = p
	}

	fmt.Fprintf(b, "Rows = `%s`, columns = `%s`; each cell is **%s** (◄ marks the best).\n\n",
		sw.AxisNames[0], sw.AxisNames[1], sw.Meta.Metric)

	header := make([]string, 0, len(colsAx)+1)
	header = append(header, sw.AxisNames[0]+`\`+sw.AxisNames[1])
	for _, c := range colsAx {
		header = append(header, numAxis(c))
	}
	aligns := make([]mdAlign, len(header))
	aligns[0] = alignLeft
	for i := 1; i < len(aligns); i++ {
		aligns[i] = alignRight
	}

	rows := make([][]string, 0, len(rowsAx))
	for _, r := range rowsAx {
		row := make([]string, 0, len(colsAx)+1)
		row = append(row, numAxis(r))
		for _, c := range colsAx {
			p, ok := cell[[2]float64{r, c}]
			switch {
			case !ok || !p.Valid:
				row = append(row, "—")
			case p.Metric == sw.Best:
				row = append(row, metricFmt(sw.Meta.Metric, p.Metric)+"◄")
			default:
				row = append(row, metricFmt(sw.Meta.Metric, p.Metric))
			}
		}
		rows = append(rows, row)
	}
	mdTable(b, header, rows, aligns)
}

func renderSweepCSV(w io.Writer, sw Sweep) error {
	cw := csv.NewWriter(w)
	header := append(append([]string{}, sw.AxisNames...),
		"valid", "return", "cagr", "sharpe", "sortino", "max_drawdown", "calmar", "trades", "metric_value")
	rows := [][]string{header}
	for _, p := range sw.Points {
		row := make([]string, 0, len(header))
		for _, c := range p.Coords {
			row = append(row, numAxis(c))
		}
		row = append(row,
			strconv.FormatBool(p.Valid), ff(p.Return), ff(p.CAGR), ff(p.Sharpe),
			ff(p.Sortino), ff(p.MaxDrawdown), ff(p.Calmar), strconv.Itoa(p.Trades), ff(p.Metric))
		rows = append(rows, row)
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func renderSweepJSON(w io.Writer, sw Sweep) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sw)
}

// metricFmt formats a metric value the way its column reads elsewhere: the
// percentage metrics as %, the ratios as plain numbers.
func metricFmt(metric string, v float64) string {
	switch metric {
	case "return", "cagr", "drawdown":
		return pct(v)
	default:
		return num(v)
	}
}

// numAxis formats an axis tick: an integer when it is whole, else two decimals.
func numAxis(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

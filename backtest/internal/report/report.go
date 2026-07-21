// Package report renders a backtest result as Markdown, CSV or JSON. It is the
// only place figures are formatted for humans; everything upstream stays numeric.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/akagr/finance-tools/backtest/internal/metrics"
)

// Meta describes the run that produced the report.
type Meta struct {
	Symbol         string   `json:"symbol"`
	Start          string   `json:"start"`
	End            string   `json:"end"`
	Bars           int      `json:"bars"`
	InitialCapital float64  `json:"initial_capital"`
	BrokerageBps   float64  `json:"brokerage_bps"`
	STTBps         float64  `json:"stt_bps"`
	SlippageBps    float64  `json:"slippage_bps"`
	Notes          []string `json:"notes,omitempty"`
}

// Line is one strategy's statistics (the tested strategy and the benchmark).
type Line struct {
	Strategy    string  `json:"strategy"`
	TotalReturn float64 `json:"total_return"`
	CAGR        float64 `json:"cagr"`
	AnnVol      float64 `json:"ann_vol"`
	Sharpe      float64 `json:"sharpe"`
	Sortino     float64 `json:"sortino"`
	MaxDrawdown float64 `json:"max_drawdown"`
	Calmar      float64 `json:"calmar"`
	FinalValue  float64 `json:"final_value"`
	Trades      int     `json:"trades"`
	Turnover    float64 `json:"turnover"`
	TotalCost   float64 `json:"total_cost"`
	Exposure    float64 `json:"exposure"`
}

// LineFrom projects metrics.Stats onto a report Line.
func LineFrom(name string, s metrics.Stats) Line {
	return Line{
		Strategy:    name,
		TotalReturn: s.TotalReturn,
		CAGR:        s.CAGR,
		AnnVol:      s.AnnVol,
		Sharpe:      s.Sharpe,
		Sortino:     s.Sortino,
		MaxDrawdown: s.MaxDrawdown,
		Calmar:      s.Calmar,
		FinalValue:  s.FinalValue,
		Trades:      s.Trades,
		Turnover:    s.Turnover,
		TotalCost:   s.TotalCost,
		Exposure:    s.Exposure,
	}
}

// Report is the full render-ready result: the tested strategy first, then the
// buy-and-hold benchmark.
type Report struct {
	Meta  Meta   `json:"meta"`
	Lines []Line `json:"lines"`
}

// Render writes rep to w in the given format: "md", "csv" or "json".
func Render(w io.Writer, rep Report, format string) error {
	switch format {
	case "md", "markdown", "":
		return renderMarkdown(w, rep)
	case "csv":
		return renderCSV(w, rep)
	case "json":
		return renderJSON(w, rep)
	default:
		return fmt.Errorf("report: unknown format %q (want md|csv|json)", format)
	}
}

const disclaimer = "NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse."

func renderMarkdown(w io.Writer, rep Report) error {
	m := rep.Meta
	var b strings.Builder
	fmt.Fprintf(&b, "# Backtest — %s\n\n", m.Symbol)
	fmt.Fprintf(&b, "- Period: %s → %s (%d bars)\n", m.Start, m.End, m.Bars)
	fmt.Fprintf(&b, "- Initial capital: %s\n", money(m.InitialCapital))
	fmt.Fprintf(&b, "- Costs: brokerage %.0f bps, STT %.0f bps, slippage %.0f bps (per trade)\n\n",
		m.BrokerageBps, m.STTBps, m.SlippageBps)

	header := []string{"Strategy", "Total", "CAGR", "Ann. vol", "Sharpe", "Sortino", "Max DD", "Calmar", "Final", "Trades", "Exposure", "Costs"}
	aligns := []mdAlign{alignLeft, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight}
	rows := make([][]string, 0, len(rep.Lines))
	for _, l := range rep.Lines {
		rows = append(rows, []string{
			l.Strategy, pct(l.TotalReturn), pct(l.CAGR), pct(l.AnnVol), num(l.Sharpe), num(l.Sortino),
			pct(l.MaxDrawdown), num(l.Calmar), money(l.FinalValue), strconv.Itoa(l.Trades), pct(l.Exposure), money(l.TotalCost),
		})
	}
	mdTable(&b, header, rows, aligns)

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

// mdAlign selects column justification for a Markdown table.
type mdAlign int

const (
	alignLeft mdAlign = iota
	alignRight
)

// mdTable writes a GitHub-flavoured Markdown table whose cells are padded to the
// widest value in each column, so the raw source is aligned and readable. The
// separator row encodes alignment (`---` left, `--:` right) which GitHub honours
// when rendering.
func mdTable(b *strings.Builder, header []string, rows [][]string, aligns []mdAlign) {
	n := len(header)
	width := make([]int, n)
	for i, h := range header {
		width[i] = runeLen(h)
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if w := runeLen(row[i]); w > width[i] {
				width[i] = w
			}
		}
	}
	alignOf := func(i int) mdAlign {
		if aligns != nil && i < len(aligns) {
			return aligns[i]
		}
		return alignLeft
	}

	writeRow := func(cells []string) {
		b.WriteByte('|')
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteByte(' ')
			b.WriteString(pad(cell, width[i], alignOf(i)))
			b.WriteString(" |")
		}
		b.WriteByte('\n')
	}

	writeRow(header)
	b.WriteByte('|')
	for i := 0; i < n; i++ {
		b.WriteByte(' ')
		if alignOf(i) == alignRight {
			dashes := width[i] - 1
			if dashes < 1 {
				dashes = 1
			}
			b.WriteString(strings.Repeat("-", dashes))
			b.WriteString(": |")
		} else {
			b.WriteString(strings.Repeat("-", width[i]))
			b.WriteString(" |")
		}
	}
	b.WriteByte('\n')
	for _, row := range rows {
		writeRow(row)
	}
}

func runeLen(s string) int { return len([]rune(s)) }

func pad(s string, w int, a mdAlign) string {
	gap := w - runeLen(s)
	if gap <= 0 {
		return s
	}
	if a == alignRight {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

func renderCSV(w io.Writer, rep Report) error {
	cw := csv.NewWriter(w)
	rows := [][]string{{
		"strategy", "total_return", "cagr", "ann_vol", "sharpe", "sortino",
		"max_drawdown", "calmar", "final_value", "trades", "turnover", "total_cost", "exposure",
	}}
	for _, l := range rep.Lines {
		rows = append(rows, []string{
			l.Strategy,
			f(l.TotalReturn), f(l.CAGR), f(l.AnnVol), f(l.Sharpe), f(l.Sortino),
			f(l.MaxDrawdown), f(l.Calmar), f(l.FinalValue), strconv.Itoa(l.Trades),
			f(l.Turnover), f(l.TotalCost), f(l.Exposure),
		})
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func renderJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func pct(x float64) string   { return strconv.FormatFloat(x*100, 'f', 2, 64) + "%" }
func num(x float64) string   { return strconv.FormatFloat(x, 'f', 2, 64) }
func f(x float64) string     { return strconv.FormatFloat(x, 'f', 4, 64) }
func money(x float64) string { return "₹" + strconv.FormatFloat(x, 'f', 2, 64) }

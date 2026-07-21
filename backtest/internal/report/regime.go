package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RegimeMeta describes a regime-analysis run.
type RegimeMeta struct {
	Symbol    string   `json:"symbol"`
	Strategy  string   `json:"strategy"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Bars      int      `json:"bars"`
	TrendMA   int      `json:"trend_ma"`
	VolWindow int      `json:"vol_window"`
	Notes     []string `json:"notes,omitempty"`
}

// RegimeBucket is the strategy's performance within one market state, alongside
// buy-and-hold over the same days.
type RegimeBucket struct {
	Group       string  `json:"group"`
	Name        string  `json:"name"`
	Days        int     `json:"days"`
	StratReturn float64 `json:"strategy_return"`
	BenchReturn float64 `json:"benchmark_return"`
	StratSharpe float64 `json:"strategy_sharpe"`
}

// Regime is the full regime-analysis report.
type Regime struct {
	Meta    RegimeMeta     `json:"meta"`
	Buckets []RegimeBucket `json:"buckets"`
}

// RenderRegime writes rg to w as "md", "csv" or "json".
func RenderRegime(w io.Writer, rg Regime, format string) error {
	switch format {
	case "md", "markdown", "":
		return renderRegimeMarkdown(w, rg)
	case "csv":
		return renderRegimeCSV(w, rg)
	case "json":
		return renderRegimeJSON(w, rg)
	default:
		return fmt.Errorf("report: unknown format %q (want md|csv|json)", format)
	}
}

func renderRegimeMarkdown(w io.Writer, rg Regime) error {
	var b strings.Builder
	m := rg.Meta
	fmt.Fprintf(&b, "# Regime analysis — %s (%s)\n\n", m.Symbol, m.Strategy)
	fmt.Fprintf(&b, "- Period: %s → %s (%d bars)\n", m.Start, m.End, m.Bars)
	fmt.Fprintf(&b, "- Trend split: price vs its %d-bar average; volatility split: %d-bar realised vol vs sample median\n\n",
		m.TrendMA, m.VolWindow)

	header := []string{"Regime", "Days", "Strategy", "Buy & hold", "Edge", "Sharpe"}
	aligns := []mdAlign{alignLeft, alignRight, alignRight, alignRight, alignRight, alignRight}
	rows := make([][]string, 0, len(rg.Buckets))
	for _, bk := range rg.Buckets {
		rows = append(rows, []string{
			bk.Group + " · " + bk.Name,
			strconv.Itoa(bk.Days),
			pct(bk.StratReturn), pct(bk.BenchReturn), pct(bk.StratReturn - bk.BenchReturn),
			num(bk.StratSharpe),
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

func renderRegimeCSV(w io.Writer, rg Regime) error {
	cw := csv.NewWriter(w)
	rows := [][]string{{"group", "regime", "days", "strategy_return", "benchmark_return", "edge", "strategy_sharpe"}}
	for _, bk := range rg.Buckets {
		rows = append(rows, []string{
			bk.Group, bk.Name, strconv.Itoa(bk.Days),
			ff(bk.StratReturn), ff(bk.BenchReturn), ff(bk.StratReturn - bk.BenchReturn), ff(bk.StratSharpe),
		})
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func renderRegimeJSON(w io.Writer, rg Regime) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rg)
}

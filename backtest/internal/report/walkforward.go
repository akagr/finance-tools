package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// WFMeta describes a walk-forward run.
type WFMeta struct {
	Symbol    string   `json:"symbol"`
	Strategy  string   `json:"strategy"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Bars      int      `json:"bars"`
	Folds     int      `json:"folds"`
	Optimised bool     `json:"optimised"`
	Rolling   bool     `json:"rolling,omitempty"`
	Metric    string   `json:"metric,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// Fold is one out-of-sample segment: the strategy's result over that slice of
// the timeline versus the buy-and-hold benchmark over the same slice. When the
// run re-optimises parameters per fold, Params records the winning settings that
// were chosen on the *prior* (training) data and then tested here.
type Fold struct {
	Index       int     `json:"index"`
	Start       string  `json:"start"`
	End         string  `json:"end"`
	Params      string  `json:"params,omitempty"`
	StratReturn float64 `json:"strategy_return"`
	BenchReturn float64 `json:"benchmark_return"`
	StratSharpe float64 `json:"strategy_sharpe"`
	StratMaxDD  float64 `json:"strategy_max_drawdown"`
	Beat        bool    `json:"beat_benchmark"`
}

// WalkForward is the full walk-forward report: one Fold per out-of-sample
// segment, in chronological order.
type WalkForward struct {
	Meta  WFMeta `json:"meta"`
	Folds []Fold `json:"folds"`
}

// RenderWalkForward writes wf to w as "md", "csv" or "json".
func RenderWalkForward(w io.Writer, wf WalkForward, format string) error {
	switch format {
	case "md", "markdown", "":
		return renderWFMarkdown(w, wf)
	case "csv":
		return renderWFCSV(w, wf)
	case "json":
		return renderWFJSON(w, wf)
	default:
		return fmt.Errorf("report: unknown format %q (want md|csv|json)", format)
	}
}

func renderWFMarkdown(w io.Writer, wf WalkForward) error {
	m := wf.Meta
	var b strings.Builder
	title := "Walk-forward"
	if m.Optimised {
		title = "Walk-forward optimisation"
	}
	fmt.Fprintf(&b, "# %s — %s (%s)\n\n", title, m.Symbol, m.Strategy)
	fmt.Fprintf(&b, "- Period: %s → %s (%d bars)\n", m.Start, m.End, m.Bars)
	fmt.Fprintf(&b, "- Out-of-sample folds: %d\n", m.Folds)
	if m.Optimised {
		fmt.Fprintf(&b, "- Parameters re-fit each fold on prior data, chosen by **%s**\n", m.Metric)
		window := "anchored (expanding)"
		if m.Rolling {
			window = "rolling (fixed trailing window)"
		}
		fmt.Fprintf(&b, "- Training window: %s\n", window)
	}
	b.WriteByte('\n')

	withParams := m.Optimised
	header := []string{"Fold", "Period"}
	aligns := []mdAlign{alignLeft, alignLeft}
	if withParams {
		header = append(header, "Params")
		aligns = append(aligns, alignLeft)
	}
	header = append(header, "Strategy", "Buy & hold", "Edge", "Sharpe", "Max DD", "Beat?")
	aligns = append(aligns, alignRight, alignRight, alignRight, alignRight, alignRight, alignLeft)

	rows := make([][]string, 0, len(wf.Folds))
	for _, f := range wf.Folds {
		beat := "no"
		if f.Beat {
			beat = "yes"
		}
		row := []string{strconv.Itoa(f.Index), f.Start + " → " + f.End}
		if withParams {
			row = append(row, f.Params)
		}
		row = append(row,
			pct(f.StratReturn), pct(f.BenchReturn), pct(f.StratReturn-f.BenchReturn),
			num(f.StratSharpe), pct(f.StratMaxDD), beat)
		rows = append(rows, row)
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

func renderWFCSV(w io.Writer, wf WalkForward) error {
	cw := csv.NewWriter(w)
	rows := [][]string{{
		"fold", "start", "end", "params", "strategy_return", "benchmark_return",
		"edge", "strategy_sharpe", "strategy_max_drawdown", "beat_benchmark",
	}}
	for _, f := range wf.Folds {
		rows = append(rows, []string{
			strconv.Itoa(f.Index), f.Start, f.End, f.Params,
			ff(f.StratReturn), ff(f.BenchReturn), ff(f.StratReturn - f.BenchReturn),
			ff(f.StratSharpe), ff(f.StratMaxDD), strconv.FormatBool(f.Beat),
		})
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func renderWFJSON(w io.Writer, wf WalkForward) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(wf)
}

// ff formats a float for CSV (shared spelling with report.go's f, duplicated to
// keep the walk-forward renderer self-contained).
func ff(x float64) string { return strconv.FormatFloat(x, 'f', 4, 64) }

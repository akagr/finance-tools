package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// historyScreen is the attribution table over time: every period between two
// valuations, with the change split into what was paid in and what was earned.
//
// A spreadsheet can show the first column. The last two are the reason for all
// the structure underneath.
type historyScreen struct {
	m *Model

	rows   []store.AttributionRow
	series []store.Point
	cursor int
}

func newHistoryScreen(m *Model) *historyScreen { return &historyScreen{m: m} }

func (h *historyScreen) Refresh() error {
	rows, err := h.m.store.Attribution()
	if err != nil {
		return err
	}
	h.rows = rows
	h.series, err = h.m.store.NetWorthSeries()
	if err != nil {
		return err
	}
	if h.cursor >= len(h.rows) {
		h.cursor = max(0, len(h.rows)-1)
	}
	return nil
}

func (h *historyScreen) Capturing() bool { return false }

func (h *historyScreen) Hints() []hint {
	return []hint{{"↑↓", "period"}}
}

func (h *historyScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		if h.cursor > 0 {
			h.cursor--
		}
	case "down", "j":
		if h.cursor < len(h.rows)-1 {
			h.cursor++
		}
	}
	return nil
}

func (h *historyScreen) View(width, height int) string {
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("History") + "\n")

	if len(h.rows) == 0 {
		b.WriteString("\n" + styMuted.Render(wrapIndent(
			"Nothing to compare yet. Once you have recorded balances on two different "+
				"dates, this screen shows each period between them, and how much of the change "+
				"was money you added versus what the portfolio earned.",
			min(width-4, 76), "  ")) + "\n")
		return b.String()
	}

	cols := []column{
		{title: "From", width: 11},
		{title: "To", width: 11},
		{title: "Net worth", width: 15, right: true},
		{title: "Change", width: 14, right: true},
		{title: "You added", width: 14, right: true},
		{title: "Earned", width: 14, right: true},
		{title: "Return", width: 8, right: true},
	}
	var cells [][]string
	var styles [][]lipgloss.Style
	for _, r := range h.rows {
		ret := "—"
		retStyle := styFaint
		// The period return is stated on the earned amount over the opening
		// value, which is the honest reading when the exact timing of
		// contributions inside the period is unknown.
		if r.StartValue != 0 {
			f := float64(r.MarketReturn) / float64(r.StartValue.Abs())
			ret, retStyle = fmtPctSigned(f), signStyle(int64(r.MarketReturn))
		}
		cells = append(cells, []string{
			r.Start, r.End, h.m.fmt(r.EndValue), h.m.fmts(r.Change),
			h.m.fmts(r.Contribution), h.m.fmts(r.MarketReturn), ret,
		})
		styles = append(styles, []lipgloss.Style{
			styMuted, lipgloss.NewStyle(), lipgloss.NewStyle(),
			signStyle(int64(r.Change)), signStyle(int64(r.Contribution)),
			signStyle(int64(r.MarketReturn)), retStyle,
		})
	}
	b.WriteString(renderTable(cols, cells, styles, h.cursor, true))

	b.WriteString("\n" + h.totals(width))
	b.WriteString("\n" + h.chart(width, height))
	return b.String()
}

func (h *historyScreen) totals(width int) string {
	var added, earned, change int64
	for _, r := range h.rows {
		added += int64(r.Contribution)
		earned += int64(r.MarketReturn)
		change += int64(r.Change)
	}
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("Across every period") + "\n")
	row := func(label string, v int64, note string) {
		b.WriteString("    " + styFieldLabel.Render(pad(label, 18, false)) +
			signStyle(v).Render(pad(h.m.fmts(amount(v)), 16, true)))
		if note != "" {
			b.WriteString("   " + styFaint.Render(note))
		}
		b.WriteString("\n")
	}
	row("You put in", added, "")
	row("Markets gave", earned, "")
	row("Total change", change, "")
	if change != 0 && added > 0 {
		b.WriteString("    " + styFaint.Render(fmt.Sprintf(
			"%.0f%% of everything you have built came from saving, not returns.",
			float64(added)/float64(change)*100)) + "\n")
	}
	return b.String()
}

func (h *historyScreen) chart(width, height int) string {
	var vals []float64
	for _, p := range h.series {
		if p.Known {
			vals = append(vals, p.Value.Float())
		}
	}
	if len(vals) < 3 {
		return ""
	}
	w := min(max(20, width-8), 72)
	return "  " + styFaint.Render(sparkline(vals, w)) + "\n" +
		"  " + styFaint.Render(fmt.Sprintf("%s → %s", h.series[0].Date, h.series[len(h.series)-1].Date)) + "\n"
}

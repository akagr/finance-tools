package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
	"github.com/charmbracelet/lipgloss"
)

// hint is one keybinding shown in the footer.
type hint struct {
	key  string
	desc string
}

// footer renders the contextual keybindings.
//
// It is always visible and always reflects the current screen. That is the
// deliberate answer to discoverability: the interface should never depend on the
// user having read documentation or memorised a key.
func footer(width int, hints []hint) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, styKey.Render(h.key)+" "+styKeyDesc.Render(h.desc))
	}
	line := strings.Join(parts, styFaint.Render("  ·  "))
	return lipgloss.NewStyle().Width(width).Render(line)
}

// fmtMoney renders an amount with the database's own grouping preference.
func fmtMoney(a money.Amount, g money.Grouping, decimals int) string {
	return symbolFor(g) + a.Format(decimals, g)
}

func symbolFor(g money.Grouping) string {
	if g == money.GroupIndian {
		return "₹"
	}
	return ""
}

// fmtSigned always shows the sign, because a change of zero and a change of
// minus zero mean different things to someone scanning a column.
func fmtSigned(a money.Amount, g money.Grouping, decimals int) string {
	s := fmtMoney(a.Abs(), g, decimals)
	switch {
	case a > 0:
		return "+" + s
	case a < 0:
		return "-" + s
	}
	return " " + s
}

// fmtPct renders a rate as a percentage.
func fmtPct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// fmtPctSigned renders a rate with an explicit sign.
func fmtPctSigned(f float64) string {
	if f >= 0 {
		return fmt.Sprintf("+%.1f%%", f*100)
	}
	return fmt.Sprintf("%.1f%%", f*100)
}

// humanDays turns a day count into the phrasing a person would use.
func humanDays(days float64) string {
	d := int(days + 0.5)
	switch {
	case d <= 0:
		return "today"
	case d == 1:
		return "1 day"
	case d < 60:
		return fmt.Sprintf("%d days", d)
	case d < 365:
		return fmt.Sprintf("%d months", (d+15)/30)
	default:
		years := float64(d) / 365.0
		return fmt.Sprintf("%.1f years", years)
	}
}

// daysSince reports how long ago a stored date was, relative to now. It is used
// only for phrasing on screen; every computed figure is dated from the data.
func daysSince(date string) int {
	t, err := time.Parse(store.DateFormat, date)
	if err != nil {
		return 0
	}
	return int(time.Since(t).Hours() / 24)
}

// column describes one column of a rendered table.
type column struct {
	title string
	width int
	right bool
}

// renderTable lays out rows in aligned columns with a highlighted cursor row.
//
// It is hand-rolled rather than using a table component because the cells here
// carry per-cell colour (gains green, losses red, unknowns dim) and a generic
// component would flatten that.
func renderTable(cols []column, rows [][]string, styles [][]lipgloss.Style, cursor int, showCursor bool) string {
	var b strings.Builder

	var head []string
	for _, c := range cols {
		head = append(head, pad(c.title, c.width, c.right))
	}
	b.WriteString("  " + styTableHead.Render(strings.Join(head, "  ")) + "\n")

	for i, row := range rows {
		var cells []string
		for j, cell := range row {
			if j >= len(cols) {
				break
			}
			text := pad(cell, cols[j].width, cols[j].right)
			if styles != nil && i < len(styles) && j < len(styles[i]) {
				text = styles[i][j].Render(text)
			}
			cells = append(cells, text)
		}
		line := strings.Join(cells, "  ")
		if showCursor && i == cursor {
			b.WriteString(stySelected.Render("▸ ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

// pad truncates or pads a cell to width, respecting display width so that
// currency symbols and box characters do not break alignment.
func pad(s string, width int, right bool) string {
	w := lipgloss.Width(s)
	if w > width && width > 1 {
		for lipgloss.Width(s) > width-1 && len(s) > 0 {
			s = s[:len(s)-1]
		}
		s += "…"
		w = lipgloss.Width(s)
	}
	gap := strings.Repeat(" ", max(0, width-w))
	if right {
		return gap + s
	}
	return s + gap
}

// bar draws a proportional bar, used for allocation. Length carries the
// information; there is no gradient or shading to decode.
//
// A negative fraction is drawn at its magnitude in the loss colour, so a debt
// reads as a bar of the size it actually is rather than as an empty row.
func bar(fraction float64, width int) string {
	neg := fraction < 0
	if neg {
		fraction = -fraction
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	solid := strings.Repeat("█", filled)
	if neg {
		solid = styLoss.Render(solid)
	}
	return solid + styFaint.Render(strings.Repeat("░", width-filled))
}

// wrapIndent wraps text and indents every line, including continuations, so a
// multi-line note stays visually attached to its bullet instead of falling back
// to the left margin.
func wrapIndent(s string, width int, indent string) string {
	lines := strings.Split(wrap(s, width), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

// sparkline compresses a series into one line, enough to see shape and
// direction without a chart.
func sparkline(values []float64, width int) string {
	if len(values) == 0 {
		return ""
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	pts := resample(values, width)
	lo, hi := pts[0], pts[0]
	for _, v := range pts {
		lo, hi = min(lo, v), max(hi, v)
	}
	var b strings.Builder
	for _, v := range pts {
		idx := 0
		if hi > lo {
			idx = int((v - lo) / (hi - lo) * float64(len(blocks)-1))
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

// resample picks n evenly spaced points from a series.
func resample(values []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	if len(values) <= n {
		return values
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = values[i*(len(values)-1)/(n-1)]
	}
	return out
}

// wrap breaks text to a width, for the explanatory lines that make a number
// mean something.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line == "" {
				line = word
			} else if len(line)+1+len(word) <= width {
				line += " " + word
			} else {
				lines = append(lines, line)
				line = word
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// addDays shifts a stored date by n days, returning the same string format.
func addDays(date string, n int) string {
	t, err := time.Parse(store.DateFormat, date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format(store.DateFormat)
}

// today is the current date in the stored format. It is used only to offer a
// default when the user is entering data, never to date a computed figure.
func today() string { return time.Now().Format(store.DateFormat) }

// parseDate validates a user-entered date in the stored format.
func parseDate(s string) (time.Time, error) { return time.Parse(store.DateFormat, s) }

// amount converts a raw scaled integer back to a money amount, for totals
// accumulated as int64.
func amount(v int64) money.Amount { return money.Amount(v) }

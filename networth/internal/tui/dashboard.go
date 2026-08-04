package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboard answers, in one screen, the question the spreadsheet could not:
// what is my net worth, how much of the change did I earn, and how much did I
// simply pay in.
type dashboard struct {
	m *Model

	summary  store.Summary
	balances []store.Balance
	series   []store.Point
	cursor   int

	// periodIdx selects which comparison period the headline uses. Being able to
	// switch between "since the last update" and "all time" matters because the
	// two tell genuinely different stories.
	periodIdx  int
	valuations []string
}

func newDashboard(m *Model) *dashboard { return &dashboard{m: m} }

var periodLabels = []string{"since last update", "1 year", "3 years", "all time"}

func (d *dashboard) Refresh() error {
	vals, err := d.m.store.ValuationDates()
	if err != nil {
		return err
	}
	d.valuations = vals

	sum, err := d.m.store.Summarise(d.periodStart())
	if err != nil {
		return err
	}
	d.summary = sum

	if sum.AsOf != "" {
		if d.balances, err = d.m.store.BalancesAt(sum.AsOf); err != nil {
			return err
		}
	}
	d.series, err = d.m.store.NetWorthSeries()
	return err
}

// periodStart maps the chosen period onto an actual valuation date. It always
// snaps to a real measurement: comparing against a date the portfolio was never
// valued on would invent a starting number.
func (d *dashboard) periodStart() string {
	if len(d.valuations) < 2 {
		return ""
	}
	switch periodLabels[d.periodIdx] {
	case "since last update":
		return d.valuations[len(d.valuations)-2]
	case "1 year":
		return d.snapBack(365)
	case "3 years":
		return d.snapBack(3 * 365)
	}
	return ""
}

// snapBack finds the latest valuation at least n days before the most recent
// one, falling back to the earliest available.
func (d *dashboard) snapBack(days int) string {
	last := d.valuations[len(d.valuations)-1]
	target := addDays(last, -days)
	best := d.valuations[0]
	for _, v := range d.valuations {
		if v <= target {
			best = v
		}
	}
	return best
}

func (d *dashboard) Focus() focusLevel { return focusIdle }

func (d *dashboard) Hints() []hint {
	return []hint{
		{"u", "update balances"},
		{"p", "period: " + periodLabels[d.periodIdx]},
		{"↑↓", "accounts"},
	}
}

func (d *dashboard) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "u":
		return func() tea.Msg { return switchViewMsg(viewUpdate) }
	case "p":
		d.periodIdx = (d.periodIdx + 1) % len(periodLabels)
		if err := d.Refresh(); err != nil {
			return func() tea.Msg { return errMsg{err} }
		}
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.inNetworth())-1 {
			d.cursor++
		}
	}
	return nil
}

func (d *dashboard) inNetworth() []store.Balance {
	var out []store.Balance
	for _, b := range d.balances {
		if b.InNetworth {
			out = append(out, b)
		}
	}
	return out
}

func (d *dashboard) View(width, height int) string {
	if d.summary.AsOf == "" {
		return d.emptyState(width)
	}
	var b strings.Builder
	b.WriteString(d.headline(width))
	b.WriteString("\n")
	b.WriteString(d.attribution(width))
	b.WriteString("\n")
	b.WriteString(d.returns(width))
	b.WriteString("\n")
	b.WriteString(d.accounts(width, height))
	if w := d.warnings(width); w != "" {
		b.WriteString("\n" + w)
	}
	return b.String()
}

// emptyState is a first run. It gives the one next step rather than an empty
// frame, because the most common reason a tool like this dies is that the first
// screen did not say what to do.
func (d *dashboard) emptyState(width int) string {
	var b strings.Builder
	b.WriteString("\n  " + styTitle.Render("Nothing recorded yet.") + "\n\n")
	b.WriteString(styMuted.Render(wrapIndent(
		"Add the accounts you want to track on the Accounts screen (press 4, then a), "+
			"then press 3 to enter their balances. Two rounds of balances, a few months apart, "+
			"is enough to start separating what you contributed from what you earned.",
		min(width-4, 76), "  ")) + "\n")
	return b.String()
}

// headline is the number people open the tool for.
func (d *dashboard) headline(width int) string {
	s := d.summary
	var b strings.Builder

	value := d.m.fmt(s.NetWorth)
	if !s.Known {
		value = "unknown"
	}
	title := styMuted.Render("Net worth")
	asOf := styFaint.Render("as of " + s.AsOf)
	gap := max(1, width-lipgloss.Width(title)-lipgloss.Width(asOf))
	b.WriteString("  " + title + strings.Repeat(" ", gap-2) + asOf + "\n")
	b.WriteString("  " + styBig.Render(value))

	if len(d.series) > 3 {
		var vals []float64
		for _, p := range d.series {
			if p.Known {
				vals = append(vals, p.Value.Float())
			}
		}
		spark := sparkline(vals, min(28, max(8, width-lipgloss.Width(value)-8)))
		b.WriteString("   " + styFaint.Render(spark))
	}
	b.WriteString("\n")

	if s.Assets != 0 || s.Liabilities != 0 {
		b.WriteString("  " + styFaint.Render(fmt.Sprintf("assets %s   liabilities %s",
			d.m.compact(s.Assets), d.m.compact(s.Liabilities))) + "\n")
	}
	return b.String()
}

// attribution is the whole reason this tool exists in place of a spreadsheet.
func (d *dashboard) attribution(width int) string {
	s := d.summary
	if s.Since == "" || s.Since == s.AsOf {
		return "  " + styMuted.Render("Record a second round of balances to see what you earned.") + "\n"
	}
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("Since "+s.Since) +
		styFaint.Render(fmt.Sprintf("  ·  %s", humanDays(s.Days))) + "\n")

	labelW, amtW := 22, 18
	row := func(label string, amt money.Amount, style lipgloss.Style, note string) {
		b.WriteString("    " + styFieldLabel.Render(pad(label, labelW, false)) +
			style.Render(pad(d.m.fmts(amt), amtW, true)))
		if note != "" {
			b.WriteString("   " + styFaint.Render(note))
		}
		b.WriteString("\n")
	}
	row("You put in", s.Contribution, signStyle(int64(s.Contribution)), "")
	row("Markets gave", s.MarketReturn, signStyle(int64(s.MarketReturn)),
		"what you actually earned")
	b.WriteString("    " + styFaint.Render(strings.Repeat("─", labelW+amtW)) + "\n")
	row("Change", s.Change, signStyle(int64(s.Change)), "")

	// The share of the change that was not earned is the insight a balance-only
	// sheet hides completely, so it is stated in words rather than left to be
	// worked out from the figures above.
	if s.Change != 0 && s.Contribution != 0 {
		share := float64(s.Contribution) / float64(s.Change)
		if share > 0 && share <= 1.5 {
			b.WriteString("    " + styFaint.Render(fmt.Sprintf(
				"%.0f%% of the change was money you added, not growth.", share*100)) + "\n")
		}
	}
	return b.String()
}

// returns shows both measures together, because either one alone misleads.
func (d *dashboard) returns(width int) string {
	s := d.summary
	if !s.HasXIRR && !s.HasTWR {
		return ""
	}
	var b strings.Builder
	b.WriteString("  " + styHeading.Render("Annualised return") + "\n")

	labelW, valW := 22, 18
	row := func(label, value, note string, style lipgloss.Style) {
		b.WriteString("    " + styFieldLabel.Render(pad(label, labelW, false)) +
			style.Render(pad(value, valW, true)))
		if note != "" {
			b.WriteString("   " + styFaint.Render(note))
		}
		b.WriteString("\n")
	}
	if s.HasXIRR {
		row("Money-weighted", fmtPct(s.XIRR), "your money's return, timing included",
			signStyle(int64(s.XIRR*1e6)))
	}
	if s.HasTWR {
		row("Time-weighted", fmtPct(s.TWR.Annualised), "the portfolio's own return",
			signStyle(int64(s.TWR.Annualised*1e6)))
	}
	if s.HasXIRR && s.HasTWR {
		gap := s.GapToXIRR()
		note := "contribution timing helped"
		if gap < 0 {
			note = "contribution timing hurt"
		}
		row("Difference", fmtPctSigned(gap), note, styMuted)
	}

	// Every time-weighted figure above is only as good as the number of times
	// the portfolio was actually measured. Saying so is not a caveat to bury.
	if s.HasTWR {
		msg := fmt.Sprintf("from %d valuations", s.ValuationPoints)
		if s.ValuationPoints < 6 {
			msg += " — update more often for a sharper split"
		}
		b.WriteString("    " + styFaint.Render(msg) + "\n")
	}
	return b.String()
}

func (d *dashboard) accounts(width, height int) string {
	rows := d.inNetworth()
	if len(rows) == 0 {
		return ""
	}
	var total float64
	for _, b := range rows {
		if b.Known && b.BaseValue > 0 {
			total += b.BaseValue.Float()
		}
	}

	// Leave room for whatever is above; show what fits, largest first.
	limit := max(3, height-22)
	shown := rows
	if len(shown) > limit {
		shown = shown[:limit]
	}
	if d.cursor >= len(shown) {
		d.cursor = max(0, len(shown)-1)
	}

	nameW := max(16, min(30, width-46))
	cols := []column{
		{title: "Account", width: nameW},
		{title: "Value", width: 16, right: true},
		{title: "Share", width: 6, right: true},
		{title: "", width: 12},
		{title: "Valued", width: 12},
	}
	var cells [][]string
	var styles [][]lipgloss.Style
	for _, b := range shown {
		// Shares are measured against gross assets, so a liability reads as
		// "this debt is a quarter of what I own" rather than as a share of a
		// net figure it helped shrink.
		share := 0.0
		if total > 0 && b.Known {
			share = b.BaseValue.Float() / total
		}
		value := d.m.fmt(b.BaseValue)
		valStyle := lipgloss.NewStyle()
		if !b.Known {
			value, valStyle = "unknown", styWarn
		}
		valued := b.AnchorDate
		valuedStyle := styFaint
		if valued == "" {
			valued, valuedStyle = "never", styWarn
		} else if daysSince(valued) > 200 {
			valuedStyle = styWarn
		}
		cells = append(cells, []string{b.Account, value, fmtPct(share), bar(share, 10), valued})
		styles = append(styles, []lipgloss.Style{
			lipgloss.NewStyle(), valStyle, styMuted, styFaint, valuedStyle,
		})
	}

	head := "  " + styHeading.Render("Accounts")
	if len(rows) > len(shown) {
		head += styFaint.Render(fmt.Sprintf("  (%d more)", len(rows)-len(shown)))
	}
	return head + "\n" + renderTable(cols, cells, styles, d.cursor, true)
}

// warnings surface anything that makes the figures above less solid than they
// look. They are shown on the main screen rather than hidden behind a command,
// because a caveat nobody sees is not a caveat.
func (d *dashboard) warnings(width int) string {
	s := d.summary
	var lines []string

	if s.UnknownAccounts > 0 {
		lines = append(lines, fmt.Sprintf(
			"%d account(s) could not be valued, so net worth is understated.", s.UnknownAccounts))
	}
	if s.UnexplainedShare > 0.15 {
		lines = append(lines, fmt.Sprintf(
			"%.0f%% of the portfolio moved with no contribution recorded, so that much of "+
				"\"markets gave\" may really be money you added.", s.UnexplainedShare*100))
	}
	var errs, warns int
	for _, i := range s.Issues {
		if i.Severity == "error" {
			errs++
		} else {
			warns++
		}
	}
	if errs > 0 {
		lines = append(lines, fmt.Sprintf("%d data problem(s) found — see Query ▸ quality.", errs))
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		text := wrapIndent(l, min(width-8, 74), "    ")
		b.WriteString("  " + styBanner.Render("! ") + styMuted.Render(strings.TrimLeft(text, " ")) + "\n")
	}
	return b.String()
}

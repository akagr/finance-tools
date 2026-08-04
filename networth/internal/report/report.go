// Package report renders a summary for reading outside the interface: in a
// terminal, in a document, or as input to something else.
//
// The same Summary that drives the dashboard drives every format here, so a
// figure can never differ between what is on screen and what is exported.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
)

// Data is everything a report can show.
type Data struct {
	Summary     store.Summary
	Balances    []store.Balance
	Attribution []store.AttributionRow
	Settings    db.Settings
	DBPath      string
}

// Text renders a plain-text report for a terminal.
func Text(w io.Writer, d Data) error {
	g, dec := d.Settings.Grouping, d.Settings.Decimals
	m := func(a money.Amount) string { return fmtMoney(a, g, dec) }
	ms := func(a money.Amount) string { return fmtSigned(a, g, dec) }
	s := d.Summary

	if s.AsOf == "" {
		fmt.Fprintln(w, "Nothing recorded yet. Run `networth` and add some accounts.")
		return nil
	}

	fmt.Fprintf(w, "Net worth as of %s\n", s.AsOf)
	fmt.Fprintf(w, "  %s\n", m(s.NetWorth))
	fmt.Fprintf(w, "  assets %s   liabilities %s\n\n", m(s.Assets), m(s.Liabilities))

	if s.Since != "" && s.Since != s.AsOf {
		fmt.Fprintf(w, "Since %s\n", s.Since)
		fmt.Fprintf(w, "  %-16s %18s\n", "You put in", ms(s.Contribution))
		fmt.Fprintf(w, "  %-16s %18s   what you actually earned\n", "Markets gave", ms(s.MarketReturn))
		fmt.Fprintf(w, "  %-16s %18s\n\n", "Change", ms(s.Change))
	}

	if s.HasXIRR || s.HasTWR {
		fmt.Fprintln(w, "Annualised return")
		if s.HasXIRR {
			fmt.Fprintf(w, "  %-16s %17s   your money's return, timing included\n",
				"Money-weighted", pct(s.XIRR))
		}
		if s.HasTWR {
			fmt.Fprintf(w, "  %-16s %17s   the portfolio's own return\n",
				"Time-weighted", pct(s.TWR.Annualised))
		}
		if s.HasXIRR && s.HasTWR {
			fmt.Fprintf(w, "  %-16s %17s   the effect of contribution timing\n",
				"Difference", pctSigned(s.GapToXIRR()))
		}
		fmt.Fprintf(w, "  from %d valuations\n\n", s.ValuationPoints)
	}

	if len(d.Balances) > 0 {
		fmt.Fprintln(w, "Accounts")
		for _, b := range d.Balances {
			if !b.InNetworth {
				continue
			}
			value := m(b.BaseValue)
			if !b.Known {
				value = "unknown"
			}
			fmt.Fprintf(w, "  %-28s %16s   %s\n", trunc(b.Account, 28), value, b.AnchorDate)
		}
		fmt.Fprintln(w)
	}

	writeCaveats(w, s)
	return nil
}

// Markdown renders a report for pasting into notes or a document.
func Markdown(w io.Writer, d Data) error {
	g, dec := d.Settings.Grouping, d.Settings.Decimals
	m := func(a money.Amount) string { return fmtMoney(a, g, dec) }
	ms := func(a money.Amount) string { return fmtSigned(a, g, dec) }
	s := d.Summary

	fmt.Fprintf(w, "# Net worth — %s\n\n", s.AsOf)
	if s.AsOf == "" {
		fmt.Fprintln(w, "Nothing recorded yet.")
		return nil
	}
	fmt.Fprintf(w, "**%s**\n\n", m(s.NetWorth))
	fmt.Fprintf(w, "| | |\n|---|---:|\n")
	fmt.Fprintf(w, "| Assets | %s |\n", m(s.Assets))
	fmt.Fprintf(w, "| Liabilities | %s |\n\n", m(s.Liabilities))

	if s.Since != "" && s.Since != s.AsOf {
		fmt.Fprintf(w, "## Since %s\n\n", s.Since)
		fmt.Fprintf(w, "| | |\n|---|---:|\n")
		fmt.Fprintf(w, "| You put in | %s |\n", ms(s.Contribution))
		fmt.Fprintf(w, "| Markets gave | %s |\n", ms(s.MarketReturn))
		fmt.Fprintf(w, "| Change | %s |\n\n", ms(s.Change))
	}

	if s.HasXIRR || s.HasTWR {
		fmt.Fprintf(w, "## Annualised return\n\n| | | |\n|---|---:|---|\n")
		if s.HasXIRR {
			fmt.Fprintf(w, "| Money-weighted (XIRR) | %s | your money's return, timing included |\n", pct(s.XIRR))
		}
		if s.HasTWR {
			fmt.Fprintf(w, "| Time-weighted (TWR) | %s | the portfolio's own return |\n", pct(s.TWR.Annualised))
		}
		if s.HasXIRR && s.HasTWR {
			fmt.Fprintf(w, "| Difference | %s | the effect of contribution timing |\n", pctSigned(s.GapToXIRR()))
		}
		fmt.Fprintf(w, "\nBased on %d valuations.\n\n", s.ValuationPoints)
	}

	if len(d.Attribution) > 0 {
		fmt.Fprintf(w, "## By period\n\n")
		fmt.Fprintf(w, "| From | To | Net worth | Change | You added | Earned |\n")
		fmt.Fprintf(w, "|---|---|---:|---:|---:|---:|\n")
		for _, r := range d.Attribution {
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n",
				r.Start, r.End, m(r.EndValue), ms(r.Change), ms(r.Contribution), ms(r.MarketReturn))
		}
		fmt.Fprintln(w)
	}

	var sb strings.Builder
	writeCaveats(&sb, s)
	if sb.Len() > 0 {
		fmt.Fprintf(w, "## Caveats\n\n%s\n", sb.String())
	}
	return nil
}

// CSV writes the per-period attribution, the table most likely to be taken
// somewhere else. Amounts are written in major units with full precision so
// nothing is lost on the way out.
func CSV(w io.Writer, d Data) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"start", "end", "start_value", "end_value",
		"change", "contribution", "market_return"}); err != nil {
		return err
	}
	for _, r := range d.Attribution {
		if err := cw.Write([]string{
			r.Start, r.End,
			r.StartValue.Rat().FloatString(2), r.EndValue.Rat().FloatString(2),
			r.Change.Rat().FloatString(2), r.Contribution.Rat().FloatString(2),
			r.MarketReturn.Rat().FloatString(2),
		}); err != nil {
			return err
		}
	}
	return cw.Error()
}

// jsonSummary is the machine-readable shape. Amounts are decimal strings, not
// floats, so an exact figure stays exact for whatever consumes it.
type jsonSummary struct {
	AsOf            string       `json:"as_of"`
	BaseCurrency    string       `json:"base_currency"`
	NetWorth        string       `json:"net_worth"`
	Assets          string       `json:"assets"`
	Liabilities     string       `json:"liabilities"`
	UnknownAccounts int          `json:"unknown_accounts"`
	Since           string       `json:"since,omitempty"`
	Change          string       `json:"change,omitempty"`
	Contribution    string       `json:"contribution,omitempty"`
	MarketReturn    string       `json:"market_return,omitempty"`
	XIRR            *float64     `json:"xirr,omitempty"`
	TWR             *float64     `json:"twr_annualised,omitempty"`
	ValuationPoints int          `json:"valuation_points"`
	Unexplained     float64      `json:"unexplained_share"`
	Accounts        []jsonAcct   `json:"accounts,omitempty"`
	Periods         []jsonPeriod `json:"periods,omitempty"`
	Issues          []db.Issue   `json:"issues,omitempty"`
}

type jsonAcct struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Value    string `json:"value"`
	Known    bool   `json:"known"`
	ValuedOn string `json:"valued_on,omitempty"`
}

type jsonPeriod struct {
	Start        string `json:"start"`
	End          string `json:"end"`
	EndValue     string `json:"end_value"`
	Change       string `json:"change"`
	Contribution string `json:"contribution"`
	MarketReturn string `json:"market_return"`
}

// JSON writes the whole report as structured data.
func JSON(w io.Writer, d Data) error {
	s := d.Summary
	out := jsonSummary{
		AsOf:            s.AsOf,
		BaseCurrency:    d.Settings.BaseCurrency,
		NetWorth:        s.NetWorth.Rat().FloatString(2),
		Assets:          s.Assets.Rat().FloatString(2),
		Liabilities:     s.Liabilities.Rat().FloatString(2),
		UnknownAccounts: s.UnknownAccounts,
		Since:           s.Since,
		ValuationPoints: s.ValuationPoints,
		Unexplained:     s.UnexplainedShare,
		Issues:          s.Issues,
	}
	if s.Since != "" {
		out.Change = s.Change.Rat().FloatString(2)
		out.Contribution = s.Contribution.Rat().FloatString(2)
		out.MarketReturn = s.MarketReturn.Rat().FloatString(2)
	}
	if s.HasXIRR {
		v := s.XIRR
		out.XIRR = &v
	}
	if s.HasTWR {
		v := s.TWR.Annualised
		out.TWR = &v
	}
	for _, b := range d.Balances {
		if !b.InNetworth {
			continue
		}
		out.Accounts = append(out.Accounts, jsonAcct{
			Name: b.Account, Kind: b.Kind, Currency: b.Currency,
			Value: b.BaseValue.Rat().FloatString(2), Known: b.Known, ValuedOn: b.AnchorDate,
		})
	}
	for _, r := range d.Attribution {
		out.Periods = append(out.Periods, jsonPeriod{
			Start: r.Start, End: r.End,
			EndValue:     r.EndValue.Rat().FloatString(2),
			Change:       r.Change.Rat().FloatString(2),
			Contribution: r.Contribution.Rat().FloatString(2),
			MarketReturn: r.MarketReturn.Rat().FloatString(2),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// writeCaveats states what would otherwise make the figures above look more
// certain than they are. A report that omits this is not shorter, it is wrong.
func writeCaveats(w io.Writer, s store.Summary) {
	var lines []string
	if s.UnknownAccounts > 0 {
		lines = append(lines, fmt.Sprintf(
			"%d account(s) could not be valued, so net worth is understated.", s.UnknownAccounts))
	}
	if s.ValuationPoints > 0 && s.ValuationPoints < 6 {
		lines = append(lines, fmt.Sprintf(
			"Only %d valuations exist, so the split between contributions and returns is coarse.",
			s.ValuationPoints))
	}
	if s.UnexplainedShare > 0.15 {
		lines = append(lines, fmt.Sprintf(
			"%.0f%% of the portfolio moved with no contribution recorded, so part of the "+
				"\"markets gave\" figure may be money that was paid in.", s.UnexplainedShare*100))
	}
	for _, i := range s.Issues {
		if i.Severity == "error" {
			lines = append(lines, i.Detail)
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "Caveats")
	for _, l := range lines {
		fmt.Fprintf(w, "  - %s\n", l)
	}
}

func fmtMoney(a money.Amount, g money.Grouping, dec int) string {
	sym := ""
	if g == money.GroupIndian {
		sym = "₹"
	}
	return sym + a.Format(dec, g)
}

func fmtSigned(a money.Amount, g money.Grouping, dec int) string {
	s := fmtMoney(a.Abs(), g, dec)
	switch {
	case a > 0:
		return "+" + s
	case a < 0:
		return "-" + s
	}
	return s
}

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + "%" }

func pctSigned(f float64) string {
	if f >= 0 {
		return "+" + pct(f)
	}
	return pct(f)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

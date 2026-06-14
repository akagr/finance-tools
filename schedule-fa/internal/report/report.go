// Package report renders a schedulefa.Report. Every format carries an audit
// trail (source FX amount, TTBR, and the rate date used) behind each INR figure,
// plus a reconciliation summary and manual-review flags.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
	"github.com/akagr/tax-tools/schedule-fa/internal/schedulefa"
)

// Format is an output format.
type Format string

const (
	Markdown Format = "md"
	CSV      Format = "csv"
	JSON     Format = "json"
)

const disclaimer = "Not tax advice. A working draft to verify before filing. " +
	"Schedule FA covers the CALENDAR year; peak values are approximate (mode C)."

// Renderer writes a report in one format.
type Renderer interface {
	Render(w io.Writer, r *schedulefa.Report) error
}

// For returns the Renderer for a format.
func For(f Format) (Renderer, error) {
	switch f {
	case Markdown:
		return mdRenderer{}, nil
	case CSV:
		return csvRenderer{}, nil
	case JSON:
		return jsonRenderer{}, nil
	default:
		return nil, fmt.Errorf("report: unknown format %q", f)
	}
}

// Write renders the report to dir/report.<ext> for each format and returns the
// paths written.
func Write(dir string, formats []Format, r *schedulefa.Report) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var paths []string
	for _, f := range formats {
		rnd, err := For(f)
		if err != nil {
			return paths, err
		}
		path := filepath.Join(dir, "report."+string(f))
		out, err := os.Create(path)
		if err != nil {
			return paths, err
		}
		err = rnd.Render(out, r)
		cerr := out.Close()
		if err != nil {
			return paths, err
		}
		if cerr != nil {
			return paths, cerr
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// --- Markdown ---

type mdRenderer struct{}

func (mdRenderer) Render(w io.Writer, r *schedulefa.Report) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "# Schedule FA — calendar year %d\n\n", r.Year)
	fmt.Fprintf(b, "> %s\n\n", disclaimer)

	fmt.Fprintf(b, "## Table A2 — Foreign Custodial Account\n\n")
	for _, a := range r.A2 {
		fmt.Fprintf(b, "- **Institution:** %s\n", a.Institution)
		fmt.Fprintf(b, "- **Address:** %s%s · country code %s\n", dash(a.Address), zipSuffix(a.ZIP), dash(a.CountryCode))
		fmt.Fprintf(b, "- **Account number:** %s · **Status:** %s · **Opened:** %s\n", dash(a.AccountNumber), dash(a.Status), dash(a.OpenDate))
		fmt.Fprintf(b, "- **Peak balance:** ₹%s · **Closing balance:** ₹%s · **Gross credited:** ₹%s\n", inr(a.PeakBalance.INR), inr(a.ClosingBalance.INR), inr(a.GrossCredited.INR))
		if a.NeedsReview {
			fmt.Fprintf(b, "- ⚠︎ _%s_\n", a.ReviewNote)
		}
		fmt.Fprintln(b)
	}

	fmt.Fprintf(b, "## Table A3 — Foreign Equity and Debt Interest\n\n")
	fmt.Fprintln(b, "| # | Entity | Country (code) | Acquired | Initial (INR) | Peak (INR) | Closing (INR) | Dividend (INR) | Proceeds (INR) | Review |")
	fmt.Fprintln(b, "|---|--------|----------------|----------|--------------:|-----------:|--------------:|---------------:|---------------:|:------:|")
	for i, row := range r.A3 {
		flag := ""
		if row.NeedsReview {
			flag = "⚠︎"
		}
		fmt.Fprintf(b, "| %d | %s | %s (%s) | %s | %s | %s | %s | %s | %s | %s |\n",
			i+1, row.EntityName, dash(row.CountryName), dash(row.CountryCode), dash(row.AcquiredOn),
			inr(row.InitialValue.INR), inr(row.PeakValue.INR), inr(row.ClosingValue.INR),
			inr(row.GrossDividend.INR), inr(row.SaleProceeds.INR), flag)
	}

	// Reconciliation summary.
	var tClose, tDiv, tProc = new(big.Rat), new(big.Rat), new(big.Rat)
	review := 0
	for _, row := range r.A3 {
		tClose.Add(tClose, ratOf(row.ClosingValue.INR))
		tDiv.Add(tDiv, ratOf(row.GrossDividend.INR))
		tProc.Add(tProc, ratOf(row.SaleProceeds.INR))
		if row.NeedsReview {
			review++
		}
	}
	fmt.Fprintf(b, "\n## Reconciliation\n\n")
	fmt.Fprintf(b, "- Securities (A3 rows): **%d**\n", len(r.A3))
	fmt.Fprintf(b, "- Rows needing manual review: **%d**\n", review)
	fmt.Fprintf(b, "- Total closing value: **₹%s**\n", money(tClose))
	fmt.Fprintf(b, "- Total gross dividend: **₹%s**\n", money(tDiv))
	fmt.Fprintf(b, "- Total sale proceeds: **₹%s**\n", money(tProc))

	// Audit trail.
	fmt.Fprintf(b, "\n## Audit trail\n\n")
	fmt.Fprint(b, "Each INR figure and the SBI TTBR (rate date actually used) behind it.\n\n")
	for i, row := range r.A3 {
		fmt.Fprintf(b, "### %d. %s\n\n", i+1, row.EntityName)
		if row.NeedsReview {
			fmt.Fprintf(b, "_Review: %s_\n\n", row.ReviewNote)
		}
		fmt.Fprintln(b, "| Figure | Source | TTBR | Rate date | INR |")
		fmt.Fprintln(b, "|--------|--------|-----:|-----------|----:|")
		writeAudit(b, "Initial value", row.InitialValue)
		writeAudit(b, "Peak value", row.PeakValue)
		writeAudit(b, "Closing value", row.ClosingValue)
		writeAudit(b, "Dividend", row.GrossDividend)
		writeAudit(b, "Sale proceeds", row.SaleProceeds)
		fmt.Fprintln(b)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func writeAudit(b *strings.Builder, label string, a schedulefa.Amount) {
	if len(a.Audit) == 0 {
		fmt.Fprintf(b, "| %s | — | — | — | %s |\n", label, inr(a.INR))
		return
	}
	for i, c := range a.Audit {
		name := label
		if i > 0 {
			name = ""
		}
		fmt.Fprintf(b, "| %s | %s %s | %s | %s | %s |\n",
			name, c.Source.Currency, money(ratOf(c.Source)), rate(c.Rate), rateDate(c), inr(c.Result))
	}
}

// --- CSV ---

type csvRenderer struct{}

func (csvRenderer) Render(w io.Writer, r *schedulefa.Report) error {
	b := &strings.Builder{}
	// Header mirrors Table A3 fields, in INR, for transcription into the utility.
	fmt.Fprintln(b, "country_name,country_code,entity_name,address,zip,nature,acquired_on,"+
		"initial_value_inr,peak_value_inr,closing_value_inr,gross_dividend_inr,sale_proceeds_inr,"+
		"peak_approximate,needs_review,review_note")
	for _, row := range r.A3 {
		fmt.Fprintf(b, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%t,%t,%s\n",
			q(row.CountryName), q(row.CountryCode), q(row.EntityName), q(row.Address), q(row.ZIP),
			q(row.NatureEntity), q(row.AcquiredOn),
			inr(row.InitialValue.INR), inr(row.PeakValue.INR), inr(row.ClosingValue.INR),
			inr(row.GrossDividend.INR), inr(row.SaleProceeds.INR),
			row.PeakApprox, row.NeedsReview, q(row.ReviewNote))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// --- JSON ---

type jsonRenderer struct{}

type jsonReport struct {
	Year       int         `json:"year"`
	Disclaimer string      `json:"disclaimer"`
	A2         []jsonA2Row `json:"a2"`
	A3         []jsonA3Row `json:"a3"`
}

type jsonA2Row struct {
	Institution    string `json:"institution"`
	Address        string `json:"address"`
	ZIP            string `json:"zip"`
	CountryCode    string `json:"country_code"`
	AccountNumber  string `json:"account_number"`
	Status         string `json:"status"`
	OpenDate       string `json:"account_opened"`
	PeakBalance    string `json:"peak_balance_inr"`
	ClosingBalance string `json:"closing_balance_inr"`
	GrossCredited  string `json:"gross_credited_inr"`
	NeedsReview    bool   `json:"needs_review"`
	ReviewNote     string `json:"review_note,omitempty"`
}

type jsonA3Row struct {
	CountryName   string       `json:"country_name"`
	CountryCode   string       `json:"country_code"`
	EntityName    string       `json:"entity_name"`
	Address       string       `json:"address,omitempty"`
	ZIP           string       `json:"zip,omitempty"`
	Nature        string       `json:"nature"`
	AcquiredOn    string       `json:"acquired_on"`
	InitialValue  string       `json:"initial_value_inr"`
	PeakValue     string       `json:"peak_value_inr"`
	PeakApprox    bool         `json:"peak_approximate"`
	ClosingValue  string       `json:"closing_value_inr"`
	GrossDividend string       `json:"gross_dividend_inr"`
	SaleProceeds  string       `json:"sale_proceeds_inr"`
	NeedsReview   bool         `json:"needs_review"`
	ReviewNote    string       `json:"review_note,omitempty"`
	Audit         []jsonAudit  `json:"audit,omitempty"`
}

type jsonAudit struct {
	Figure     string `json:"figure"`
	Currency   string `json:"source_currency"`
	SourceAmt  string `json:"source_amount"`
	TTBR       string `json:"ttbr"`
	RateDate   string `json:"rate_date"`
	ResultINR  string `json:"result_inr"`
}

func (jsonRenderer) Render(w io.Writer, r *schedulefa.Report) error {
	out := jsonReport{Year: r.Year, Disclaimer: disclaimer}
	for _, a := range r.A2 {
		out.A2 = append(out.A2, jsonA2Row{
			Institution: a.Institution, Address: a.Address, ZIP: a.ZIP, CountryCode: a.CountryCode,
			AccountNumber: a.AccountNumber, Status: a.Status, OpenDate: a.OpenDate,
			PeakBalance: inr(a.PeakBalance.INR), ClosingBalance: inr(a.ClosingBalance.INR),
			GrossCredited: inr(a.GrossCredited.INR), NeedsReview: a.NeedsReview, ReviewNote: a.ReviewNote,
		})
	}
	for _, row := range r.A3 {
		jr := jsonA3Row{
			CountryName:   row.CountryName,
			CountryCode:   row.CountryCode,
			EntityName:    row.EntityName,
			Address:       row.Address,
			ZIP:           row.ZIP,
			Nature:        row.NatureEntity,
			AcquiredOn:    row.AcquiredOn,
			InitialValue:  inr(row.InitialValue.INR),
			PeakValue:     inr(row.PeakValue.INR),
			PeakApprox:    row.PeakApprox,
			ClosingValue:  inr(row.ClosingValue.INR),
			GrossDividend: inr(row.GrossDividend.INR),
			SaleProceeds:  inr(row.SaleProceeds.INR),
			NeedsReview:   row.NeedsReview,
			ReviewNote:    row.ReviewNote,
		}
		jr.Audit = append(jr.Audit, auditLines("Initial value", row.InitialValue)...)
		jr.Audit = append(jr.Audit, auditLines("Peak value", row.PeakValue)...)
		jr.Audit = append(jr.Audit, auditLines("Closing value", row.ClosingValue)...)
		jr.Audit = append(jr.Audit, auditLines("Dividend", row.GrossDividend)...)
		jr.Audit = append(jr.Audit, auditLines("Sale proceeds", row.SaleProceeds)...)
		out.A3 = append(out.A3, jr)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func auditLines(figure string, a schedulefa.Amount) []jsonAudit {
	var out []jsonAudit
	for _, c := range a.Audit {
		out = append(out, jsonAudit{
			Figure:    figure,
			Currency:  string(c.Source.Currency),
			SourceAmt: money(ratOf(c.Source)),
			TTBR:      rate(c.Rate),
			RateDate:  rateDate(c),
			ResultINR: inr(c.Result),
		})
	}
	return out
}

// --- formatting helpers ---

func ratOf(m model.Money) *big.Rat {
	if m.Amount == nil {
		return new(big.Rat)
	}
	return m.Amount
}

func inr(m model.Money) string  { return money(ratOf(m)) }
func money(r *big.Rat) string   { return r.FloatString(2) }

func rate(r fx.Rate) string {
	if r.INRPerUnit == nil {
		return "—"
	}
	return r.INRPerUnit.FloatString(4)
}

func rateDate(c fx.Conversion) string {
	if c.RateDate.IsZero() {
		return "—"
	}
	return c.RateDate.Format("2006-01-02")
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func zipSuffix(zip string) string {
	if zip == "" {
		return ""
	}
	return " " + zip
}

// q quotes a CSV field if it contains a comma, quote, or newline.
func q(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

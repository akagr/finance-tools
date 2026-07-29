package report

import (
	"html/template"
	"io"

	"github.com/akagr/finance-tools/schedule-fa/internal/fsi"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/gains"
)

// fsiHTML produces a single self-contained, print-friendly Schedule FSI page,
// matching the Schedule FA report so the two print as one pack. Open it in a
// browser and "Print → Save as PDF".
type fsiHTML struct{}

type fsiHTMLData struct {
	Report    jsonFSIReport
	Period    string
	Countries []fsiHTMLCountry
	Disposals []fsiHTMLDisposal
	OtherSrc  []jsonOSLine
	OSAudit   []fsiHTMLAudit
}

// fsiHTMLCountry pairs a country block with the per-head notes, which the
// Markdown prints as a bullet list under the table.
type fsiHTMLCountry struct {
	jsonFSICountry
	Notes []fsiHeadNote
}

type fsiHeadNote struct {
	Head string
	Note string
}

// fsiHTMLDisposal is one closed lot plus its conversion lines, so the working
// behind the gain is visible without a second table.
type fsiHTMLDisposal struct {
	jsonDisposal
	Term  string
	Lines []auditLine
}

type fsiHTMLAudit struct {
	Security string
	Type     string
	Lines    []auditLine
}

func (fsiHTML) RenderFSI(w io.Writer, r *fsi.Report) error {
	view, err := fsiView(r)
	if err != nil {
		return err
	}
	data := fsiHTMLData{
		Report:   view,
		Period:   fmtDay(r.From) + " to " + fmtDay(r.To),
		OtherSrc: view.OtherSource,
	}

	for i, c := range r.Countries {
		hc := fsiHTMLCountry{jsonFSICountry: view.Countries[i]}
		for _, h := range c.Heads {
			if h.Note != "" {
				hc.Notes = append(hc.Notes, fsiHeadNote{Head: string(h.Head), Note: h.Note})
			}
		}
		data.Countries = append(data.Countries, hc)
	}

	for i, d := range r.Gains.Disposals {
		term := string(d.Term)
		if d.Term == gains.Long && d.PreRateCut {
			term += " (pre-23-Jul)"
		}
		hd := fsiHTMLDisposal{jsonDisposal: view.Disposals[i], Term: term}
		hd.Lines = append(hd.Lines, lineOf("Cost", d.Cost), lineOf("Proceeds", d.Proceeds))
		if d.Expense.Source.Amount != nil && d.Expense.Source.Amount.Sign() != 0 {
			hd.Lines = append(hd.Lines, lineOf("Transfer expenses", d.Expense))
		}
		data.Disposals = append(data.Disposals, hd)
	}

	for _, l := range r.OtherSource {
		a := fsiHTMLAudit{Security: dash(l.Security), Type: l.Type}
		for _, c := range l.Audit {
			a.Lines = append(a.Lines, auditLine{
				Source:   string(c.Source.Currency) + " " + money(ratOf(c.Source)),
				TTBR:     rate(c.Rate),
				RateDate: rateDate(c),
				Result:   inr(c.Result),
			})
		}
		data.OSAudit = append(data.OSAudit, a)
	}

	return fsiHTMLTemplate.Execute(w, data)
}

// lineOf renders one conversion as an audit line labelled by the leg it backs.
func lineOf(leg string, c fx.Conversion) auditLine {
	return auditLine{
		Figure:   leg,
		Source:   string(c.Source.Currency) + " " + money(ratOf(c.Source)),
		TTBR:     rate(c.Rate),
		RateDate: rateDate(c),
		Result:   inr(c.Result),
	}
}

var fsiHTMLTemplate = template.Must(
	template.New("fsi").
		Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
		Parse(fsiHTMLSource),
)

const fsiHTMLSource = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Schedule FSI — FY {{.Report.FinancialYear}}</title>
` + htmlStyle + `
</head>
<body>
<h1>Schedule FSI &amp; TR — FY {{.Report.FinancialYear}} (AY {{.Report.AssessmentYear}})</h1>
<p class="disclaimer">{{.Report.Disclaimer}}</p>
<p class="disclaimer">Period {{.Period}} · capital-gains FX method: <strong>{{.Report.CGMethod}}</strong></p>

{{range .Countries}}
<h2>{{.CountryName}} (country code {{.CountryCode}})</h2>
<table class="card">
  <tr><th>Taxpayer Identification Number</th><td>{{.TIN}}</td></tr>
</table>
<table>
  <thead><tr>
    <th>Head of income</th>
    <th class="num">(b) Income outside India</th>
    <th class="num">(c) Tax paid outside</th>
    <th class="num">(d) Tax payable in India</th>
    <th class="num">(e) Relief</th>
    <th>(f) DTAA article</th>
  </tr></thead>
  <tbody>
  {{range .Heads}}
    <tr>
      <td>{{.Head}}</td>
      <td class="num">{{.Income}}</td>
      <td class="num">{{.TaxPaidOutside}}</td>
      <td class="num">{{.TaxPayableIndia}}</td>
      <td class="num">{{.Relief}}</td>
      <td>{{if .DTAAArticle}}{{.DTAAArticle}}{{else}}—{{end}}</td>
    </tr>
  {{end}}
    <tr>
      <td><strong>Total</strong></td>
      <td class="num"><strong>{{.Total.Income}}</strong></td>
      <td class="num"><strong>{{.Total.TaxPaidOutside}}</strong></td>
      <td class="num"><strong>{{.Total.TaxPayableIndia}}</strong></td>
      <td class="num"><strong>{{.Total.Relief}}</strong></td>
      <td></td>
    </tr>
  </tbody>
</table>
{{range .Notes}}<p class="disclaimer">{{.Head}}: {{.Note}}</p>{{end}}
{{if .NeedsReview}}<p class="review">⚠︎ {{.ReviewNote}}</p>{{end}}
{{end}}

<h2>Schedule TR — summary of tax relief claimed</h2>
{{if .Report.TR}}
<table>
  <thead><tr>
    <th>Country (code)</th><th>TIN</th>
    <th class="num">Tax paid outside India</th><th class="num">Relief available</th><th>Claimed u/s</th>
  </tr></thead>
  <tbody>
  {{range .Report.TR}}
    <tr>
      <td>{{.CountryName}} ({{.CountryCode}})</td>
      <td>{{.TIN}}</td>
      <td class="num">{{.TaxPaidOutside}}</td>
      <td class="num">{{.ReliefAvailable}}</td>
      <td>{{.Section}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<table class="card">
  <tr><th>Total taxes paid outside India</th><td>₹{{.Report.TRTotals.TaxPaid}}</td></tr>
  <tr><th>Total relief available</th><td>₹{{.Report.TRTotals.Relief}}</td></tr>
  <tr><th>Of which u/s 90/90A</th><td>₹{{.Report.TRTotals.DTAA}}</td></tr>
</table>
{{else}}
<p class="disclaimer">No foreign tax was paid, so no relief is claimed.</p>
{{end}}

<h2>Tie-out — what these figures must match elsewhere in the return</h2>
<table>
  <thead><tr><th>Schedule</th><th>Row</th><th class="num">INR</th></tr></thead>
  <tbody>
    <tr><td>CG</td><td>A5 — short-term, assets other than A1–A4 (slab)</td><td class="num">{{.Report.TieOut.ScheduleCGA5}}</td></tr>
    <tr><td>CG</td><td>B8 — long-term, assets other than B1–B7 (12.5%)</td><td class="num">{{.Report.TieOut.ScheduleCGB8}}</td></tr>
    {{if ne .Report.TieOut.ScheduleCGB8PreCut "0.00"}}<tr><td>CG</td><td>B8 — long-term transferred before 23 Jul 2024 (20% with indexation)</td><td class="num">{{.Report.TieOut.ScheduleCGB8PreCut}}</td></tr>{{end}}
    <tr><td>OS</td><td>Gross dividends, payments in lieu and interest</td><td class="num">{{.Report.TieOut.ScheduleOS}}</td></tr>
  </tbody>
</table>

<h2>Capital gains — lot by lot</h2>
{{if .Disposals}}
<table>
  <thead><tr>
    <th>Security</th><th>Acquired</th><th>Transferred</th><th class="num">Qty</th><th>Term</th>
    <th class="num">Cost</th><th class="num">Proceeds</th><th class="num">Expenses</th><th class="num">Gain</th><th>Review</th>
  </tr></thead>
  <tbody>
  {{range .Disposals}}
    <tr>
      <td>{{.Security}}</td>
      <td>{{.Acquired}}</td>
      <td>{{.Transferred}}</td>
      <td class="num">{{.Quantity}}</td>
      <td>{{.Term}}</td>
      <td class="num">{{.CostINR}}</td>
      <td class="num">{{.ProceedsINR}}</td>
      <td class="num">{{.ExpenseINR}}</td>
      <td class="num">{{.GainINR}}</td>
      <td>{{if .NeedsReview}}<span class="flag">⚠︎</span>{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>

<p class="disclaimer">The SBI TTBR behind each leg, at the Rule 115 specified date.</p>
{{range .Disposals}}
<details open>
  <summary>{{.Security}} — acquired {{.Acquired}}, transferred {{.Transferred}}{{if .NeedsReview}} <span class="flag">⚠︎</span>{{end}}{{if .ReviewNote}} <span class="sub">— {{.ReviewNote}}</span>{{end}}</summary>
  <table class="audit">
    <thead><tr><th>Leg</th><th>Source</th><th class="num">TTBR</th><th>Rate date</th><th class="num">INR</th></tr></thead>
    <tbody>
    {{range .Lines}}
      <tr><td>{{.Figure}}</td><td>{{.Source}}</td><td class="num">{{.TTBR}}</td><td>{{.RateDate}}</td><td class="num">{{.Result}}</td></tr>
    {{end}}
    </tbody>
  </table>
</details>
{{end}}
{{else}}
<p class="disclaimer">No lots were closed in the year.</p>
{{end}}

<h2>Other Sources — distribution by distribution</h2>
{{if .OtherSrc}}
<table>
  <thead><tr><th>Type</th><th>Security</th><th>Country</th><th class="num">Income</th><th class="num">Foreign tax</th></tr></thead>
  <tbody>
  {{range .OtherSrc}}
    <tr>
      <td>{{.Type}}</td>
      <td>{{.Security}}</td>
      <td>{{if .CountryName}}{{.CountryName}}{{else}}{{.CountryCode}}{{end}}</td>
      <td class="num">{{.Income}}</td>
      <td class="num">{{.TaxPaid}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<table class="audit">
  <thead><tr><th>Security</th><th>Figure</th><th>Source</th><th class="num">TTBR</th><th>Rate date</th><th class="num">INR</th></tr></thead>
  <tbody>
  {{range .OSAudit}}{{$a := .}}
    {{range $j, $line := .Lines}}
    <tr>{{if eq $j 0}}<td{{if gt (len $a.Lines) 1}} rowspan="{{len $a.Lines}}"{{end}}>{{$a.Security}}</td>{{end}}<td>{{$a.Type}}</td><td>{{$line.Source}}</td><td class="num">{{$line.TTBR}}</td><td>{{$line.RateDate}}</td><td class="num">{{$line.Result}}</td></tr>
    {{end}}
  {{end}}
  </tbody>
</table>
{{else}}
<p class="disclaimer">No dividends or interest in the year.</p>
{{end}}

<h2>Form 67 worksheet</h2>
{{if .Report.Form67}}
<p class="disclaimer">Only income that actually bore foreign tax is listed — there is no credit to claim on the rest.</p>
<table>
  <thead><tr><th>Country (code)</th><th>Income type</th><th class="num">Income</th><th class="num">Foreign tax</th><th>DTAA article</th></tr></thead>
  <tbody>
  {{range .Report.Form67}}
    <tr>
      <td>{{.CountryName}} ({{.CountryCode}})</td>
      <td>{{.IncomeType}}</td>
      <td class="num">{{.Income}}</td>
      <td class="num">{{.TaxPaid}}</td>
      <td>{{if .Article}}{{.Article}}{{else}}—{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<p class="disclaimer">No foreign tax was paid, so Form 67 is not required.</p>
{{end}}

<h2>Assumptions</h2>
<ul>
{{range .Report.Assumptions}}<li>{{.}}</li>{{end}}
</ul>

<h2>Before you file</h2>
<ul>
{{range .Report.ReviewNotes}}<li>{{.}}</li>{{end}}
</ul>

<footer>{{.Report.Disclaimer}}</footer>
</body>
</html>
`

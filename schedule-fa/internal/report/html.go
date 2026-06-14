package report

import (
	"html/template"
	"io"
	"math/big"

	"github.com/akagr/tax-tools/schedule-fa/internal/schedulefa"
)

// htmlRenderer produces a single self-contained, print-friendly HTML page.
// Open it in a browser and "Print → Save as PDF" for a PDF copy.
type htmlRenderer struct{}

type htmlData struct {
	Report jsonReport
	Recon  recon
}

type recon struct {
	Securities    int
	NeedsReview   int
	TotalClosing  string
	TotalDividend string
	TotalProceeds string
}

func (htmlRenderer) Render(w io.Writer, r *schedulefa.Report) error {
	view := buildView(r)

	tClose, tDiv, tProc := new(big.Rat), new(big.Rat), new(big.Rat)
	review := 0
	for _, row := range r.A3 {
		tClose.Add(tClose, ratOf(row.ClosingValue.INR))
		tDiv.Add(tDiv, ratOf(row.GrossDividend.INR))
		tProc.Add(tProc, ratOf(row.SaleProceeds.INR))
		if row.NeedsReview {
			review++
		}
	}
	data := htmlData{
		Report: view,
		Recon: recon{
			Securities:    len(r.A3),
			NeedsReview:   review,
			TotalClosing:  money(tClose),
			TotalDividend: money(tDiv),
			TotalProceeds: money(tProc),
		},
	}
	return htmlTemplate.Execute(w, data)
}

var htmlTemplate = template.Must(
	template.New("report").
		Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
		Parse(htmlSource),
)

const htmlSource = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Schedule FA — {{.Report.Year}}</title>
<style>
  :root { --ink:#1a1a1a; --muted:#666; --line:#ddd; --warn:#a8400a; --bg:#fff; }
  * { box-sizing: border-box; }
  body { font: 14px/1.5 -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif;
         color: var(--ink); background: var(--bg); margin: 2rem auto; max-width: 1000px; padding: 0 1rem; }
  h1 { font-size: 1.5rem; margin: 0 0 .25rem; }
  h2 { font-size: 1.1rem; margin: 2rem 0 .5rem; border-bottom: 2px solid var(--ink); padding-bottom: .2rem; }
  .disclaimer { color: var(--muted); font-size: .85rem; margin: 0 0 1rem; }
  table { border-collapse: collapse; width: 100%; margin: .5rem 0; }
  th, td { border: 1px solid var(--line); padding: .35rem .5rem; text-align: left; vertical-align: top; }
  th { background: #f5f5f5; font-weight: 600; }
  td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .card td { border: none; padding: .15rem .5rem .15rem 0; }
  .card th { background: none; border: none; text-align: left; padding: .15rem 1rem .15rem 0; white-space: nowrap; color: var(--muted); font-weight: 500; }
  .review { color: var(--warn); font-size: .85rem; margin: .25rem 0; }
  .flag { color: var(--warn); font-weight: 700; }
  details { margin: .4rem 0; }
  summary { cursor: pointer; font-weight: 600; }
  summary .sub { color: var(--muted); font-weight: 400; }
  .audit th { background: #fafafa; }
  footer { color: var(--muted); font-size: .8rem; margin-top: 2rem; border-top: 1px solid var(--line); padding-top: .5rem; }
  @media print {
    body { margin: 0; max-width: none; font-size: 11px; }
    h2 { page-break-after: avoid; }
    tr, details { page-break-inside: avoid; }
    details[open] summary ~ * { display: revert; }
    details > *:not(summary) { display: block; }
    summary { list-style: none; }
  }
</style>
</head>
<body>
<h1>Schedule FA — calendar year {{.Report.Year}}</h1>
<p class="disclaimer">{{.Report.Disclaimer}}</p>

<h2>Table A2 — Foreign Custodial Account</h2>
{{range .Report.A2}}
<table class="card">
  <tr><th>Institution</th><td>{{.Institution}}</td></tr>
  <tr><th>Address</th><td>{{.Address}}{{if .ZIP}} {{.ZIP}}{{end}} · country code {{.CountryCode}}</td></tr>
  <tr><th>Account number</th><td>{{.AccountNumber}}</td></tr>
  <tr><th>Status / Opened</th><td>{{.Status}} · {{.OpenDate}}</td></tr>
  <tr><th>Peak balance</th><td>₹{{.PeakBalance}}</td></tr>
  <tr><th>Closing balance</th><td>₹{{.ClosingBalance}}</td></tr>
  <tr><th>Gross credited</th><td>₹{{.GrossCredited}}</td></tr>
</table>
{{if .NeedsReview}}<p class="review">⚠︎ {{.ReviewNote}}</p>{{end}}
{{end}}

<h2>Table A3 — Foreign Equity and Debt Interest</h2>
<table>
  <thead><tr>
    <th>#</th><th>Entity</th><th>Country (code)</th><th>Acquired</th>
    <th class="num">Initial</th><th class="num">Peak</th><th class="num">Closing</th>
    <th class="num">Dividend</th><th class="num">Proceeds</th><th>Review</th>
  </tr></thead>
  <tbody>
  {{range $i, $r := .Report.A3}}
    <tr>
      <td>{{add $i 1}}</td>
      <td>{{$r.EntityName}}</td>
      <td>{{$r.CountryName}} ({{$r.CountryCode}})</td>
      <td>{{$r.AcquiredOn}}</td>
      <td class="num">{{$r.InitialValue}}</td>
      <td class="num">{{$r.PeakValue}}</td>
      <td class="num">{{$r.ClosingValue}}</td>
      <td class="num">{{$r.GrossDividend}}</td>
      <td class="num">{{$r.SaleProceeds}}</td>
      <td>{{if $r.NeedsReview}}<span class="flag">⚠︎</span>{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>

<h2>Reconciliation</h2>
<table class="card">
  <tr><th>Securities (A3 rows)</th><td>{{.Recon.Securities}}</td></tr>
  <tr><th>Rows needing review</th><td>{{.Recon.NeedsReview}}</td></tr>
  <tr><th>Total closing value</th><td>₹{{.Recon.TotalClosing}}</td></tr>
  <tr><th>Total gross dividend</th><td>₹{{.Recon.TotalDividend}}</td></tr>
  <tr><th>Total sale proceeds</th><td>₹{{.Recon.TotalProceeds}}</td></tr>
</table>

<h2>Audit trail</h2>
<p class="disclaimer">Each INR figure and the SBI TTBR (rate date actually used) behind it.</p>
{{range $i, $r := .Report.A3}}
<details open>
  <summary>{{add $i 1}}. {{$r.EntityName}} {{if $r.NeedsReview}}<span class="flag">⚠︎</span>{{end}}
    {{if $r.ReviewNote}}<span class="sub">— {{$r.ReviewNote}}</span>{{end}}</summary>
  <table class="audit">
    <thead><tr><th>Figure</th><th>Source</th><th class="num">TTBR</th><th>Rate date</th><th class="num">INR</th></tr></thead>
    <tbody>
    {{range $r.Audit}}
      <tr><td>{{.Figure}}</td><td>{{.Currency}} {{.SourceAmt}}</td><td class="num">{{.TTBR}}</td><td>{{.RateDate}}</td><td class="num">{{.ResultINR}}</td></tr>
    {{end}}
    </tbody>
  </table>
</details>
{{end}}

<footer>{{.Report.Disclaimer}}</footer>
</body>
</html>
`

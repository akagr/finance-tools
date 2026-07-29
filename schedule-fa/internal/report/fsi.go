// Renderers for Schedule FSI / TR. They share the money and CSV helpers with the
// Schedule FA renderers in this package.
//
// Four artifacts come out of one run:
//
//	report-fsi.md    the working paper: the grid, the workings behind every
//	                 figure, the assumptions made, and what still needs review
//	report-fsi.csv   flat rows for transcription into the ITR utility
//	report-fsi.json  the full model, audit trail included
//	schedule-fsi.json  a fragment shaped like the ITD's own ITR-2 JSON schema
//	                 (ScheduleFSIDtls / ScheduleTR1), whole rupees
//
// The ITD utility has no partial-schedule import, so the transcription table is
// the primary artifact; the schema-shaped fragment is for anyone assembling a
// complete ITR JSON themselves.

package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/akagr/finance-tools/schedule-fa/internal/fsi"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/gains"
)

const fsiDisclaimer = "Not tax advice. A working draft to verify before filing. " +
	"Schedule FSI covers the FINANCIAL year and converts under Rule 115 — it is NOT " +
	"comparable figure-for-figure with Schedule FA."

// FSIRenderer writes a Schedule FSI report in one format.
type FSIRenderer interface {
	RenderFSI(w io.Writer, r *fsi.Report) error
}

// FSIFor returns the renderer for a format.
func FSIFor(f Format) (FSIRenderer, error) {
	switch f {
	case Markdown:
		return fsiMD{}, nil
	case CSV:
		return fsiCSV{}, nil
	case JSON:
		return fsiJSON{}, nil
	default:
		return nil, fmt.Errorf("report: unknown format %q for Schedule FSI (want md, csv or json)", f)
	}
}

// WriteFSI renders the report to dir/report-fsi.<ext> for each format, plus the
// ITD-schema-shaped schedule-fsi.json, and returns the paths written.
func WriteFSI(dir string, formats []Format, r *fsi.Report) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var paths []string
	for _, f := range formats {
		rnd, err := FSIFor(f)
		if err != nil {
			return paths, err
		}
		path := filepath.Join(dir, "report-fsi."+string(f))
		out, err := os.Create(path)
		if err != nil {
			return paths, err
		}
		err = rnd.RenderFSI(out, r)
		cerr := out.Close()
		if err != nil {
			return paths, err
		}
		if cerr != nil {
			return paths, cerr
		}
		paths = append(paths, path)
	}
	path := filepath.Join(dir, "schedule-fsi.json")
	out, err := os.Create(path)
	if err != nil {
		return paths, err
	}
	err = RenderITD(out, r)
	cerr := out.Close()
	if err != nil {
		return paths, err
	}
	if cerr != nil {
		return paths, cerr
	}
	return append(paths, path), nil
}

// --- Markdown ---

type fsiMD struct{}

func (fsiMD) RenderFSI(w io.Writer, r *fsi.Report) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "# Schedule FSI — FY %s (AY %s)\n\n", r.FYLabel(), r.AYLabel())
	fmt.Fprintf(b, "> %s\n\n", fsiDisclaimer)
	fmt.Fprintf(b, "Period: %s to %s · capital-gains FX method: **%s**\n\n",
		r.From.Format("2006-01-02"), r.To.Format("2006-01-02"), r.Method)

	for _, c := range r.Countries {
		fmt.Fprintf(b, "## %s (country code %s)\n\n", dash(c.CountryName), dash(c.CountryCode))
		fmt.Fprintf(b, "Taxpayer Identification Number: %s\n\n", dash(c.TIN))
		fmt.Fprintln(b, "| Head of income | (b) Income outside India | (c) Tax paid outside | (d) Tax payable in India | (e) Relief | (f) DTAA article |")
		fmt.Fprintln(b, "|----------------|-------------------------:|---------------------:|-------------------------:|-----------:|:----------------:|")
		for _, h := range c.Heads {
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", h.Head,
				money(h.Income), money(h.TaxPaidOutside), money(h.TaxPayableIndia),
				money(h.Relief), dash(h.DTAAArticle))
		}
		fmt.Fprintf(b, "| **Total** | **%s** | **%s** | **%s** | **%s** | |\n\n",
			money(c.Total.Income), money(c.Total.TaxPaidOutside),
			money(c.Total.TaxPayableIndia), money(c.Total.Relief))
		for _, h := range c.Heads {
			if h.Note != "" {
				fmt.Fprintf(b, "- _%s: %s_\n", h.Head, h.Note)
			}
		}
		if c.NeedsReview {
			fmt.Fprintf(b, "- ⚠︎ _%s_\n", c.ReviewNote)
		}
		fmt.Fprintln(b)
	}

	// Schedule TR.
	fmt.Fprintf(b, "## Schedule TR — summary of tax relief claimed\n\n")
	if len(r.TR) == 0 {
		fmt.Fprintln(b, "No foreign tax was paid, so no relief is claimed.")
		fmt.Fprintln(b)
	} else {
		fmt.Fprintln(b, "| Country (code) | TIN | Tax paid outside India | Relief available | Relief claimed u/s |")
		fmt.Fprintln(b, "|----------------|-----|-----------------------:|-----------------:|:------------------:|")
		for _, t := range r.TR {
			fmt.Fprintf(b, "| %s (%s) | %s | %s | %s | %s |\n", dash(t.CountryName), dash(t.CountryCode),
				dash(t.TIN), money(t.TaxPaidOutside), money(t.ReliefAvailable), t.Section)
		}
		fmt.Fprintf(b, "\n- Total taxes paid outside India: **₹%s**\n", money(r.TRTaxPaid))
		fmt.Fprintf(b, "- Total relief available: **₹%s** (of which u/s 90/90A: ₹%s)\n\n", money(r.TRRelief), money(r.TRDTAA))
	}

	// Tie-out.
	fmt.Fprintf(b, "## Tie-out — what these figures must match elsewhere in the return\n\n")
	fmt.Fprintln(b, "| Schedule | Row | INR |")
	fmt.Fprintln(b, "|----------|-----|----:|")
	fmt.Fprintf(b, "| CG | A5 — short-term, assets other than A1–A4 (slab) | %s |\n", money(r.TieOut.ScheduleCGA5))
	fmt.Fprintf(b, "| CG | B8 — long-term, assets other than B1–B7 (12.5%%) | %s |\n", money(r.TieOut.ScheduleCGB8))
	if r.TieOut.ScheduleCGB8PreCut.Sign() != 0 {
		fmt.Fprintf(b, "| CG | B8 — long-term transferred before 23 Jul 2024 (20%% with indexation) | %s |\n", money(r.TieOut.ScheduleCGB8PreCut))
	}
	fmt.Fprintf(b, "| OS | Gross dividends, payments in lieu and interest | %s |\n\n", money(r.TieOut.ScheduleOS))

	// Capital-gains workings.
	fmt.Fprintf(b, "## Capital gains — lot by lot\n\n")
	if len(r.Gains.Disposals) == 0 {
		fmt.Fprintln(b, "No lots were closed in the year.")
		fmt.Fprintln(b)
	} else {
		fmt.Fprintln(b, "| Security | Acquired | Transferred | Qty | Term | Cost (INR) | Proceeds (INR) | Expenses (INR) | Gain (INR) | Review |")
		fmt.Fprintln(b, "|----------|----------|-------------|----:|:----:|-----------:|---------------:|---------------:|-----------:|:------:|")
		for _, d := range r.Gains.Disposals {
			flag := ""
			if d.NeedsReview {
				flag = "⚠︎"
			}
			term := string(d.Term)
			if d.Term == gains.Long && d.PreRateCut {
				term += " (pre-23-Jul)"
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				firstNonEmptyStr(d.Instrument.Symbol, d.Instrument.Name),
				fmtDay(d.OpenDate), fmtDay(d.CloseDate), d.Quantity.FloatString(0), term,
				inr(d.Cost.Result), inr(d.Proceeds.Result), inr(d.Expense.Result),
				money(d.GainINR), flag)
		}
		fmt.Fprintln(b)
		fmt.Fprintln(b, "The rate behind each leg (SBI TTBR at the Rule 115 specified date):")
		fmt.Fprintln(b)
		fmt.Fprintln(b, "| Security | Leg | Source | TTBR | Rate date | INR |")
		fmt.Fprintln(b, "|----------|-----|--------|-----:|-----------|----:|")
		for _, d := range r.Gains.Disposals {
			sym := firstNonEmptyStr(d.Instrument.Symbol, d.Instrument.Name)
			writeConv(b, sym, "Cost", d.Cost)
			writeConv(b, sym, "Proceeds", d.Proceeds)
			if d.Expense.Source.Amount != nil && d.Expense.Source.Amount.Sign() != 0 {
				writeConv(b, sym, "Transfer expenses", d.Expense)
			}
		}
		fmt.Fprintln(b)
		for _, d := range r.Gains.Disposals {
			if d.NeedsReview {
				fmt.Fprintf(b, "- ⚠︎ **%s**: %s\n", firstNonEmptyStr(d.Instrument.Symbol, d.Instrument.Name), d.ReviewNote)
			}
		}
		fmt.Fprintln(b)
	}

	// Other sources.
	fmt.Fprintf(b, "## Other Sources — distribution by distribution\n\n")
	if len(r.OtherSource) == 0 {
		fmt.Fprintln(b, "No dividends or interest in the year.")
		fmt.Fprintln(b)
	} else {
		fmt.Fprintln(b, "| Type | Security | Country | Income (INR) | Foreign tax (INR) |")
		fmt.Fprintln(b, "|------|----------|---------|-------------:|------------------:|")
		for _, l := range r.OtherSource {
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", l.Type, dash(l.Security),
				dash(l.CountryName), money(l.Income), money(l.TaxPaid))
		}
		fmt.Fprintln(b)
		fmt.Fprintln(b, "| Security | Figure | Source | TTBR | Rate date | INR |")
		fmt.Fprintln(b, "|----------|--------|--------|-----:|-----------|----:|")
		for _, l := range r.OtherSource {
			for _, c := range l.Audit {
				fmt.Fprintf(b, "| %s | %s | %s %s | %s | %s | %s |\n", dash(l.Security), l.Type,
					c.Source.Currency, money(ratOf(c.Source)), rate(c.Rate), rateDate(c), inr(c.Result))
			}
		}
		fmt.Fprintln(b)
	}

	// Form 67 worksheet.
	fmt.Fprintf(b, "## Form 67 worksheet\n\n")
	if len(r.Form67) == 0 {
		fmt.Fprintln(b, "No foreign tax was paid, so Form 67 is not required.")
		fmt.Fprintln(b)
	} else {
		fmt.Fprintln(b, "Only income that actually bore foreign tax is listed — there is no credit to claim on the rest.")
		fmt.Fprintln(b)
		fmt.Fprintln(b, "| Country (code) | Income type | Income (INR) | Foreign tax (INR) | DTAA article |")
		fmt.Fprintln(b, "|----------------|-------------|-------------:|------------------:|:------------:|")
		for _, f := range r.Form67 {
			fmt.Fprintf(b, "| %s (%s) | %s | %s | %s | %s |\n", dash(f.CountryName), dash(f.CountryCode),
				f.IncomeType, money(f.Income), money(f.TaxPaid), dash(f.Article))
		}
		fmt.Fprintln(b)
	}

	fmt.Fprintf(b, "## Assumptions\n\n")
	for _, a := range r.Assumptions {
		fmt.Fprintf(b, "- %s\n", a)
	}
	fmt.Fprintf(b, "\n## Before you file\n\n")
	for _, n := range r.ReviewNotes {
		fmt.Fprintf(b, "- %s\n", n)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func writeConv(b *strings.Builder, sym, leg string, c fx.Conversion) {
	fmt.Fprintf(b, "| %s | %s | %s %s | %s | %s | %s |\n", sym, leg,
		c.Source.Currency, money(ratOf(c.Source)), rate(c.Rate), rateDate(c), inr(c.Result))
}

// --- CSV ---

type fsiCSV struct{}

func (fsiCSV) RenderFSI(w io.Writer, r *fsi.Report) error {
	b := &strings.Builder{}
	fmt.Fprintln(b, "country_name,country_code,tin,head,income_outside_india_inr,"+
		"tax_paid_outside_inr,tax_payable_india_inr,relief_inr,dtaa_article,needs_review,review_note")
	for _, c := range r.Countries {
		for _, h := range c.Heads {
			fmt.Fprintf(b, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%t,%s\n",
				q(c.CountryName), q(c.CountryCode), q(c.TIN), q(string(h.Head)),
				money(h.Income), money(h.TaxPaidOutside), money(h.TaxPayableIndia),
				money(h.Relief), q(h.DTAAArticle), c.NeedsReview, q(c.ReviewNote))
		}
		fmt.Fprintf(b, "%s,%s,%s,%s,%s,%s,%s,%s,,%t,%s\n",
			q(c.CountryName), q(c.CountryCode), q(c.TIN), "Total",
			money(c.Total.Income), money(c.Total.TaxPaidOutside),
			money(c.Total.TaxPayableIndia), money(c.Total.Relief),
			c.NeedsReview, q(c.ReviewNote))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// --- JSON ---

type fsiJSON struct{}

type jsonFSIReport struct {
	FinancialYear  string           `json:"financial_year"`
	AssessmentYear string           `json:"assessment_year"`
	Disclaimer     string           `json:"disclaimer"`
	CGMethod       string           `json:"capital_gains_fx_method"`
	Countries      []jsonFSICountry `json:"countries"`
	TR             []jsonTRRow      `json:"schedule_tr"`
	TRTotals       jsonTRTotals     `json:"schedule_tr_totals"`
	Form67         []jsonForm67     `json:"form_67"`
	Disposals      []jsonDisposal   `json:"capital_gains"`
	OtherSource    []jsonOSLine     `json:"other_sources"`
	TieOut         jsonTieOut       `json:"tie_out"`
	Assumptions    []string         `json:"assumptions"`
	ReviewNotes    []string         `json:"review_notes"`
}

type jsonFSICountry struct {
	CountryName string        `json:"country_name"`
	CountryCode string        `json:"country_code"`
	TIN         string        `json:"tin"`
	Heads       []jsonFSIHead `json:"heads"`
	Total       jsonFSIHead   `json:"total"`
	NeedsReview bool          `json:"needs_review"`
	ReviewNote  string        `json:"review_note,omitempty"`
}

type jsonFSIHead struct {
	Head            string `json:"head"`
	Income          string `json:"income_outside_india_inr"`
	TaxPaidOutside  string `json:"tax_paid_outside_inr"`
	TaxPayableIndia string `json:"tax_payable_india_inr"`
	Relief          string `json:"relief_inr"`
	DTAAArticle     string `json:"dtaa_article,omitempty"`
	Note            string `json:"note,omitempty"`
}

type jsonTRRow struct {
	CountryName     string `json:"country_name"`
	CountryCode     string `json:"country_code"`
	TIN             string `json:"tin"`
	TaxPaidOutside  string `json:"tax_paid_outside_inr"`
	ReliefAvailable string `json:"relief_available_inr"`
	Section         string `json:"relief_claimed_us"`
}

type jsonTRTotals struct {
	TaxPaid string `json:"total_tax_paid_outside_inr"`
	Relief  string `json:"total_relief_inr"`
	DTAA    string `json:"relief_us_90_90a_inr"`
}

type jsonForm67 struct {
	CountryName string `json:"country_name"`
	CountryCode string `json:"country_code"`
	IncomeType  string `json:"income_type"`
	Income      string `json:"income_inr"`
	TaxPaid     string `json:"tax_paid_inr"`
	Article     string `json:"dtaa_article,omitempty"`
}

type jsonDisposal struct {
	Security    string      `json:"security"`
	ISIN        string      `json:"isin,omitempty"`
	Acquired    string      `json:"acquired_on"`
	Transferred string      `json:"transferred_on"`
	Quantity    string      `json:"quantity"`
	Term        string      `json:"term"`
	PreRateCut  bool        `json:"transferred_before_23_jul_2024"`
	CostINR     string      `json:"cost_inr"`
	ProceedsINR string      `json:"proceeds_inr"`
	ExpenseINR  string      `json:"transfer_expenses_inr"`
	GainINR     string      `json:"gain_inr"`
	Audit       []jsonAudit `json:"audit"`
	NeedsReview bool        `json:"needs_review"`
	ReviewNote  string      `json:"review_note,omitempty"`
}

type jsonOSLine struct {
	Type        string      `json:"type"`
	Security    string      `json:"security"`
	CountryCode string      `json:"country_code"`
	Income      string      `json:"income_inr"`
	TaxPaid     string      `json:"tax_paid_inr"`
	Audit       []jsonAudit `json:"audit"`
}

type jsonTieOut struct {
	ScheduleCGA5       string `json:"schedule_cg_a5_stcg_inr"`
	ScheduleCGB8       string `json:"schedule_cg_b8_ltcg_inr"`
	ScheduleCGB8PreCut string `json:"schedule_cg_b8_ltcg_pre_23_jul_2024_inr"`
	ScheduleOS         string `json:"schedule_os_inr"`
}

func (fsiJSON) RenderFSI(w io.Writer, r *fsi.Report) error {
	out := jsonFSIReport{
		FinancialYear:  r.FYLabel(),
		AssessmentYear: r.AYLabel(),
		Disclaimer:     fsiDisclaimer,
		CGMethod:       string(r.Method),
		TRTotals: jsonTRTotals{
			TaxPaid: money(r.TRTaxPaid), Relief: money(r.TRRelief), DTAA: money(r.TRDTAA),
		},
		TieOut: jsonTieOut{
			ScheduleCGA5:       money(r.TieOut.ScheduleCGA5),
			ScheduleCGB8:       money(r.TieOut.ScheduleCGB8),
			ScheduleCGB8PreCut: money(r.TieOut.ScheduleCGB8PreCut),
			ScheduleOS:         money(r.TieOut.ScheduleOS),
		},
		Assumptions: r.Assumptions,
		ReviewNotes: r.ReviewNotes,
	}
	for _, c := range r.Countries {
		jc := jsonFSICountry{
			CountryName: c.CountryName, CountryCode: c.CountryCode, TIN: c.TIN,
			Total:       jsonHead(c.Total),
			NeedsReview: c.NeedsReview, ReviewNote: c.ReviewNote,
		}
		for _, h := range c.Heads {
			jc.Heads = append(jc.Heads, jsonHead(h))
		}
		out.Countries = append(out.Countries, jc)
	}
	for _, t := range r.TR {
		out.TR = append(out.TR, jsonTRRow{
			CountryName: t.CountryName, CountryCode: t.CountryCode, TIN: t.TIN,
			TaxPaidOutside: money(t.TaxPaidOutside), ReliefAvailable: money(t.ReliefAvailable),
			Section: t.Section,
		})
	}
	for _, f := range r.Form67 {
		out.Form67 = append(out.Form67, jsonForm67{
			CountryName: f.CountryName, CountryCode: f.CountryCode, IncomeType: f.IncomeType,
			Income: money(f.Income), TaxPaid: money(f.TaxPaid), Article: f.Article,
		})
	}
	for _, d := range r.Gains.Disposals {
		jd := jsonDisposal{
			Security:    firstNonEmptyStr(d.Instrument.Symbol, d.Instrument.Name),
			ISIN:        d.Instrument.ISIN,
			Acquired:    fmtDay(d.OpenDate),
			Transferred: fmtDay(d.CloseDate),
			Quantity:    d.Quantity.FloatString(0),
			Term:        string(d.Term),
			PreRateCut:  d.PreRateCut,
			CostINR:     inr(d.Cost.Result),
			ProceedsINR: inr(d.Proceeds.Result),
			ExpenseINR:  inr(d.Expense.Result),
			GainINR:     money(d.GainINR),
			NeedsReview: d.NeedsReview,
			ReviewNote:  d.ReviewNote,
		}
		jd.Audit = append(jd.Audit, auditOf("Cost", d.Cost), auditOf("Proceeds", d.Proceeds))
		out.Disposals = append(out.Disposals, jd)
	}
	for _, l := range r.OtherSource {
		jl := jsonOSLine{
			Type: l.Type, Security: l.Security, CountryCode: l.CountryCode,
			Income: money(l.Income), TaxPaid: money(l.TaxPaid),
		}
		for _, c := range l.Audit {
			jl.Audit = append(jl.Audit, auditOf(l.Type, c))
		}
		out.OtherSource = append(out.OtherSource, jl)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func jsonHead(h fsi.HeadRow) jsonFSIHead {
	return jsonFSIHead{
		Head: string(h.Head), Income: money(h.Income),
		TaxPaidOutside: money(h.TaxPaidOutside), TaxPayableIndia: money(h.TaxPayableIndia),
		Relief: money(h.Relief), DTAAArticle: h.DTAAArticle, Note: h.Note,
	}
}

func auditOf(figure string, c fx.Conversion) jsonAudit {
	return jsonAudit{
		Figure:    figure,
		Currency:  string(c.Source.Currency),
		SourceAmt: money(ratOf(c.Source)),
		TTBR:      rate(c.Rate),
		RateDate:  rateDate(c),
		ResultINR: inr(c.Result),
	}
}

// --- ITD schema fragment ---

// itdFSIIncType mirrors the schema's ScheduleFSIIncType. Amounts are whole
// rupees because the schema types them as integers.
type itdFSIIncType struct {
	IncFrmOutsideInd    int64  `json:"IncFrmOutsideInd"`
	TaxPaidOutsideInd   int64  `json:"TaxPaidOutsideInd"`
	TaxPayableinInd     int64  `json:"TaxPayableinInd"`
	TaxReliefinInd      int64  `json:"TaxReliefinInd"`
	DTAAReliefUs90or90A string `json:"DTAAReliefUs90or90A,omitempty"`
}

type itdFSIDtls struct {
	CountryName               string        `json:"CountryName"`
	CountryCodeExcludingIndia string        `json:"CountryCodeExcludingIndia"`
	TaxIdentificationNo       string        `json:"TaxIdentificationNo"`
	IncFromSal                itdFSIIncType `json:"IncFromSal"`
	IncFromHP                 itdFSIIncType `json:"IncFromHP"`
	IncCapGain                itdFSIIncType `json:"IncCapGain"`
	IncOthSrc                 itdFSIIncType `json:"IncOthSrc"`
	TotalCountryWise          itdFSIIncType `json:"TotalCountryWise"`
}

type itdTRRow struct {
	CountryName               string `json:"CountryName"`
	CountryCodeExcludingIndia string `json:"CountryCodeExcludingIndia"`
	TaxIdentificationNo       string `json:"TaxIdentificationNo"`
	TaxPaidOutsideIndia       int64  `json:"TaxPaidOutsideIndia"`
	TaxReliefOutsideIndia     int64  `json:"TaxReliefOutsideIndia"`
	ReliefClaimedUsSection    string `json:"ReliefClaimedUsSection,omitempty"`
}

type itdFragment struct {
	Note        string `json:"_note"`
	ScheduleFSI struct {
		ScheduleFSIDtls []itdFSIDtls `json:"ScheduleFSIDtls"`
	} `json:"ScheduleFSI"`
	ScheduleTR1 struct {
		ScheduleTR                   []itdTRRow `json:"ScheduleTR"`
		TotalTaxPaidOutsideIndia     int64      `json:"TotalTaxPaidOutsideIndia"`
		TotalTaxReliefOutsideIndia   int64      `json:"TotalTaxReliefOutsideIndia"`
		TaxReliefOutsideIndiaDTAA    int64      `json:"TaxReliefOutsideIndiaDTAA"`
		TaxReliefOutsideIndiaNotDTAA int64      `json:"TaxReliefOutsideIndiaNotDTAA"`
	} `json:"ScheduleTR1"`
}

// RenderITD writes the Schedule FSI / TR fragment using the ITD's own field
// names and whole-rupee integers, for pasting into a hand-assembled ITR JSON.
func RenderITD(w io.Writer, r *fsi.Report) error {
	var frag itdFragment
	frag.Note = "Schedule FSI/TR fragment in the ITD ITR-2 schema's field names, whole rupees. " +
		"Not a complete ITR JSON, and the utility cannot import a single schedule. " + fsiDisclaimer
	frag.ScheduleFSI.ScheduleFSIDtls = []itdFSIDtls{}
	frag.ScheduleTR1.ScheduleTR = []itdTRRow{}

	for _, c := range r.Countries {
		d := itdFSIDtls{
			CountryName:               c.CountryName,
			CountryCodeExcludingIndia: c.CountryCode,
			TaxIdentificationNo:       c.TIN,
			TotalCountryWise:          itdHead(c.Total),
		}
		for _, h := range c.Heads {
			switch h.Head {
			case fsi.HeadSalary:
				d.IncFromSal = itdHead(h)
			case fsi.HeadHouseProperty:
				d.IncFromHP = itdHead(h)
			case fsi.HeadCapitalGains:
				d.IncCapGain = itdHead(h)
			case fsi.HeadOtherSources:
				d.IncOthSrc = itdHead(h)
			}
		}
		frag.ScheduleFSI.ScheduleFSIDtls = append(frag.ScheduleFSI.ScheduleFSIDtls, d)
	}
	for _, t := range r.TR {
		frag.ScheduleTR1.ScheduleTR = append(frag.ScheduleTR1.ScheduleTR, itdTRRow{
			CountryName:               t.CountryName,
			CountryCodeExcludingIndia: t.CountryCode,
			TaxIdentificationNo:       t.TIN,
			TaxPaidOutsideIndia:       rupees(t.TaxPaidOutside),
			TaxReliefOutsideIndia:     rupees(t.ReliefAvailable),
			ReliefClaimedUsSection:    t.Section,
		})
	}
	frag.ScheduleTR1.TotalTaxPaidOutsideIndia = rupees(r.TRTaxPaid)
	frag.ScheduleTR1.TotalTaxReliefOutsideIndia = rupees(r.TRRelief)
	frag.ScheduleTR1.TaxReliefOutsideIndiaDTAA = rupees(r.TRDTAA)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(frag)
}

func itdHead(h fsi.HeadRow) itdFSIIncType {
	return itdFSIIncType{
		IncFrmOutsideInd:    rupees(h.Income),
		TaxPaidOutsideInd:   rupees(h.TaxPaidOutside),
		TaxPayableinInd:     rupees(h.TaxPayableIndia),
		TaxReliefinInd:      rupees(h.Relief),
		DTAAReliefUs90or90A: h.DTAAArticle,
	}
}

// rupees rounds an exact amount to whole rupees, half away from zero, as the
// ITR utility's integer fields require.
func rupees(r *big.Rat) int64 {
	if r == nil {
		return 0
	}
	half := big.NewRat(1, 2)
	adj := new(big.Rat).Add(new(big.Rat).Abs(r), half)
	n := new(big.Int).Quo(adj.Num(), adj.Denom())
	if r.Sign() < 0 {
		return -n.Int64()
	}
	return n.Int64()
}

func fmtDay(t interface{ Format(string) string }) string {
	s := t.Format("2006-01-02")
	if strings.HasPrefix(s, "0001-01-01") {
		return "—"
	}
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

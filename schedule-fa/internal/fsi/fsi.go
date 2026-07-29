// Package fsi assembles Schedule FSI (income from outside India and tax relief),
// Schedule TR (summary of relief claimed) and a Form 67 worksheet from a parsed
// IBKR statement.
//
// Schedule FSI is a country × head-of-income grid. Per country the ITR asks for
// the taxpayer's foreign TIN, then for each of Salary, House Property, Capital
// Gains and Other Sources:
//
//	(b) income from outside India, INCLUDED IN PART B-TI
//	(c) tax paid outside India
//	(d) tax payable on that income under normal provisions in India
//	(e) relief available = lower of (c) and (d)
//	(f) the relevant DTAA article, if relief is claimed u/s 90/90A
//
// Two things follow from column (b)'s wording that shape this package. First,
// the figure must be the same rupee amount that is offered to tax in Schedule CG
// / Schedule OS — so the report carries those tie-out figures alongside. Second,
// it is income, not a loss: a net capital loss belongs in Schedule CG/CFL, and
// is flagged rather than reported as a negative.
//
// Column (d) cannot be derived from a broker statement — it depends on the
// taxpayer's total income, regime, slab, surcharge and cess. It is therefore
// computed from EXPLICIT assumptions (Options.MarginalRate and friends) which
// are echoed in the report, never guessed silently.
//
// What this package does not do: only Capital Gains and Other Sources are
// populated, because that is all an IBKR statement evidences. Salary (RSU
// perquisite) and House Property rows are emitted empty for completeness.
package fsi

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/akagr/finance-tools/schedule-fa/internal/entities"
	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/gains"
	"github.com/akagr/finance-tools/schedule-fa/internal/itr"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
	"github.com/akagr/finance-tools/schedule-fa/internal/rule115"
)

// Head is a head of income as ITR-2's Schedule FSI lists them. (ITR-3 adds
// Business or Profession; ITR-2 does not have that row.)
type Head string

const (
	HeadSalary        Head = "Salary"
	HeadHouseProperty Head = "House Property"
	HeadCapitalGains  Head = "Capital Gains"
	HeadOtherSources  Head = "Other Sources"
)

// Heads is the fixed row order of Schedule FSI in ITR-2.
var Heads = []Head{HeadSalary, HeadHouseProperty, HeadCapitalGains, HeadOtherSources}

// India-US DTAA articles cited in column (f).
const (
	ArticleDividends   = "10"
	ArticleInterest    = "11"
	ArticleCapitalGain = "13"
)

// usCountryCode is the ITR country code for the United States (Canada is 1).
const usCountryCode = "2"

// LTCGRate is the s.112 rate for transfers on or after 23 Jul 2024 (12.5%).
var LTCGRate = big.NewRat(25, 2)

// LTCGRatePreCut is the s.112 rate for transfers before 23 Jul 2024 (20%, with
// indexation — which this tool does not compute).
var LTCGRatePreCut = big.NewRat(20, 1)

// SurchargeCap is the 15% ceiling on surcharge for s.112/112A income.
var SurchargeCap = big.NewRat(15, 1)

// Options carries the inputs that a broker statement cannot supply.
type Options struct {
	FYStart      int      // financial year start, e.g. 2025 for FY 2025-26
	TIN          string   // taxpayer's identification number in the source country
	MarginalRate *big.Rat // slab rate applied to slab-taxed foreign income, percent
	Surcharge    *big.Rat // percent of tax
	Cess         *big.Rat // percent of tax + surcharge (4 unless told otherwise)
	CGMethod     gains.Method
	Entities     *entities.Store
}

// HeadRow is one head-of-income row within a country block.
type HeadRow struct {
	Head            Head
	Income          *big.Rat // col (b)
	TaxPaidOutside  *big.Rat // col (c)
	TaxPayableIndia *big.Rat // col (d)
	Relief          *big.Rat // col (e)
	DTAAArticle     string   // col (f)
	Note            string
}

// CountryRow is one country block of Schedule FSI.
type CountryRow struct {
	CountryName string
	CountryCode string
	TIN         string
	Heads       []HeadRow
	Total       HeadRow
	NeedsReview bool
	ReviewNote  string
}

// TRRow is one country line of Schedule TR.
type TRRow struct {
	CountryName     string
	CountryCode     string
	TIN             string
	TaxPaidOutside  *big.Rat
	ReliefAvailable *big.Rat
	Section         string // 90 for a DTAA country, 91 otherwise
}

// Form67Line is one line of the Form 67 worksheet: the foreign income and the
// tax paid on it, by source and type, with the rate dates actually used.
type Form67Line struct {
	CountryName string
	CountryCode string
	IncomeType  string
	Income      *big.Rat
	TaxPaid     *big.Rat
	Article     string
	RateNote    string
}

// OSLine is the Other Sources breakdown behind column (b).
type OSLine struct {
	CountryName string
	CountryCode string
	Type        string // Dividend / Payment in lieu / Interest / Unattributed withholding
	Security    string
	Income      *big.Rat
	TaxPaid     *big.Rat
	Audit       []fx.Conversion
}

// TieOut carries the figures Schedule FSI must agree with elsewhere in the
// return, so a mismatch is visible before filing rather than after a notice.
type TieOut struct {
	ScheduleCGA5       *big.Rat // STCG on assets other than A1–A4 (slab)
	ScheduleCGB8       *big.Rat // LTCG on assets other than B1–B7 (12.5%)
	ScheduleCGB8PreCut *big.Rat // LTCG on pre-23-Jul-2024 transfers (20% with indexation)
	ScheduleOS         *big.Rat // dividends + payments in lieu + interest, gross
}

// Report is the complete Schedule FSI output for one financial year.
type Report struct {
	FYStart     int
	From        time.Time
	To          time.Time
	Method      gains.Method
	Countries   []CountryRow
	TR          []TRRow
	TRTaxPaid   *big.Rat
	TRRelief    *big.Rat
	TRDTAA      *big.Rat
	Form67      []Form67Line
	Gains       *gains.Summary
	OtherSource []OSLine
	TieOut      TieOut
	Assumptions []string
	ReviewNotes []string
}

// FYLabel renders the financial year the way the ITR does, e.g. "2025-26".
func (r *Report) FYLabel() string {
	return fmt.Sprintf("%d-%02d", r.FYStart, (r.FYStart+1)%100)
}

// AYLabel renders the assessment year, e.g. "2026-27".
func (r *Report) AYLabel() string {
	return fmt.Sprintf("%d-%02d", r.FYStart+1, (r.FYStart+2)%100)
}

// bucket accumulates one country's figures while the report is assembled.
type bucket struct {
	name, code string
	stcg       *big.Rat
	ltcg       *big.Rat
	ltcgPreCut *big.Rat
	cgTaxPaid  *big.Rat
	osIncome   *big.Rat
	osTaxPaid  *big.Rat
	hasDiv     bool
	hasInt     bool
	intPaid    *big.Rat // interest debited, in the trade currency
	intPaidCur model.Currency
	notes      []string
}

func newBucket(name, code string) *bucket {
	return &bucket{
		name: name, code: code,
		stcg: new(big.Rat), ltcg: new(big.Rat), ltcgPreCut: new(big.Rat),
		cgTaxPaid: new(big.Rat), osIncome: new(big.Rat), osTaxPaid: new(big.Rat),
		intPaid: new(big.Rat),
	}
}

// Build assembles Schedules FSI and TR, the Form 67 worksheet, and the tie-out
// figures from a statement parsed over the financial year.
func Build(st *model.Statement, store fx.Store, opts Options) (*Report, error) {
	rep := &Report{
		FYStart:   opts.FYStart,
		From:      st.From,
		To:        st.To,
		Method:    opts.CGMethod,
		TRTaxPaid: new(big.Rat), TRRelief: new(big.Rat), TRDTAA: new(big.Rat),
		TieOut: TieOut{
			ScheduleCGA5: new(big.Rat), ScheduleCGB8: new(big.Rat),
			ScheduleCGB8PreCut: new(big.Rat), ScheduleOS: new(big.Rat),
		},
	}

	gs, err := gains.Compute(st, store, opts.FYStart, opts.CGMethod)
	if err != nil {
		return nil, err
	}
	rep.Gains = gs
	rep.ReviewNotes = append(rep.ReviewNotes, gs.ReviewNotes...)

	buckets := map[string]*bucket{}
	var order []string
	get := func(name, code string) *bucket {
		key := code + "|" + name
		b, ok := buckets[key]
		if !ok {
			b = newBucket(name, code)
			buckets[key] = b
			order = append(order, key)
		}
		return b
	}

	// --- Capital gains, by source country of the issuer ---
	for _, d := range gs.Disposals {
		name, code := countryOf(d.Instrument, opts.Entities)
		b := get(name, code)
		switch {
		case d.Term == gains.Short:
			b.stcg.Add(b.stcg, d.GainINR)
		case d.PreRateCut:
			b.ltcgPreCut.Add(b.ltcgPreCut, d.GainINR)
		default:
			b.ltcg.Add(b.ltcg, d.GainINR)
		}
		if d.NeedsReview {
			b.notes = append(b.notes, d.Instrument.Symbol+": "+d.ReviewNote)
		}
	}

	// --- Other Sources: dividends, payments in lieu, interest ---
	for _, dv := range st.Dividends {
		name, code := countryOf(dv.Instrument, opts.Entities)
		b := get(name, code)
		typ := "Dividend"
		if dv.Kind == model.DividendInLieu {
			typ = "Payment in lieu of dividend"
		}
		inc, err := rule115.Convert(store, dv.Gross, rule115.Dividend, dv.PayDate, opts.FYStart)
		if err != nil {
			return nil, fmt.Errorf("fsi: %s dividend on %s: %w", dv.Instrument.Symbol, dv.PayDate.Format("2006-01-02"), err)
		}
		line := OSLine{
			CountryName: name, CountryCode: code, Type: typ,
			Security: firstNonEmpty(dv.Instrument.Symbol, dv.Instrument.Name),
			Income:   ratOf(inc.Result), TaxPaid: new(big.Rat),
			Audit: []fx.Conversion{inc},
		}
		b.osIncome.Add(b.osIncome, ratOf(inc.Result))
		if dv.Kind == model.DividendInLieu {
			b.notes = append(b.notes, firstNonEmpty(dv.Instrument.Symbol, "a holding")+
				": payment in lieu of dividend (shares were on loan) — withheld at the US statutory 30%, not the 25% treaty rate, and not a qualified dividend")
		} else {
			b.hasDiv = true
		}
		if !dv.Withholding.IsZero() {
			// Rule 128(8): foreign tax converts at the month-end before the
			// month it was deducted, which for withholding is the pay date.
			tax, err := rule115.Convert(store, dv.Withholding, rule115.ForeignTax, dv.PayDate, opts.FYStart)
			if err != nil {
				return nil, fmt.Errorf("fsi: %s withholding on %s: %w", dv.Instrument.Symbol, dv.PayDate.Format("2006-01-02"), err)
			}
			line.TaxPaid = ratOf(tax.Result)
			line.Audit = append(line.Audit, tax)
			b.osTaxPaid.Add(b.osTaxPaid, ratOf(tax.Result))
		}
		rep.OtherSource = append(rep.OtherSource, line)
	}

	for _, in := range st.Interest {
		name, code := countryOf(in.Instrument, opts.Entities)
		if code == "" {
			// Broker credit interest carries no security: it is sourced where
			// the broker entity is.
			_, _, iname, icode := itr.Institution(st.Account.IBEntity)
			if icode != "" {
				name, code = iname, icode
			}
		}
		b := get(name, code)
		if in.Amount.Amount != nil && in.Amount.Amount.Sign() < 0 {
			// Debit interest is an expense, not negative income. Accumulate it
			// so several rows produce one note rather than one note each.
			b.intPaid.Add(b.intPaid, new(big.Rat).Abs(in.Amount.Amount))
			b.intPaidCur = in.Amount.Currency
			continue
		}
		// Rule 115 pins "other income" to 31 March of the previous year, so
		// broker credit interest converts at the year-end rate.
		inc, err := rule115.Convert(store, in.Amount, rule115.OtherIncome, in.Date, opts.FYStart)
		if err != nil {
			return nil, fmt.Errorf("fsi: interest on %s: %w", in.Date.Format("2006-01-02"), err)
		}
		b.osIncome.Add(b.osIncome, ratOf(inc.Result))
		b.hasInt = true
		rep.OtherSource = append(rep.OtherSource, OSLine{
			CountryName: name, CountryCode: code, Type: "Interest",
			Security: firstNonEmpty(in.Instrument.Symbol, in.Description),
			Income:   ratOf(inc.Result), TaxPaid: new(big.Rat),
			Audit: []fx.Conversion{inc},
		})
	}

	for _, w := range st.UnmatchedWithholding {
		name, code := countryOf(w.Instrument, opts.Entities)
		if code == "" {
			_, _, iname, icode := itr.Institution(st.Account.IBEntity)
			if icode != "" {
				name, code = iname, icode
			}
		}
		b := get(name, code)
		tax, err := rule115.Convert(store, w.Amount, rule115.ForeignTax, w.Date, opts.FYStart)
		if err != nil {
			return nil, fmt.Errorf("fsi: unattributed withholding on %s: %w", w.Date.Format("2006-01-02"), err)
		}
		b.osTaxPaid.Add(b.osTaxPaid, ratOf(tax.Result))
		b.notes = append(b.notes, "withholding of "+ratStr(ratOf(tax.Result))+
			" INR on "+w.Date.Format("2006-01-02")+" could not be matched to a distribution — confirm the income it relates to before claiming the credit")
		rep.OtherSource = append(rep.OtherSource, OSLine{
			CountryName: name, CountryCode: code, Type: "Unattributed withholding",
			Security: firstNonEmpty(w.Instrument.Symbol, w.Description),
			Income:   new(big.Rat), TaxPaid: ratOf(tax.Result),
			Audit: []fx.Conversion{tax},
		})
	}

	sort.Strings(order)
	for _, key := range order {
		b := buckets[key]
		rep.Countries = append(rep.Countries, b.countryRow(opts))
		rep.TieOut.ScheduleCGA5.Add(rep.TieOut.ScheduleCGA5, b.stcg)
		rep.TieOut.ScheduleCGB8.Add(rep.TieOut.ScheduleCGB8, b.ltcg)
		rep.TieOut.ScheduleCGB8PreCut.Add(rep.TieOut.ScheduleCGB8PreCut, b.ltcgPreCut)
		rep.TieOut.ScheduleOS.Add(rep.TieOut.ScheduleOS, b.osIncome)
	}

	rep.buildTR()
	rep.buildForm67(buckets, order)
	rep.Assumptions = assumptions(opts)
	rep.ReviewNotes = append(rep.ReviewNotes, globalNotes(rep)...)
	return rep, nil
}

// countryRow turns an accumulated bucket into the four ITR rows plus the total.
func (b *bucket) countryRow(opts Options) CountryRow {
	row := CountryRow{CountryName: b.name, CountryCode: b.code, TIN: opts.TIN}

	// Capital gains. Losses are not "income included in Part B-TI": only
	// positive buckets are reported, and the loss is flagged for Schedule CG.
	cgIncome := new(big.Rat)
	for _, v := range []*big.Rat{b.stcg, b.ltcg, b.ltcgPreCut} {
		if v.Sign() > 0 {
			cgIncome.Add(cgIncome, v)
		}
	}
	cgTax := new(big.Rat)
	cgTax.Add(cgTax, taxOn(b.stcg, opts.MarginalRate, opts.Surcharge, opts.Cess))
	cgTax.Add(cgTax, taxOn(b.ltcg, LTCGRate, capped(opts.Surcharge), opts.Cess))
	cgTax.Add(cgTax, taxOn(b.ltcgPreCut, LTCGRatePreCut, capped(opts.Surcharge), opts.Cess))

	osIncome := new(big.Rat)
	if b.osIncome.Sign() > 0 {
		osIncome.Set(b.osIncome)
	}
	osTax := taxOn(osIncome, opts.MarginalRate, opts.Surcharge, opts.Cess)

	for _, h := range Heads {
		hr := HeadRow{Head: h, Income: new(big.Rat), TaxPaidOutside: new(big.Rat),
			TaxPayableIndia: new(big.Rat), Relief: new(big.Rat)}
		switch h {
		case HeadCapitalGains:
			hr.Income = cgIncome
			hr.TaxPaidOutside = b.cgTaxPaid
			hr.TaxPayableIndia = cgTax
			hr.Relief = lower(hr.TaxPaidOutside, hr.TaxPayableIndia)
			switch {
			case hr.Relief.Sign() > 0:
				hr.DTAAArticle = ArticleCapitalGain
			case hr.Income.Sign() > 0 && b.code == usCountryCode:
				hr.Note = "the US does not tax a non-resident alien's gains on listed stock, so no credit arises"
			case hr.Income.Sign() > 0:
				hr.Note = "no foreign tax was withheld on these gains — confirm whether the source country taxes them"
			}
		case HeadOtherSources:
			hr.Income = osIncome
			hr.TaxPaidOutside = b.osTaxPaid
			hr.TaxPayableIndia = osTax
			hr.Relief = lower(hr.TaxPaidOutside, hr.TaxPayableIndia)
			if hr.Relief.Sign() > 0 {
				switch {
				case b.hasDiv:
					hr.DTAAArticle = ArticleDividends
				case b.hasInt:
					hr.DTAAArticle = ArticleInterest
				}
			}
			if b.hasDiv && b.hasInt {
				hr.Note = "dividends and interest are aggregated in this row; cite the article for the income actually bearing the tax (10 dividends, 11 interest)"
			}
		}
		row.Heads = append(row.Heads, hr)
	}

	row.Total = HeadRow{Head: "Total", Income: new(big.Rat), TaxPaidOutside: new(big.Rat),
		TaxPayableIndia: new(big.Rat), Relief: new(big.Rat)}
	for _, hr := range row.Heads {
		row.Total.Income.Add(row.Total.Income, hr.Income)
		row.Total.TaxPaidOutside.Add(row.Total.TaxPaidOutside, hr.TaxPaidOutside)
		row.Total.TaxPayableIndia.Add(row.Total.TaxPayableIndia, hr.TaxPayableIndia)
		row.Total.Relief.Add(row.Total.Relief, hr.Relief)
	}

	notes := dedupe(b.notes)
	if b.intPaid.Sign() > 0 {
		notes = append(notes, "interest PAID of "+ratStr(b.intPaid)+" "+string(b.intPaidCur)+
			" is an expense, not income — it is excluded here; any s.57 claim must be made manually")
	}
	if b.code == "" {
		notes = append(notes, "country code unknown — set it via --entities (the ITR utility rejects a blank or wrong code)")
	}
	if b.stcg.Sign() < 0 || b.ltcg.Sign() < 0 || b.ltcgPreCut.Sign() < 0 {
		notes = append(notes, "a capital LOSS in this country is excluded from column (b); apply the s.70/71 set-off rules in Schedule CG and adjust")
	}
	if opts.TIN == "" {
		notes = append(notes, "taxpayer identification number missing — pass --tin (use the passport number if the source country allotted none)")
	}
	row.NeedsReview = len(notes) > 0
	row.ReviewNote = strings.Join(notes, "; ")
	return row
}

func (r *Report) buildTR() {
	for _, c := range r.Countries {
		if c.Total.TaxPaidOutside.Sign() == 0 && c.Total.Relief.Sign() == 0 {
			continue
		}
		r.TR = append(r.TR, TRRow{
			CountryName: c.CountryName, CountryCode: c.CountryCode, TIN: c.TIN,
			TaxPaidOutside: c.Total.TaxPaidOutside, ReliefAvailable: c.Total.Relief,
			Section: "90",
		})
		r.TRTaxPaid.Add(r.TRTaxPaid, c.Total.TaxPaidOutside)
		r.TRRelief.Add(r.TRRelief, c.Total.Relief)
		r.TRDTAA.Add(r.TRDTAA, c.Total.Relief)
	}
}

func (r *Report) buildForm67(buckets map[string]*bucket, order []string) {
	byKey := map[string]*Form67Line{}
	var keys []string
	for _, l := range r.OtherSource {
		if l.TaxPaid.Sign() == 0 {
			continue // no foreign tax means no credit to claim, so nothing to report
		}
		k := l.CountryCode + "|" + l.Type
		f, ok := byKey[k]
		if !ok {
			article := ""
			switch l.Type {
			case "Dividend":
				article = ArticleDividends
			case "Interest":
				article = ArticleInterest
			}
			f = &Form67Line{
				CountryName: l.CountryName, CountryCode: l.CountryCode,
				IncomeType: l.Type, Income: new(big.Rat), TaxPaid: new(big.Rat),
				Article:  article,
				RateNote: "income at the Rule 115 specified date; tax at the Rule 128(8) date",
			}
			byKey[k] = f
			keys = append(keys, k)
		}
		f.Income.Add(f.Income, l.Income)
		f.TaxPaid.Add(f.TaxPaid, l.TaxPaid)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.Form67 = append(r.Form67, *byKey[k])
	}
	defer func() {
		sort.SliceStable(r.Form67, func(i, j int) bool {
			if r.Form67[i].CountryCode != r.Form67[j].CountryCode {
				return r.Form67[i].CountryCode < r.Form67[j].CountryCode
			}
			return r.Form67[i].IncomeType < r.Form67[j].IncomeType
		})
	}()
	for _, key := range order {
		b := buckets[key]
		inc := new(big.Rat)
		for _, v := range []*big.Rat{b.stcg, b.ltcg, b.ltcgPreCut} {
			if v.Sign() > 0 {
				inc.Add(inc, v)
			}
		}
		if b.cgTaxPaid.Sign() == 0 {
			continue
		}
		r.Form67 = append(r.Form67, Form67Line{
			CountryName: b.name, CountryCode: b.code, IncomeType: "Capital gains",
			Income: inc, TaxPaid: b.cgTaxPaid, Article: articleIf(b.cgTaxPaid, ArticleCapitalGain),
			RateNote: "gain computed under Rule 115 (" + "see the capital-gains detail" + ")",
		})
	}
}

func assumptions(opts Options) []string {
	return []string{
		fmt.Sprintf("Slab (marginal) rate applied to short-term gains and Other Sources: %s%%", ratStr(opts.MarginalRate)),
		fmt.Sprintf("Surcharge %s%% (capped at %s%% on s.112 long-term gains) and cess %s%% are included in column (d)",
			ratStr(opts.Surcharge), ratStr(SurchargeCap), ratStr(opts.Cess)),
		fmt.Sprintf("Long-term gains on foreign shares: holding period > %d months, taxed at %s%% without indexation (s.112, transfers on/after 23 Jul 2024)",
			gains.LongTermMonths, ratStr(LTCGRate)),
		fmt.Sprintf("Capital-gains FX method: %s", opts.CGMethod),
		"Income converted under Rule 115 (TTBR of the last day of the preceding month; 31 March for other income), foreign tax under Rule 128(8) — NOT the Schedule FA event-date convention",
		"Relief is the lower of the foreign tax and the Indian tax on the same income; excess foreign tax cannot be carried forward",
	}
}

func globalNotes(r *Report) []string {
	notes := []string{
		"Column (b) must equal the income actually offered in Schedule CG / Schedule OS — check it against the tie-out section before filing",
		"Schedule FA covers the CALENDAR year and converts at event-date rates; this schedule covers the FINANCIAL year at Rule 115 rates. The two are not meant to agree",
	}
	if r.TRRelief.Sign() > 0 {
		notes = append(notes, fmt.Sprintf(
			"Form 67 must be filed to claim this relief — by the end of AY %s under the amended Rule 128(9), but file it with the return", r.AYLabel()))
	}
	return notes
}

// --- arithmetic helpers ---

// taxOn returns rate% of a positive base, grossed up by surcharge and cess.
func taxOn(base, ratePct, surchargePct, cessPct *big.Rat) *big.Rat {
	if base == nil || base.Sign() <= 0 {
		return new(big.Rat)
	}
	tax := pct(base, ratePct)
	sur := pct(tax, surchargePct)
	cess := pct(new(big.Rat).Add(tax, sur), cessPct)
	return new(big.Rat).Add(new(big.Rat).Add(tax, sur), cess)
}

func pct(base, rate *big.Rat) *big.Rat {
	if base == nil || rate == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Quo(new(big.Rat).Mul(base, rate), big.NewRat(100, 1))
}

// capped applies the 15% surcharge ceiling that s.112 income enjoys.
func capped(surcharge *big.Rat) *big.Rat {
	if surcharge == nil {
		return new(big.Rat)
	}
	if surcharge.Cmp(SurchargeCap) > 0 {
		return new(big.Rat).Set(SurchargeCap)
	}
	return surcharge
}

func lower(a, b *big.Rat) *big.Rat {
	if a == nil || b == nil {
		return new(big.Rat)
	}
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

func articleIf(tax *big.Rat, article string) string {
	if tax != nil && tax.Sign() > 0 {
		return article
	}
	return ""
}

// countryOf resolves the SOURCE country of an income: the issuer's country, not
// the listing venue. A US-listed Irish UCITS ETF is Irish-source.
func countryOf(in model.Instrument, ents *entities.Store) (name, code string) {
	name, code = itr.Country(in.ListingCtry)
	if ents == nil {
		return name, code
	}
	if e, ok := ents.Lookup(in.ISIN, in.Symbol); ok && strings.TrimSpace(e.CountryCode) != "" {
		code = e.CountryCode
		if n := itr.NameForCode(code); n != "" {
			name = n
		}
	}
	return name, code
}

func ratOf(m model.Money) *big.Rat {
	if m.Amount == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(m.Amount)
}

func ratStr(r *big.Rat) string {
	if r == nil {
		return "0"
	}
	s := r.FloatString(2)
	return strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
}

// dedupe drops repeated notes while preserving order. Several transactions in
// the same security raise the same warning, and a review note that repeats
// itself once per transaction stops being read.
func dedupe(notes []string) []string {
	seen := make(map[string]bool, len(notes))
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

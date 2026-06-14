// Package schedulefa assembles the final Schedule FA rows (Table A2 + A3) from a
// parsed statement, the FX store, and computed peaks. Every INR figure carries
// the per-event fx.Conversion records behind it so the report can show its work.
//
// M3 builds Table A3 (one row per security held at any time in the calendar
// year). Table A2 (the custodial account) and richer entity metadata are M5.
//
// Conversion-date conventions (documented assumptions):
//   - Initial value : TTBR on each lot's acquisition date (or each buy's date).
//   - Closing value : TTBR on 31-Dec.
//   - Dividend      : TTBR on the pay/credit date of each distribution.
//   - Sale proceeds : TTBR on each sell's trade date.
//   - Peak value    : provided by the peak engine (max INR over priced points).
package schedulefa

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/entities"
	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
	"github.com/akagr/tax-tools/schedule-fa/internal/peak"
)

// Amount is an INR total plus the per-event conversions that sum to it.
type Amount struct {
	INR   model.Money
	Audit []fx.Conversion
}

// A3Row is one security held at any time during the calendar year.
type A3Row struct {
	CountryName   string
	CountryCode   string
	EntityName    string
	Address       string
	ZIP           string
	NatureEntity  string
	AcquiredOn    string
	InitialValue  Amount
	PeakValue     Amount
	PeakApprox    bool
	ClosingValue  Amount
	GrossDividend Amount
	SaleProceeds  Amount
	NeedsReview   bool
	ReviewNote    string
}

// A2Row is the custodial-account row (M5).
type A2Row struct {
	Institution    string
	Address        string
	ZIP            string
	CountryCode    string
	AccountNumber  string
	Status         string
	OpenDate       string
	PeakBalance    Amount
	ClosingBalance Amount
	GrossCredited  Amount
	NeedsReview    bool
	ReviewNote     string
}

// Report is the complete Schedule FA output for one calendar year.
type Report struct {
	Year int
	A2   []A2Row
	A3   []A3Row
}

type valuedEvent struct {
	Money model.Money
	Date  time.Time
}

// Options carries optional inputs to Build.
type Options struct {
	Entities *entities.Store // entity metadata override (nil ok)
	A2Peak   *fx.Conversion  // exact portfolio peak (mode B); nil => upper-bound sum
}

// Build assembles Tables A2 and A3 from the statement, FX store, computed peaks,
// and options.
func Build(s *model.Statement, store fx.Store, peaks []peak.Result, opts Options) (*Report, error) {
	rep := &Report{Year: s.Year}
	ents := opts.Entities
	yearEnd := time.Date(s.Year, time.December, 31, 0, 0, 0, 0, time.UTC)

	peakByKey := map[string]peak.Result{}
	for _, p := range peaks {
		peakByKey[instKey(p.Instrument)] = p
	}
	corpActions := map[string][]model.CorporateAction{}
	for _, ca := range s.CorporateActions {
		k := instKey(ca.Instrument)
		corpActions[k] = append(corpActions[k], ca)
	}

	// Every instrument held at any time during the year: positions ∪ trades.
	insts := map[string]model.Instrument{}
	var order []string
	add := func(in model.Instrument) {
		k := instKey(in)
		if _, ok := insts[k]; !ok {
			insts[k] = in
			order = append(order, k)
		}
	}
	for _, p := range s.OpenPositions {
		add(p.Instrument)
	}
	for _, t := range s.Trades {
		add(t.Instrument)
	}
	sort.Strings(order)

	for _, k := range order {
		inst := insts[k]
		row := A3Row{
			EntityName:   firstNonEmpty(inst.Name, inst.Symbol),
			NatureEntity: natureOf(inst.AssetClass),
			PeakApprox:   true,
		}
		row.CountryName, row.CountryCode = countryFor(inst.ListingCtry)

		// User-supplied entity metadata overrides the IBKR-derived defaults.
		if e, ok := ents.Lookup(inst.ISIN, inst.Symbol); ok {
			row.EntityName = firstNonEmpty(e.Name, row.EntityName)
			row.Address = firstNonEmpty(e.Address, row.Address)
			row.ZIP = firstNonEmpty(e.ZIP, row.ZIP)
			row.CountryCode = firstNonEmpty(e.CountryCode, row.CountryCode)
			row.NatureEntity = firstNonEmpty(e.Nature, row.NatureEntity)
		}

		// Initial value + acquisition date (vesting date for RSUs).
		var initEvents []valuedEvent
		var acq time.Time
		if lots := lotsFor(s, k); len(lots) > 0 {
			for _, l := range lots {
				initEvents = append(initEvents, valuedEvent{l.CostBasis, l.AcquiredOn()})
				acq = earliest(acq, l.AcquiredOn())
			}
		} else {
			for _, t := range tradesFor(s, k) {
				if t.Side == model.Buy {
					cost := new(big.Rat).Mul(t.Quantity, ratOf(t.Price))
					initEvents = append(initEvents, valuedEvent{model.NewMoney(t.Price.Currency, cost), t.Date})
					acq = earliest(acq, t.Date)
				}
			}
		}
		row.AcquiredOn = fmtDate(acq)
		initAmt, initErrs := convertEvents(store, initEvents)
		row.InitialValue = initAmt

		// Peak (from the peak engine).
		if p, ok := peakByKey[k]; ok && p.HasValue {
			row.PeakValue = Amount{INR: p.Peak.Result, Audit: []fx.Conversion{p.Peak}}
		}

		// Closing value (year-end position × mark price; 0 if exited).
		var closeEvents []valuedEvent
		if pos, mark, ok := closingFor(s, k); ok {
			val := new(big.Rat).Mul(pos, ratOf(mark))
			if val.Sign() > 0 {
				closeEvents = append(closeEvents, valuedEvent{model.NewMoney(mark.Currency, val), yearEnd})
			}
		}
		closeAmt, closeErrs := convertEvents(store, closeEvents)
		row.ClosingValue = closeAmt

		// Gross dividends (at pay date).
		var divEvents []valuedEvent
		for _, d := range dividendsFor(s, k) {
			divEvents = append(divEvents, valuedEvent{d.Gross, d.PayDate})
		}
		divAmt, divErrs := convertEvents(store, divEvents)
		row.GrossDividend = divAmt

		// Sale proceeds (at trade date).
		var procEvents []valuedEvent
		for _, t := range tradesFor(s, k) {
			if t.Side == model.Sell {
				procEvents = append(procEvents, valuedEvent{t.Proceeds, t.Date})
			}
		}
		procAmt, procErrs := convertEvents(store, procEvents)
		row.SaleProceeds = procAmt

		// Review flags — only real data gaps trip NeedsReview. The mode-C peak
		// caveat is conveyed once, in the report header, not per row.
		var notes []string
		if row.CountryCode == "" {
			notes = append(notes, "country code unknown (set via --entities)")
		}
		if row.Address == "" || row.ZIP == "" {
			notes = append(notes, "entity address/ZIP missing (set via --entities)")
		}
		if cas := corpActions[k]; len(cas) > 0 {
			notes = append(notes, fmt.Sprintf("%d corporate action(s) in year — verify quantity/cost basis", len(cas)))
		}
		for _, e := range concatErrs(initErrs, closeErrs, divErrs, procErrs) {
			notes = append(notes, e.Error())
		}
		row.NeedsReview = len(notes) > 0
		row.ReviewNote = strings.Join(notes, "; ")

		rep.A3 = append(rep.A3, row)
	}

	rep.A2 = []A2Row{buildA2(s, rep.A3, opts.A2Peak)}
	return rep, nil
}

// buildA2 assembles the custodial-account row by aggregating the A3 figures. The
// account peak is the exact daily-NAV maximum when supplied (mode B); otherwise
// it is an upper bound (sum of per-security peaks, which are not simultaneous).
// Cash balances are not included either way.
func buildA2(s *model.Statement, a3 []A3Row, exactPeak *fx.Conversion) A2Row {
	acc := s.Account
	var closes, peaks, credited []Amount
	for _, r := range a3 {
		closes = append(closes, r.ClosingValue)
		peaks = append(peaks, r.PeakValue)
		credited = append(credited, r.GrossDividend)
	}
	addr, zip, _, code := institutionMeta(acc.IBEntity)
	row := A2Row{
		Institution:    acc.Institution,
		Address:        addr,
		ZIP:            zip,
		CountryCode:    code,
		AccountNumber:  acc.Number,
		Status:         "Owner",
		OpenDate:       fmtDate(acc.OpenDate),
		ClosingBalance: sumAmounts(closes),
		GrossCredited:  sumAmounts(credited),
	}
	var notes []string
	if exactPeak != nil {
		row.PeakBalance = Amount{INR: exactPeak.Result, Audit: []fx.Conversion{*exactPeak}}
		notes = append(notes, "peak balance is the exact daily-NAV maximum on "+fmtDate(exactPeak.RateDate)+"; cash not included")
	} else {
		row.PeakBalance = sumAmounts(peaks)
		notes = append(notes, "peak balance is an upper bound (per-security peaks summed, not simultaneous); cash not included")
	}
	if code == "" {
		notes = append(notes, "institution country code unknown")
	}
	row.NeedsReview = true
	row.ReviewNote = strings.Join(notes, "; ")
	return row
}

func sumAmounts(amts []Amount) Amount {
	sum := new(big.Rat)
	var audit []fx.Conversion
	for _, a := range amts {
		sum.Add(sum, ratOf(a.INR))
		audit = append(audit, a.Audit...)
	}
	return Amount{INR: model.NewMoney(model.INR, sum), Audit: audit}
}

// institutionMeta returns the broker entity's address, ZIP, country name, and
// ITR country code for Table A2.
func institutionMeta(ibEntity string) (address, zip, countryName, itrCode string) {
	switch strings.ToUpper(strings.TrimSpace(ibEntity)) {
	case "", "IBLLC-US", "IBLLC":
		return "One Pickwick Plaza, Greenwich, CT", "06830", "United States of America", "1"
	default:
		return "", "", "", ""
	}
}

// convertEvents converts each event to INR and sums them, collecting the audit
// trail and any per-event FX errors (which become manual-review notes).
func convertEvents(store fx.Store, evs []valuedEvent) (Amount, []error) {
	sum := new(big.Rat)
	var audit []fx.Conversion
	var errs []error
	for _, e := range evs {
		conv, err := fx.Convert(store, e.Money, e.Date)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sum.Add(sum, ratOf(conv.Result))
		audit = append(audit, conv)
	}
	return Amount{INR: model.NewMoney(model.INR, sum), Audit: audit}, errs
}

// --- lookups over the statement ---

func lotsFor(s *model.Statement, key string) []model.Lot {
	var out []model.Lot
	for _, l := range s.Lots {
		if instKey(l.Instrument) == key {
			out = append(out, l)
		}
	}
	return out
}

func tradesFor(s *model.Statement, key string) []model.Trade {
	var out []model.Trade
	for _, t := range s.Trades {
		if instKey(t.Instrument) == key {
			out = append(out, t)
		}
	}
	return out
}

func dividendsFor(s *model.Statement, key string) []model.Dividend {
	var out []model.Dividend
	for _, d := range s.Dividends {
		if instKey(d.Instrument) == key {
			out = append(out, d)
		}
	}
	return out
}

func closingFor(s *model.Statement, key string) (pos *big.Rat, mark model.Money, ok bool) {
	for _, p := range s.OpenPositions {
		if instKey(p.Instrument) == key {
			return p.Quantity, p.MarkPrice, true
		}
	}
	return nil, model.Money{}, false
}

// --- small helpers ---

func instKey(in model.Instrument) string {
	if in.ISIN != "" {
		return "isin:" + in.ISIN
	}
	return "sym:" + in.Symbol
}

func ratOf(m model.Money) *big.Rat {
	if m.Amount == nil {
		return new(big.Rat)
	}
	return m.Amount
}

func earliest(cur, d time.Time) time.Time {
	if d.IsZero() {
		return cur
	}
	if cur.IsZero() || d.Before(cur) {
		return d
	}
	return cur
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func concatErrs(groups ...[]error) []error {
	var out []error
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// isdCountries maps an ISO-3166 alpha-2 (or common 3-letter) code to (display
// name, ISD telephone code). Schedule FA uses the country's ISD code as its
// "country code". Only common ones are mapped; the rest are flagged for manual
// entry (set via --entities).
var isdCountries = map[string][2]string{
	"US":  {"United States of America", "1"},
	"USA": {"United States of America", "1"},
	"IE":  {"Ireland", "353"},
	"GB":  {"United Kingdom", "44"},
	"UK":  {"United Kingdom", "44"},
	"CA":  {"Canada", "1"},
	"AU":  {"Australia", "61"},
	"DE":  {"Germany", "49"},
	"NL":  {"Netherlands", "31"},
	"FR":  {"France", "33"},
	"CH":  {"Switzerland", "41"},
	"SG":  {"Singapore", "65"},
	"JP":  {"Japan", "81"},
	"HK":  {"Hong Kong", "852"},
	"LU":  {"Luxembourg", "352"},
}

func countryFor(iso string) (name, code string) {
	if iso == "" {
		return "", ""
	}
	if v, ok := isdCountries[strings.ToUpper(iso)]; ok {
		return v[0], v[1]
	}
	return iso, "" // ISD code must be filled manually
}

func natureOf(assetClass string) string {
	switch strings.ToUpper(assetClass) {
	case "STK":
		return "Listed equity share"
	case "ETF":
		return "Exchange Traded Fund"
	case "BOND":
		return "Debt instrument"
	case "":
		return ""
	default:
		return assetClass
	}
}

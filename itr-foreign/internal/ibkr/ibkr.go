// Package ibkr ingests Interactive Brokers data.
//
// v1 parses a downloaded Activity Flex Query in XML form (offline mode, M1).
// The Flex Web Service online pull (SendRequest/GetStatement) lands in M6.
//
// The parser is deliberately tolerant: encoding/xml ignores unknown elements and
// attributes, dates are parsed across the formats Flex can emit, and records are
// constrained to the requested reporting period (a calendar year for Schedule
// FA, an Apr–Mar financial year for the income schedules). Sections consumed:
// AccountInformation, OpenPositions (with optional Lot detail), Trades
// (executions plus CLOSED_LOT / nested <Lot> realized-gain detail),
// CashTransactions (dividends, payments in lieu, interest and withholding),
// CorporateActions, SecuritiesInfo.
package ibkr

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/akagr/finance-tools/itr-foreign/internal/model"
)

// ParseFlexFile parses an IBKR Activity Flex XML file, constrained to `year`.
func ParseFlexFile(path string, year int) (*model.Statement, error) {
	return ParseFlexFilePeriod(path, CalendarYear(year))
}

// ParseFlexFilePeriod parses an IBKR Activity Flex XML file, constrained to an
// arbitrary reporting period (e.g. an Apr–Mar financial year).
func ParseFlexFilePeriod(path string, p Period) (*model.Statement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseFlexPeriod(f, p)
}

// Period is an inclusive reporting window. Schedule FA uses a calendar year;
// the income schedules (FSI/TR/CG/OS) use the Apr–Mar financial year.
type Period struct {
	From time.Time
	To   time.Time
}

// CalendarYear returns the Jan 1 – Dec 31 period for a calendar year.
func CalendarYear(year int) Period {
	return Period{
		From: time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(year, time.December, 31, 23, 59, 59, 0, time.UTC),
	}
}

// FinancialYear returns the Apr 1 – Mar 31 period for the FY starting in
// `startYear` (e.g. 2025 for FY 2025-26, i.e. AY 2026-27).
func FinancialYear(startYear int) Period {
	return Period{
		From: time.Date(startYear, time.April, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(startYear+1, time.March, 31, 23, 59, 59, 0, time.UTC),
	}
}

// calendarYear reports the calendar year the period covers exactly, or 0.
func (p Period) calendarYear() int {
	if p.From.Month() == time.January && p.From.Day() == 1 &&
		p.To.Month() == time.December && p.To.Day() == 31 &&
		p.From.Year() == p.To.Year() {
		return p.From.Year()
	}
	return 0
}

// ParseFlexXML reads an IBKR Activity Flex Query (XML output) and returns the
// statement with all dated records constrained to `year` (1 Jan – 31 Dec).
// Open positions are the year-end snapshot and are kept as-is.
func ParseFlexXML(r io.Reader, year int) (*model.Statement, error) {
	return ParseFlexPeriod(r, CalendarYear(year))
}

// ParseFlexPeriod reads an IBKR Activity Flex Query (XML output) and returns the
// statement with all dated records constrained to the period. Open positions are
// the end-of-period snapshot and are kept as-is.
func ParseFlexPeriod(r io.Reader, period Period) (*model.Statement, error) {
	var resp flexResponse
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("ibkr: decode flex xml: %w", err)
	}
	if len(resp.Statements.Statement) == 0 {
		return nil, fmt.Errorf("ibkr: no FlexStatement found (check the query has sections enabled)")
	}

	yearStart, yearEnd := period.From, period.To
	inYear := func(t time.Time) bool {
		return !t.IsZero() && !t.Before(yearStart) && !t.After(yearEnd)
	}

	out := &model.Statement{Year: period.calendarYear(), From: yearStart, To: yearEnd}
	instruments := map[string]model.Instrument{} // keyed by isin|symbol

	for _, st := range resp.Statements.Statement {
		// Account (first non-empty wins; statements are usually one).
		if out.Account.Number == "" {
			ai := st.AccountInfo
			out.Account = model.Account{
				Number:       firstNonEmpty(ai.AccountID, st.AccountID),
				Name:         ai.Name,
				BaseCurrency: model.Currency(ai.Currency),
				OpenDate:     mustDate(ai.DateOpened),
				Institution:  institutionFor(ai.IBEntity),
				IBEntity:     ai.IBEntity,
				Street:       ai.Street,
				City:         ai.City,
				State:        ai.State,
				PostalCode:   ai.PostalCode,
				Country:      ai.Country,
			}
		}

		// Securities metadata first, so positions/trades can enrich from it.
		for _, s := range st.SecuritiesInfo.Securities {
			inst := model.Instrument{
				Symbol:      s.Symbol,
				ISIN:        s.ISIN,
				Name:        s.Description,
				AssetClass:  s.AssetCategory,
				ListingCtry: s.IssuerCountryCode,
				Currency:    model.Currency(s.Currency),
			}
			instruments[instKey(s.ISIN, s.Symbol)] = inst
		}
		mergeInst := func(in model.Instrument) model.Instrument {
			key := instKey(in.ISIN, in.Symbol)
			if existing, ok := instruments[key]; ok {
				in = enrich(existing, in)
			}
			instruments[key] = in
			return in
		}

		// Open positions. IBKR may emit several rows per instrument: a SUMMARY
		// row plus one LOT row per tax lot (or several LOT rows with no summary).
		// We aggregate them into a single year-end Position and one Lot per LOT
		// row, so positions are never double-counted nor under-counted.
		type posGroup struct {
			inst model.Instrument
			rows []flexOpenPosition
		}
		posByKey := map[string]*posGroup{}
		var posOrder []string
		for _, p := range st.OpenPositions.Positions {
			inst := mergeInst(model.Instrument{
				Symbol: p.Symbol, ISIN: p.ISIN, Name: p.Description,
				AssetClass: p.AssetCategory, ListingCtry: p.IssuerCountryCode,
				Currency: model.Currency(p.Currency),
			})
			k := instKey(p.ISIN, p.Symbol)
			g := posByKey[k]
			if g == nil {
				g = &posGroup{inst: inst}
				posByKey[k] = g
				posOrder = append(posOrder, k)
			}
			g.rows = append(g.rows, p)
		}
		for _, k := range posOrder {
			g := posByKey[k]
			var summaries, lotRows []flexOpenPosition
			for _, r := range g.rows {
				if strings.EqualFold(r.LevelOfDetail, "SUMMARY") {
					summaries = append(summaries, r)
				} else {
					lotRows = append(lotRows, r)
				}
			}
			// Position quantity: prefer LOT rows (summed); fall back to SUMMARY.
			posBase := lotRows
			if len(posBase) == 0 {
				posBase = summaries
			}
			qty := new(big.Rat)
			var mark model.Money
			for _, r := range posBase {
				qty.Add(qty, parseRat(r.Position))
				if strings.TrimSpace(r.MarkPrice) != "" {
					mark = money(r.Currency, r.MarkPrice)
				}
			}
			out.OpenPositions = append(out.OpenPositions, model.Position{
				Instrument: g.inst,
				Date:       yearEnd,
				Quantity:   qty,
				MarkPrice:  mark,
			})
			// Lots: one per LOT row (or nested <Lot>); fall back to SUMMARY rows.
			lotBase := lotRows
			if len(lotBase) == 0 {
				lotBase = summaries
			}
			for _, r := range lotBase {
				if len(r.Lots) > 0 {
					for _, l := range r.Lots {
						out.Lots = append(out.Lots, model.Lot{
							Instrument: g.inst,
							OpenDate:   mustDate(firstNonEmpty(l.HoldingPeriodDateTime, l.OpenDateTime)),
							VestDate:   mustDate(l.VestingDate),
							Quantity:   parseRat(l.Position),
							CostBasis:  money(r.Currency, l.CostBasisMoney),
						})
					}
					continue
				}
				out.Lots = append(out.Lots, model.Lot{
					Instrument: g.inst,
					OpenDate:   mustDate(firstNonEmpty(r.HoldingPeriodDateTime, r.OpenDateTime)),
					VestDate:   mustDate(r.VestingDate),
					Quantity:   parseRat(r.Position),
					CostBasis:  money(r.Currency, r.CostBasisMoney),
				})
			}
		}

		// Trades within the year.
		for _, t := range st.Trades.Trades {
			d := mustDate(firstNonEmpty(t.TradeDate, t.DateTime))
			if !inYear(d) {
				continue
			}
			inst := mergeInst(model.Instrument{
				Symbol: t.Symbol, ISIN: t.ISIN, Name: t.Description,
				AssetClass: t.AssetCategory, ListingCtry: t.IssuerCountryCode,
				Currency: model.Currency(t.Currency),
			})
			// A CLOSED_LOT row is IBKR's per-lot breakdown of a closing trade,
			// not a separate execution — counting it as a trade would double the
			// proceeds. It is the capital-gains input instead.
			if strings.EqualFold(t.LevelOfDetail, "CLOSED_LOT") {
				out.RealizedLots = append(out.RealizedLots, closedLot(inst, d, t.Currency,
					t.Quantity, t.Proceeds, firstNonEmpty(t.Cost, t.CostBasis, t.CostBasisMoney),
					t.IBCommission, t.FifoPnlRealized,
					firstNonEmpty(t.HoldingPeriodDateTime, t.OpenDateTime)))
				continue
			}
			out.Trades = append(out.Trades, model.Trade{
				Instrument: inst,
				Date:       d,
				Side:       side(t.BuySell),
				Quantity:   absRat(parseRat(t.Quantity)),
				Price:      money(t.Currency, t.TradePrice),
				Proceeds:   money(t.Currency, t.Proceeds), // IBKR-signed: negative for buys
				Commission: money(t.Currency, t.IBCommission),
			})
			// Flex can instead nest the closed-lot breakdown inside the trade.
			for _, l := range t.Lots {
				out.RealizedLots = append(out.RealizedLots, closedLot(inst, d, t.Currency,
					l.Quantity, l.Proceeds, firstNonEmpty(l.Cost, l.CostBasis, l.CostBasisMoney),
					l.IBCommission, l.FifoPnlRealized,
					firstNonEmpty(l.HoldingPeriodDateTime, l.OpenDateTime)))
			}
		}

		// Cash transactions → dividends, payments in lieu, interest and
		// withholding, within the year. Withholding rows are matched to a
		// same-instrument, same-day dividend; anything left over is kept as an
		// unattributed withholding rather than dropped — it is still a
		// creditable foreign tax, and discarding it forfeits the credit.
		var pending []model.Dividend
		var pendingWH []model.Withholding
		withheld := map[string]*big.Rat{}
		for _, c := range st.CashTransactions.Txns {
			d := mustDate(firstNonEmpty(c.DateTime, c.SettleDate))
			if !inYear(d) {
				continue
			}
			inst := mergeInst(model.Instrument{
				Symbol: c.Symbol, ISIN: c.ISIN, Name: c.Description,
				Currency: model.Currency(c.Currency),
			})
			switch classify(c.Type) {
			case txnDividend, txnPaymentInLieu:
				kind := model.DividendCash
				if classify(c.Type) == txnPaymentInLieu {
					kind = model.DividendInLieu
				}
				pending = append(pending, model.Dividend{
					Instrument:  inst,
					PayDate:     d,
					Gross:       money(c.Currency, c.Amount),
					Withholding: model.NewMoney(model.Currency(c.Currency), nil),
					Kind:        kind,
				})
			case txnInterest:
				out.Interest = append(out.Interest, model.Interest{
					Instrument:  inst,
					Date:        d,
					Amount:      money(c.Currency, c.Amount), // signed: paid interest is negative
					Withholding: model.NewMoney(model.Currency(c.Currency), nil),
					Description: firstNonEmpty(c.Description, c.Type),
				})
			case txnWithholding:
				// IBKR posts withholding as a debit (negative). Negating rather
				// than taking the absolute value lets a later reversal/refund
				// row (positive) correctly net the tax down.
				k := instKey(c.ISIN, c.Symbol) + "|" + d.Format("20060102")
				withheld[k] = new(big.Rat).Add(orZero(withheld[k]), negRat(parseRat(c.Amount)))
				pendingWH = append(pendingWH, model.Withholding{
					Instrument:  inst,
					Date:        d,
					Amount:      model.NewMoney(model.Currency(c.Currency), negRat(parseRat(c.Amount))),
					Description: firstNonEmpty(c.Description, c.Type),
				})
			}
		}
		for i := range pending {
			k := instKey(pending[i].Instrument.ISIN, pending[i].Instrument.Symbol) + "|" + pending[i].PayDate.Format("20060102")
			if w, ok := withheld[k]; ok {
				pending[i].Withholding = model.NewMoney(pending[i].Gross.Currency, w)
				delete(withheld, k)
			}
		}
		for _, w := range pendingWH {
			k := instKey(w.Instrument.ISIN, w.Instrument.Symbol) + "|" + w.Date.Format("20060102")
			if _, unmatched := withheld[k]; unmatched {
				out.UnmatchedWithholding = append(out.UnmatchedWithholding, w)
			}
		}
		out.Dividends = append(out.Dividends, pending...)

		// Corporate actions within the year (flagged, not reprocessed).
		for _, ca := range st.CorporateActions.Actions {
			d := mustDate(firstNonEmpty(ca.DateTime, ca.ReportDate))
			if !inYear(d) {
				continue
			}
			inst := mergeInst(model.Instrument{
				Symbol: ca.Symbol, ISIN: ca.ISIN, Name: ca.Description,
				AssetClass: ca.AssetCategory, Currency: model.Currency(ca.Currency),
			})
			out.CorporateActions = append(out.CorporateActions, model.CorporateAction{
				Instrument:  inst,
				Date:        d,
				Type:        firstNonEmpty(ca.Type, ca.ActionDescription),
				Description: ca.Description,
			})
		}
	}

	return out, nil
}

// institutionFor maps an IBKR entity code to the legal institution name. The
// holder's own address comes from AccountInformation; this is the broker entity.
func institutionFor(ibEntity string) string {
	switch strings.ToUpper(strings.TrimSpace(ibEntity)) {
	case "", "IBLLC-US", "IBLLC":
		return "Interactive Brokers LLC"
	case "IBUK", "IBUK-L":
		return "Interactive Brokers (U.K.) Limited"
	case "IBIE":
		return "Interactive Brokers Ireland Limited"
	case "IBCE":
		return "Interactive Brokers Central Europe Zrt."
	case "IBKR-IN", "IBINDIA":
		return "Interactive Brokers (India) Pvt. Ltd."
	default:
		return "Interactive Brokers (" + ibEntity + ")"
	}
}

// --- XML schema (only the attributes we use; unknowns are ignored) ---

type flexResponse struct {
	XMLName    xml.Name `xml:"FlexQueryResponse"`
	Statements struct {
		Statement []flexStatement `xml:"FlexStatement"`
	} `xml:"FlexStatements"`
}

type flexStatement struct {
	AccountID   string          `xml:"accountId,attr"`
	FromDate    string          `xml:"fromDate,attr"`
	ToDate      string          `xml:"toDate,attr"`
	AccountInfo flexAccountInfo `xml:"AccountInformation"`

	OpenPositions struct {
		Positions []flexOpenPosition `xml:"OpenPosition"`
	} `xml:"OpenPositions"`
	Trades struct {
		Trades []flexTrade `xml:"Trade"`
	} `xml:"Trades"`
	CashTransactions struct {
		Txns []flexCashTxn `xml:"CashTransaction"`
	} `xml:"CashTransactions"`
	CorporateActions struct {
		Actions []flexCorpAction `xml:"CorporateAction"`
	} `xml:"CorporateActions"`
	SecuritiesInfo struct {
		Securities []flexSecurityInfo `xml:"SecurityInfo"`
	} `xml:"SecuritiesInfo"`
}

type flexAccountInfo struct {
	AccountID  string `xml:"accountId,attr"`
	Name       string `xml:"name,attr"`
	Currency   string `xml:"currency,attr"`
	DateOpened string `xml:"dateOpened,attr"`
	IBEntity   string `xml:"ibEntity,attr"`
	Street     string `xml:"street,attr"`
	City       string `xml:"city,attr"`
	State      string `xml:"state,attr"`
	Country    string `xml:"country,attr"`
	PostalCode string `xml:"postalCode,attr"`
}

type flexOpenPosition struct {
	AssetCategory         string    `xml:"assetCategory,attr"`
	Symbol                string    `xml:"symbol,attr"`
	Description           string    `xml:"description,attr"`
	ISIN                  string    `xml:"isin,attr"`
	Currency              string    `xml:"currency,attr"`
	Position              string    `xml:"position,attr"`
	MarkPrice             string    `xml:"markPrice,attr"`
	CostBasisPrice        string    `xml:"costBasisPrice,attr"`
	CostBasisMoney        string    `xml:"costBasisMoney,attr"`
	OpenDateTime          string    `xml:"openDateTime,attr"`
	HoldingPeriodDateTime string    `xml:"holdingPeriodDateTime,attr"`
	VestingDate           string    `xml:"vestingDate,attr"`
	IssuerCountryCode     string    `xml:"issuerCountryCode,attr"`
	LevelOfDetail         string    `xml:"levelOfDetail,attr"`
	Lots                  []flexLot `xml:"Lot"`
}

type flexLot struct {
	Position              string `xml:"position,attr"`
	CostBasisMoney        string `xml:"costBasisMoney,attr"`
	OpenDateTime          string `xml:"openDateTime,attr"`
	HoldingPeriodDateTime string `xml:"holdingPeriodDateTime,attr"`
	VestingDate           string `xml:"vestingDate,attr"`
}

type flexTrade struct {
	AssetCategory     string `xml:"assetCategory,attr"`
	Symbol            string `xml:"symbol,attr"`
	ISIN              string `xml:"isin,attr"`
	Description       string `xml:"description,attr"`
	Currency          string `xml:"currency,attr"`
	TradeDate         string `xml:"tradeDate,attr"`
	DateTime          string `xml:"dateTime,attr"`
	BuySell           string `xml:"buySell,attr"`
	Quantity          string `xml:"quantity,attr"`
	TradePrice        string `xml:"tradePrice,attr"`
	Proceeds          string `xml:"proceeds,attr"`
	IBCommission      string `xml:"ibCommission,attr"`
	IssuerCountryCode string `xml:"issuerCountryCode,attr"`
	// Closed-lot detail (levelOfDetail="CLOSED_LOT", or nested <Lot> rows).
	LevelOfDetail         string          `xml:"levelOfDetail,attr"`
	Cost                  string          `xml:"cost,attr"`
	CostBasis             string          `xml:"costBasis,attr"`
	CostBasisMoney        string          `xml:"costBasisMoney,attr"`
	FifoPnlRealized       string          `xml:"fifoPnlRealized,attr"`
	OpenDateTime          string          `xml:"openDateTime,attr"`
	HoldingPeriodDateTime string          `xml:"holdingPeriodDateTime,attr"`
	Lots                  []flexClosedLot `xml:"Lot"`
}

// flexClosedLot is the per-lot breakdown Flex nests inside a closing trade.
type flexClosedLot struct {
	Quantity              string `xml:"quantity,attr"`
	Proceeds              string `xml:"proceeds,attr"`
	Cost                  string `xml:"cost,attr"`
	CostBasis             string `xml:"costBasis,attr"`
	CostBasisMoney        string `xml:"costBasisMoney,attr"`
	IBCommission          string `xml:"ibCommission,attr"`
	FifoPnlRealized       string `xml:"fifoPnlRealized,attr"`
	OpenDateTime          string `xml:"openDateTime,attr"`
	HoldingPeriodDateTime string `xml:"holdingPeriodDateTime,attr"`
}

type flexCashTxn struct {
	Type        string `xml:"type,attr"`
	Symbol      string `xml:"symbol,attr"`
	ISIN        string `xml:"isin,attr"`
	Description string `xml:"description,attr"`
	Currency    string `xml:"currency,attr"`
	Amount      string `xml:"amount,attr"`
	DateTime    string `xml:"dateTime,attr"`
	SettleDate  string `xml:"settleDate,attr"`
}

type flexCorpAction struct {
	AssetCategory     string `xml:"assetCategory,attr"`
	Symbol            string `xml:"symbol,attr"`
	ISIN              string `xml:"isin,attr"`
	Currency          string `xml:"currency,attr"`
	Type              string `xml:"type,attr"`
	ActionDescription string `xml:"actionDescription,attr"`
	Description       string `xml:"description,attr"`
	DateTime          string `xml:"dateTime,attr"`
	ReportDate        string `xml:"reportDate,attr"`
}

type flexSecurityInfo struct {
	Symbol            string `xml:"symbol,attr"`
	ISIN              string `xml:"isin,attr"`
	Description       string `xml:"description,attr"`
	AssetCategory     string `xml:"assetCategory,attr"`
	Currency          string `xml:"currency,attr"`
	IssuerCountryCode string `xml:"issuerCountryCode,attr"`
}

// --- helpers ---

type txnKind int

const (
	txnOther txnKind = iota
	txnDividend
	txnPaymentInLieu
	txnWithholding
	txnInterest
)

func classify(t string) txnKind {
	s := strings.ToLower(t)
	switch {
	case strings.Contains(s, "withholding"):
		return txnWithholding
	case strings.Contains(s, "in lieu"):
		// A substitute payment on lent-out shares: a credit like a dividend, but
		// withheld at the US statutory 30% with no treaty rate.
		return txnPaymentInLieu
	case strings.Contains(s, "dividend"):
		return txnDividend
	case strings.Contains(s, "interest"):
		return txnInterest
	default:
		return txnOther
	}
}

// closedLot builds a RealizedLot from IBKR's own FIFO lot-closing detail.
// Quantity, cost and proceeds are stored as positive magnitudes; the reported
// fifoPnlRealized is retained so the gains engine can cross-check the
// arithmetic (and so short covers, whose signs invert, get flagged).
func closedLot(inst model.Instrument, closeDate time.Time, cur, qty, proceeds, cost, commission, pnl, openDate string) model.RealizedLot {
	return model.RealizedLot{
		Instrument:  inst,
		OpenDate:    mustDate(openDate),
		CloseDate:   closeDate,
		Quantity:    absRat(parseRat(qty)),
		Cost:        model.NewMoney(model.Currency(cur), absRat(parseRat(cost))),
		Proceeds:    model.NewMoney(model.Currency(cur), absRat(parseRat(proceeds))),
		Commission:  money(cur, commission),
		RealizedPnL: money(cur, pnl),
	}
}

func side(s string) model.Side {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "SELL") {
		return model.Sell
	}
	return model.Buy
}

func instKey(isin, symbol string) string {
	if isin != "" {
		return "isin:" + isin
	}
	return "sym:" + symbol
}

// enrich fills empty fields of `in` from `base` (existing securities metadata).
func enrich(base, in model.Instrument) model.Instrument {
	if in.Name == "" {
		in.Name = base.Name
	}
	if in.AssetClass == "" {
		in.AssetClass = base.AssetClass
	}
	if in.ListingCtry == "" {
		in.ListingCtry = base.ListingCtry
	}
	if in.Currency == "" {
		in.Currency = base.Currency
	}
	if in.ISIN == "" {
		in.ISIN = base.ISIN
	}
	return in
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func money(cur, amount string) model.Money {
	return model.NewMoney(model.Currency(cur), parseRat(amount))
}

// parseRat parses an IBKR decimal string to a *big.Rat; empty/garbage -> zero.
func parseRat(s string) *big.Rat {
	s = strings.TrimSpace(s)
	if s == "" {
		return new(big.Rat)
	}
	if r, ok := new(big.Rat).SetString(s); ok {
		return r
	}
	return new(big.Rat)
}

func absRat(r *big.Rat) *big.Rat {
	return new(big.Rat).Abs(r)
}

func negRat(r *big.Rat) *big.Rat {
	return new(big.Rat).Neg(r)
}

func orZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// mustDate parses a Flex date, returning the zero time on failure (callers treat
// zero as "unknown / out of range").
func mustDate(s string) time.Time {
	t, _ := parseFlexDate(s)
	return t
}

// parseFlexDate handles the date formats Flex can emit: "20060102",
// "2006-01-02", optionally followed by ";HHMMSS", " HH:MM:SS", or "EDT" suffix.
func parseFlexDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if i := strings.IndexAny(s, "; "); i >= 0 {
		s = s[:i]
	}
	for _, layout := range []string{"20060102", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("ibkr: unrecognized date %q", s)
}

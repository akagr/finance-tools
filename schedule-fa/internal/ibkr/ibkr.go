// Package ibkr ingests Interactive Brokers data.
//
// v1 parses a downloaded Activity Flex Query in XML form (offline mode, M1).
// The Flex Web Service online pull (SendRequest/GetStatement) lands in M6.
//
// The parser is deliberately tolerant: encoding/xml ignores unknown elements and
// attributes, dates are parsed across the formats Flex can emit, and records are
// constrained to the requested calendar year. Sections consumed:
// AccountInformation, OpenPositions (with optional Lot detail), Trades,
// CashTransactions (dividends + withholding), SecuritiesInfo.
package ibkr

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

// ParseFlexFile parses an IBKR Activity Flex XML file, constrained to `year`.
func ParseFlexFile(path string, year int) (*model.Statement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseFlexXML(f, year)
}

// ParseFlexXML reads an IBKR Activity Flex Query (XML output) and returns the
// statement with all dated records constrained to `year` (1 Jan – 31 Dec).
// Open positions are the year-end snapshot and are kept as-is.
func ParseFlexXML(r io.Reader, year int) (*model.Statement, error) {
	var resp flexResponse
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("ibkr: decode flex xml: %w", err)
	}
	if len(resp.Statements.Statement) == 0 {
		return nil, fmt.Errorf("ibkr: no FlexStatement found (check the query has sections enabled)")
	}

	yearStart := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(year, time.December, 31, 23, 59, 59, 0, time.UTC)
	inYear := func(t time.Time) bool {
		return !t.IsZero() && !t.Before(yearStart) && !t.After(yearEnd)
	}

	out := &model.Statement{Year: year}
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

		// Open positions → year-end snapshot + first-cut lots.
		for _, p := range st.OpenPositions.Positions {
			inst := mergeInst(model.Instrument{
				Symbol: p.Symbol, ISIN: p.ISIN, Name: p.Description,
				AssetClass: p.AssetCategory, ListingCtry: p.IssuerCountryCode,
				Currency: model.Currency(p.Currency),
			})
			out.OpenPositions = append(out.OpenPositions, model.Position{
				Instrument: inst,
				Date:       yearEnd,
				Quantity:   parseRat(p.Position),
				MarkPrice:  money(p.Currency, p.MarkPrice),
			})
			// Lots: nested <Lot> if lot detail was enabled, else the aggregate.
			if len(p.Lots) > 0 {
				for _, l := range p.Lots {
					out.Lots = append(out.Lots, model.Lot{
						Instrument: inst,
						OpenDate:   mustDate(firstNonEmpty(l.OpenDateTime, l.HoldingPeriodDateTime)),
						VestDate:   mustDate(l.VestingDate),
						Quantity:   parseRat(l.Position),
						CostBasis:  money(p.Currency, l.CostBasisMoney),
					})
				}
			} else {
				out.Lots = append(out.Lots, model.Lot{
					Instrument: inst,
					OpenDate:   mustDate(firstNonEmpty(p.OpenDateTime, p.HoldingPeriodDateTime)),
					VestDate:   mustDate(p.VestingDate),
					Quantity:   parseRat(p.Position),
					CostBasis:  money(p.Currency, p.CostBasisMoney),
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
			out.Trades = append(out.Trades, model.Trade{
				Instrument: inst,
				Date:       d,
				Side:       side(t.BuySell),
				Quantity:   absRat(parseRat(t.Quantity)),
				Price:      money(t.Currency, t.TradePrice),
				Proceeds:   money(t.Currency, t.Proceeds), // IBKR-signed: negative for buys
				Commission: money(t.Currency, t.IBCommission),
			})
		}

		// Cash transactions → dividends (income) and withholding, within the year.
		// Withholding rows are matched to a same-instrument, same-day dividend;
		// any unmatched withholding is emitted as its own row for completeness.
		var pending []model.Dividend
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
			case txnDividend:
				pending = append(pending, model.Dividend{
					Instrument:  inst,
					PayDate:     d,
					Gross:       money(c.Currency, c.Amount),
					Withholding: model.NewMoney(model.Currency(c.Currency), nil),
				})
			case txnWithholding:
				k := instKey(c.ISIN, c.Symbol) + "|" + d.Format("20060102")
				withheld[k] = new(big.Rat).Add(orZero(withheld[k]), absRat(parseRat(c.Amount)))
			}
		}
		for i := range pending {
			k := instKey(pending[i].Instrument.ISIN, pending[i].Instrument.Symbol) + "|" + pending[i].PayDate.Format("20060102")
			if w, ok := withheld[k]; ok {
				pending[i].Withholding = model.NewMoney(pending[i].Gross.Currency, w)
				delete(withheld, k)
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
}

type flexCashTxn struct {
	Type       string `xml:"type,attr"`
	Symbol     string `xml:"symbol,attr"`
	ISIN       string `xml:"isin,attr"`
	Description string `xml:"description,attr"`
	Currency   string `xml:"currency,attr"`
	Amount     string `xml:"amount,attr"`
	DateTime   string `xml:"dateTime,attr"`
	SettleDate string `xml:"settleDate,attr"`
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
	txnWithholding
)

func classify(t string) txnKind {
	s := strings.ToLower(t)
	switch {
	case strings.Contains(s, "withholding"):
		return txnWithholding
	case strings.Contains(s, "dividend"), strings.Contains(s, "payment in lieu"):
		return txnDividend
	default:
		return txnOther
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

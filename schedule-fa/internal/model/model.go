// Package model holds the broker-agnostic domain types that flow through the
// schedule-fa pipeline: parsed IBKR data in, Schedule FA rows out.
//
// Money is kept exact with math/big.Rat — never float64 — because every figure
// is multiplied by an FX rate and rounded only at the final reporting step.
package model

import (
	"math/big"
	"time"
)

// Currency is an ISO-4217 code, e.g. "USD", "INR".
type Currency string

const (
	USD Currency = "USD"
	INR Currency = "INR"
)

// Money is an exact monetary amount in a single currency. A nil Amount is zero.
type Money struct {
	Currency Currency
	Amount   *big.Rat
}

// NewMoney builds a Money from a big.Rat (which it does not retain by reference).
func NewMoney(cur Currency, r *big.Rat) Money {
	if r == nil {
		return Money{Currency: cur, Amount: new(big.Rat)}
	}
	return Money{Currency: cur, Amount: new(big.Rat).Set(r)}
}

// IsZero reports whether the amount is zero (a nil Amount counts as zero).
func (m Money) IsZero() bool { return m.Amount == nil || m.Amount.Sign() == 0 }

// Instrument identifies a security held at IBKR.
type Instrument struct {
	Symbol      string   // e.g. "AAPL"
	ISIN        string   // e.g. "US0378331005"
	Name        string   // issuer name, e.g. "Apple Inc"
	AssetClass  string   // e.g. "STK", "ETF", "BOND"
	ListingCtry string   // ISO country of the listing/issuer, e.g. "US"
	Currency    Currency // trading currency
}

// Side is the direction of a trade.
type Side string

const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

// Trade is a single execution.
type Trade struct {
	Instrument Instrument
	Date       time.Time
	Side       Side
	Quantity   *big.Rat // signed positive; Side carries direction
	Price      Money    // per-unit
	Proceeds   Money    // gross proceeds (sells) or cost (buys), trade currency
	Commission Money
}

// Lot is an open tax lot — the unit of "date of acquiring the interest" and
// "initial value" in Table A3.
type Lot struct {
	Instrument Instrument
	OpenDate   time.Time
	VestDate   time.Time // for RSUs: the vesting date = date of acquiring the interest (zero if N/A)
	Quantity   *big.Rat
	CostBasis  Money // total cost in trade currency at acquisition
}

// AcquiredOn is the date the interest was acquired — the holding-period / open
// date (which, for vested RSUs, IBKR already reports as the vesting date). A
// separate VestDate field is only used as a fallback when OpenDate is absent,
// and never when it is in the future (IBKR uses it for forward lock-up dates).
func (l Lot) AcquiredOn() time.Time {
	if !l.OpenDate.IsZero() {
		return l.OpenDate
	}
	return l.VestDate
}

// DividendKind distinguishes a real distribution from a substitute payment made
// when the shares were out on loan. Both are credits to the account (so Schedule
// FA treats them alike), but a payment in lieu is not a dividend for US
// withholding — it is FDAP income withheld at 30% with no treaty rate — so
// Schedule FSI must keep them apart.
type DividendKind string

const (
	DividendCash   DividendKind = "DIVIDEND"
	DividendInLieu DividendKind = "PAYMENT_IN_LIEU"
)

// Dividend is a cash distribution. Schedule FA wants the GROSS figure; the US
// withholding is tracked separately (it is the Schedule TR/FSI credit).
type Dividend struct {
	Instrument  Instrument
	PayDate     time.Time
	Gross       Money
	Withholding Money
	Kind        DividendKind // empty means DividendCash
}

// Interest is interest credited (or debited) to the account — broker credit
// interest on idle cash, bond coupons, and the negative margin-interest rows.
// Amount is signed: positive received, negative paid.
type Interest struct {
	Instrument  Instrument // zero-valued for plain broker cash interest
	Date        time.Time
	Amount      Money
	Withholding Money
	Description string
}

// Withholding is a foreign tax deduction that could not be attributed to a
// specific dividend. It is kept rather than dropped because it is still a
// creditable foreign tax — discarding it silently would forfeit the credit.
type Withholding struct {
	Instrument  Instrument
	Date        time.Time
	Amount      Money // positive
	Description string
}

// RealizedLot is one closed tax lot: the unit of capital-gains computation.
// IBKR's own FIFO matching produces these, and it is the only reliable source
// of the acquisition cost of a lot bought before the reporting period.
//
// Quantity, Cost and Proceeds are positive magnitudes; Commission is the
// closing commission as IBKR reports it (negative). RealizedPnL is IBKR's own
// fifoPnlRealized, kept purely to cross-check our arithmetic.
type RealizedLot struct {
	Instrument  Instrument
	OpenDate    time.Time // acquisition date (drives the holding period)
	CloseDate   time.Time // date of transfer
	Quantity    *big.Rat
	Cost        Money
	Proceeds    Money
	Commission  Money
	RealizedPnL Money
}

// Position is a holding snapshot on a given date (e.g. the 31-Dec close, or a
// daily point used by the peak engine).
type Position struct {
	Instrument Instrument
	Date       time.Time
	Quantity   *big.Rat
	MarkPrice  Money // per-unit close on Date
}

// Account is the IBKR custodial account (feeds Table A2).
type Account struct {
	Number       string
	Name         string
	BaseCurrency Currency
	OpenDate     time.Time
	Institution  string // legal IB entity, e.g. "Interactive Brokers LLC"
	IBEntity     string // raw ibEntity code from the statement, e.g. "IBLLC-US"
	// Holder's address as recorded with IBKR (not the institution's address).
	Street     string
	City       string
	State      string
	PostalCode string
	Country    string // ISO-2, e.g. "US"
}

// CorporateAction is a split/merger/spin-off etc. affecting a holding. We do not
// reprocess these; their presence flags the affected security for manual review.
type CorporateAction struct {
	Instrument  Instrument
	Date        time.Time
	Type        string
	Description string
}

// Statement is everything parsed from one IBKR Activity Flex export, already
// constrained to the requested reporting period.
//
// Schedule FA runs on the CALENDAR year and uses Year; the income schedules run
// on the Apr–Mar FINANCIAL year and use From/To. Year is 0 when the period
// parsed is not a whole calendar year, so FA code cannot silently consume an
// FY statement.
type Statement struct {
	Account              Account
	Year                 int       // calendar year (0 if the period is not a calendar year)
	From                 time.Time // inclusive start of the reporting period
	To                   time.Time // inclusive end of the reporting period
	OpenPositions        []Position
	Lots                 []Lot
	Trades               []Trade
	RealizedLots         []RealizedLot
	Dividends            []Dividend
	Interest             []Interest
	UnmatchedWithholding []Withholding
	CorporateActions     []CorporateAction
}

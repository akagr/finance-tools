// Package gains turns IBKR's closed tax lots into Indian capital-gains figures.
//
// Foreign shares are NOT "listed securities" for Indian tax purposes (they are
// not listed on a recognised stock exchange in India), so sections 111A and 112A
// do not apply. That means:
//
//   - the long-term holding period is 24 MONTHS, not 12
//     (s.2(42A), as amended by the Finance (No. 2) Act 2024);
//   - long-term gains are taxed under s.112 at 12.5% WITHOUT indexation for
//     transfers on or after 23 July 2024, and at 20% with indexation before
//     that date;
//   - there is no ₹1.25 lakh exemption (that is s.112A only);
//   - short-term gains are taxed at slab rates, not 20%.
//
// Gains land in Schedule CG rows A5 (STCG) and B8 (LTCG) — the residual rows —
// not in the listed-equity rows.
//
// FX follows Rule 115, not the Schedule FA convention: see package rule115.
// Two methods are in live practitioner use and they give different answers when
// the rupee has moved, so the choice is explicit rather than hidden:
//
//	PerLeg  (default) cost at the specified date of the ACQUISITION month, and
//	        proceeds at the specified date of the TRANSFER month. Rupee
//	        depreciation over the holding period therefore lands inside the
//	        taxable gain — a resident gets no currency shelter, because the
//	        first proviso to s.48 (and Rule 115A) apply only to non-residents.
//	NetGain compute the gain in the foreign currency first, then convert the
//	        whole gain at the transfer month's specified date.
package gains

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/akagr/finance-tools/schedule-fa/internal/fx"
	"github.com/akagr/finance-tools/schedule-fa/internal/model"
	"github.com/akagr/finance-tools/schedule-fa/internal/rule115"
)

// Method is the FX convention used to compute the gain.
type Method string

const (
	PerLeg  Method = "per-leg"
	NetGain Method = "net-gain"
)

// ParseMethod validates a method name.
func ParseMethod(s string) (Method, error) {
	switch Method(strings.TrimSpace(strings.ToLower(s))) {
	case PerLeg, "":
		return PerLeg, nil
	case NetGain:
		return NetGain, nil
	default:
		return "", fmt.Errorf("gains: unknown method %q (want %q or %q)", s, PerLeg, NetGain)
	}
}

// Term is the holding-period classification.
type Term string

const (
	Short Term = "STCG"
	Long  Term = "LTCG"
)

// LongTermMonths is the holding period beyond which a foreign share is
// long-term (s.2(42A) after the Finance (No. 2) Act 2024).
const LongTermMonths = 24

// rateChange is the date from which s.112 charges 12.5% without indexation.
var rateChange = time.Date(2024, time.July, 23, 0, 0, 0, 0, time.UTC)

// Disposal is one closed lot expressed in Indian tax terms.
type Disposal struct {
	Instrument  model.Instrument
	OpenDate    time.Time
	CloseDate   time.Time
	Quantity    *big.Rat
	Term        Term
	PreRateCut  bool // transferred before 23 Jul 2024: 20% with indexation
	Method      Method
	Cost        fx.Conversion // INR cost of acquisition
	Proceeds    fx.Conversion // INR full value of consideration
	Expense     fx.Conversion // INR transfer expenditure (closing commission)
	GainINR     *big.Rat
	GainFCY     *big.Rat
	NeedsReview bool
	ReviewNote  string
}

// Summary is the capital-gains position for the year.
type Summary struct {
	Method      Method
	Disposals   []Disposal
	STCG        *big.Rat // slab-rated, Schedule CG row A5
	LTCG        *big.Rat // 12.5% without indexation, Schedule CG row B8
	LTCGPreCut  *big.Rat // transfers before 23 Jul 2024: 20% WITH indexation, unindexed here
	NeedsReview bool
	ReviewNotes []string
}

// Compute converts every closed lot in the statement into a Disposal and
// aggregates the year's gains. fyStart is the financial year's starting
// calendar year (2025 for FY 2025-26).
func Compute(st *model.Statement, store fx.Store, fyStart int, m Method) (*Summary, error) {
	sum := &Summary{
		Method:     m,
		STCG:       new(big.Rat),
		LTCG:       new(big.Rat),
		LTCGPreCut: new(big.Rat),
	}

	corpActions := map[string]int{}
	for _, ca := range st.CorporateActions {
		corpActions[instKey(ca.Instrument)]++
	}

	lots := append([]model.RealizedLot(nil), st.RealizedLots...)
	sort.SliceStable(lots, func(i, j int) bool {
		if !lots[i].CloseDate.Equal(lots[j].CloseDate) {
			return lots[i].CloseDate.Before(lots[j].CloseDate)
		}
		return instKey(lots[i].Instrument) < instKey(lots[j].Instrument)
	})

	for _, l := range lots {
		d, err := disposal(l, store, fyStart, m)
		if err != nil {
			return nil, err
		}
		if n := corpActions[instKey(l.Instrument)]; n > 0 {
			d.addNote(fmt.Sprintf("%d corporate action(s) on this security in the year — lot matching and cost basis may need adjusting", n))
		}
		switch {
		case d.Term == Short:
			sum.STCG.Add(sum.STCG, d.GainINR)
		case d.PreRateCut:
			sum.LTCGPreCut.Add(sum.LTCGPreCut, d.GainINR)
		default:
			sum.LTCG.Add(sum.LTCG, d.GainINR)
		}
		if d.NeedsReview {
			sum.NeedsReview = true
		}
		sum.Disposals = append(sum.Disposals, d)
	}

	if sum.LTCGPreCut.Sign() != 0 {
		sum.NeedsReview = true
		sum.ReviewNotes = append(sum.ReviewNotes,
			"long-term transfers before 23 Jul 2024 are taxed at 20% WITH indexation; the figure shown is NOT indexed — apply the cost inflation index manually")
	}
	if sum.STCG.Sign() < 0 || sum.LTCG.Sign() < 0 || sum.LTCGPreCut.Sign() < 0 {
		sum.NeedsReview = true
		sum.ReviewNotes = append(sum.ReviewNotes,
			"a net capital LOSS cannot be reported as income in Schedule FSI — carry it to Schedule CG/CFL under the set-off and carry-forward rules")
	}
	return sum, nil
}

func disposal(l model.RealizedLot, store fx.Store, fyStart int, m Method) (Disposal, error) {
	d := Disposal{
		Instrument: l.Instrument,
		OpenDate:   l.OpenDate,
		CloseDate:  l.CloseDate,
		Quantity:   ratOf2(l.Quantity),
		Method:     m,
		PreRateCut: l.CloseDate.Before(rateChange),
	}

	d.Term = Long
	switch {
	case l.OpenDate.IsZero():
		// Without an acquisition date the holding period is unknowable; assume
		// the higher-taxed short term and flag rather than guess in the
		// taxpayer's favour.
		d.Term = Short
		d.addNote("acquisition date missing — treated as SHORT term; verify the holding period")
	case !l.CloseDate.After(l.OpenDate.AddDate(0, LongTermMonths, 0)):
		d.Term = Short
	}

	cur := l.Proceeds.Currency
	if cur == "" {
		cur = l.Cost.Currency
	}
	expenseFCY := new(big.Rat).Abs(ratOf(l.Commission))
	proceedsFCY := ratOf(l.Proceeds)
	costFCY := ratOf(l.Cost)
	d.GainFCY = new(big.Rat).Sub(new(big.Rat).Sub(proceedsFCY, expenseFCY), costFCY)

	var err error
	switch m {
	case NetGain:
		// One rate for the whole gain: the transfer month's specified date.
		if d.Proceeds, err = conv(store, cur, proceedsFCY, l.CloseDate, fyStart); err != nil {
			return d, err
		}
		if d.Expense, err = conv(store, cur, expenseFCY, l.CloseDate, fyStart); err != nil {
			return d, err
		}
		if d.Cost, err = conv(store, cur, costFCY, l.CloseDate, fyStart); err != nil {
			return d, err
		}
		gain, err := conv(store, cur, d.GainFCY, l.CloseDate, fyStart)
		if err != nil {
			return d, err
		}
		d.GainINR = ratOf(gain.Result)
	default:
		// Per leg: cost at the acquisition month, proceeds at the transfer month.
		if d.Cost, err = conv(store, cur, costFCY, l.OpenDate, fyStart); err != nil {
			return d, err
		}
		if d.Proceeds, err = conv(store, cur, proceedsFCY, l.CloseDate, fyStart); err != nil {
			return d, err
		}
		if d.Expense, err = conv(store, cur, expenseFCY, l.CloseDate, fyStart); err != nil {
			return d, err
		}
		d.GainINR = new(big.Rat).Sub(
			new(big.Rat).Sub(ratOf(d.Proceeds.Result), ratOf(d.Expense.Result)),
			ratOf(d.Cost.Result))
	}

	// Cross-check our arithmetic against IBKR's own realized P&L. A mismatch
	// means the lot is not a plain long sale (a short cover inverts the signs)
	// or the statement is missing a leg — either way a human must look.
	if pnl := ratOf(l.RealizedPnL); pnl.Sign() != 0 {
		diff := new(big.Rat).Sub(d.GainFCY, pnl)
		if diff.Abs(diff).Cmp(big.NewRat(1, 1)) > 0 {
			d.addNote(fmt.Sprintf("computed %s gain %s does not match IBKR's realized P&L %s — check for a short sale or a missing leg",
				cur, ratStr(d.GainFCY), ratStr(pnl)))
		}
	}
	return d, nil
}

func (d *Disposal) addNote(s string) {
	d.NeedsReview = true
	if d.ReviewNote == "" {
		d.ReviewNote = s
		return
	}
	d.ReviewNote += "; " + s
}

func conv(store fx.Store, cur model.Currency, amt *big.Rat, event time.Time, fyStart int) (fx.Conversion, error) {
	return rule115.Convert(store, model.NewMoney(cur, amt), rule115.CapitalGains, event, fyStart)
}

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

func ratOf2(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(r)
}

func ratStr(r *big.Rat) string { return r.FloatString(2) }

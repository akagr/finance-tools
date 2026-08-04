package store

import (
	"database/sql"
	"time"

	"github.com/akagr/finance-tools/networth/internal/analysis"
	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
)

// Summary is everything needed to answer "how am I doing, and how much of that
// did I actually earn?" - assembled once and shared by the dashboard, the
// reports and the exports so they can never disagree.
type Summary struct {
	AsOf        string
	NetWorth    money.Amount
	Assets      money.Amount
	Liabilities money.Amount
	Known       bool
	// UnknownAccounts is carried everywhere the total is, because a missing
	// price or exchange rate silently shrinks a SUM and must never pass unseen.
	UnknownAccounts int

	// The period being reported on: from the previous valuation, or from the
	// start of history when asked for the whole span.
	Since      string
	SinceValue money.Amount
	Days       float64

	Change       money.Amount
	Contribution money.Amount
	MarketReturn money.Amount

	XIRR    float64
	HasXIRR bool
	TWR     analysis.TWRResult
	HasTWR  bool

	// ValuationPoints is how many times the portfolio was actually measured.
	// Every time-weighted figure is only as good as this number.
	ValuationPoints int
	// UnexplainedShare is the fraction of the portfolio sitting in accounts
	// whose balance moved with no recorded contribution. That movement lands in
	// "market return" whether it was earned or deposited, so this is the single
	// most useful measure of how far to trust the split.
	UnexplainedShare float64

	Issues []db.Issue
}

// GapToXIRR is how much of the money-weighted return came from the timing and
// size of contributions rather than from the investments.
func (s Summary) GapToXIRR() float64 { return s.XIRR - s.TWR.Annualised }

// Summarise builds a Summary covering everything from since (exclusive) to the
// latest data. An empty since covers the whole history.
func (s *Store) Summarise(since string) (Summary, error) {
	var out Summary

	latest, err := s.LatestDate()
	if err != nil {
		return out, err
	}
	if latest == "" {
		return out, nil // an empty database is not an error
	}
	out.AsOf = latest

	if err := s.DB.QueryRow(`SELECT networth, assets, liabilities, unknown_accounts
		FROM v_networth WHERE as_of = ?`, latest).Scan(
		nullAmount(&out.NetWorth, &out.Known), nullAmount(&out.Assets, new(bool)),
		nullAmount(&out.Liabilities, new(bool)), &out.UnknownAccounts); err != nil && err != sql.ErrNoRows {
		return out, err
	}

	vals, err := s.ValuationDates()
	if err != nil {
		return out, err
	}
	out.ValuationPoints = len(vals)
	if len(vals) == 0 {
		out.Issues, _ = s.DB.Check()
		return out, nil
	}

	start := since
	if start == "" {
		start = vals[0]
	}
	out.Since = start

	if err := s.DB.QueryRow(`SELECT n.networth FROM v_networth n
		WHERE n.as_of <= ? ORDER BY n.as_of DESC LIMIT 1`, start).Scan(
		nullAmount(&out.SinceValue, new(bool))); err != nil && err != sql.ErrNoRows {
		return out, err
	}

	flows, err := s.Flows(start, latest)
	if err != nil {
		return out, err
	}
	for _, f := range flows {
		out.Contribution += f.Amount
	}
	out.Change = out.NetWorth - out.SinceValue
	out.MarketReturn = out.Change - out.Contribution

	t0, err0 := time.Parse(DateFormat, start)
	t1, err1 := time.Parse(DateFormat, latest)
	if err0 == nil && err1 == nil {
		out.Days = t1.Sub(t0).Hours() / 24
	}

	out.fillReturns(s, vals, start, latest, flows)

	var unexplained, covered int64
	if err := s.DB.QueryRow(`SELECT COALESCE(SUM(unexplained_value),0), COALESCE(SUM(covered_value),0)
		FROM v_attribution_quality WHERE start_date >= ?`, start).Scan(&unexplained, &covered); err == nil && covered > 0 {
		out.UnexplainedShare = float64(unexplained) / float64(covered)
	}

	out.Issues, _ = s.DB.Check()
	return out, nil
}

// fillReturns computes both return measures over the reporting window.
func (out *Summary) fillReturns(s *Store, vals []string, start, latest string, flows []Flow) {
	// Money-weighted: the opening value and every contribution are outflows from
	// the investor, the closing value is the inflow that settles them.
	cfs := []analysis.Cashflow{}
	if t, err := time.Parse(DateFormat, start); err == nil && out.SinceValue != 0 {
		cfs = append(cfs, analysis.Cashflow{Date: t, Amount: -out.SinceValue})
	}
	for _, f := range flows {
		t, err := time.Parse(DateFormat, f.Date)
		if err != nil || !f.Known {
			continue
		}
		cfs = append(cfs, analysis.Cashflow{Date: t, Amount: -f.Amount})
	}
	if t, err := time.Parse(DateFormat, latest); err == nil {
		cfs = append(cfs, analysis.Cashflow{Date: t, Amount: out.NetWorth})
	}
	if r, err := analysis.XIRR(cfs); err == nil {
		out.XIRR, out.HasXIRR = r, true
	}

	// Time-weighted: one sub-period between each pair of consecutive valuations
	// inside the window, so contribution timing cancels out.
	var periods []analysis.Period
	within := []string{}
	for _, v := range vals {
		if v >= start && v <= latest {
			within = append(within, v)
		}
	}
	for i := 0; i+1 < len(within); i++ {
		t0, err0 := time.Parse(DateFormat, within[i])
		t1, err1 := time.Parse(DateFormat, within[i+1])
		if err0 != nil || err1 != nil {
			continue
		}
		v0, ok0 := s.netWorthOn(within[i])
		v1, ok1 := s.netWorthOn(within[i+1])
		if !ok0 || !ok1 {
			continue
		}
		p := analysis.Period{Start: t0, End: t1, StartValue: v0, EndValue: v1}
		for _, f := range flows {
			if !f.Known || f.Date <= within[i] || f.Date > within[i+1] {
				continue
			}
			if ft, err := time.Parse(DateFormat, f.Date); err == nil {
				p.Flows = append(p.Flows, analysis.Cashflow{Date: ft, Amount: f.Amount})
			}
		}
		periods = append(periods, p)
	}
	if res, err := analysis.TWR(periods); err == nil {
		out.TWR, out.HasTWR = res, true
	}
}

func (s *Store) netWorthOn(date string) (money.Amount, bool) {
	var v sql.NullInt64
	err := s.DB.QueryRow(`SELECT networth FROM v_networth WHERE as_of <= ? ORDER BY as_of DESC LIMIT 1`,
		date).Scan(&v)
	if err != nil || !v.Valid {
		return 0, false
	}
	return money.Amount(v.Int64), true
}

// AttributionRow is one valuation-to-valuation period, split into what was added
// and what was earned.
type AttributionRow struct {
	Start, End   string
	StartValue   money.Amount
	EndValue     money.Amount
	Change       money.Amount
	Contribution money.Amount
	MarketReturn money.Amount
	Known        bool
}

// Attribution lists every period, newest last.
func (s *Store) Attribution() ([]AttributionRow, error) {
	rows, err := s.DB.Query(`SELECT start_date, end_date, start_value, end_value,
		change, net_contribution, market_return FROM v_attribution ORDER BY start_date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttributionRow
	for rows.Next() {
		var r AttributionRow
		var sv, ev, ch, mr sql.NullInt64
		if err := rows.Scan(&r.Start, &r.End, &sv, &ev, &ch, &r.Contribution, &mr); err != nil {
			return nil, err
		}
		r.StartValue, r.EndValue = money.Amount(sv.Int64), money.Amount(ev.Int64)
		r.Change, r.MarketReturn = money.Amount(ch.Int64), money.Amount(mr.Int64)
		r.Known = sv.Valid && ev.Valid
		out = append(out, r)
	}
	return out, rows.Err()
}

// nullAmount scans a possibly-NULL money column, recording whether it was known.
func nullAmount(dst *money.Amount, known *bool) any {
	return &amountScanner{dst: dst, known: known}
}

type amountScanner struct {
	dst   *money.Amount
	known *bool
}

func (a *amountScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a.dst, *a.known = 0, false
	case int64:
		*a.dst, *a.known = money.Amount(v), true
	case float64:
		*a.dst, *a.known = money.Amount(int64(v)), true
	default:
		*a.dst, *a.known = 0, false
	}
	return nil
}

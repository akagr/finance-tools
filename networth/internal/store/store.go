// Package store is the typed layer over the database: accounts, balance
// snapshots and the flows that cross the net worth boundary.
//
// It deliberately keeps the SQL that expresses *policy* in the view layer
// (views.sql) and holds only the SQL that expresses *operations* here, so that
// anyone querying the file directly sees the same definitions the application
// uses.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
)

// DateFormat is the only date format stored, so dates sort lexically in SQL.
const DateFormat = "2006-01-02"

// ExternalAccount is the counter-party for money entering or leaving net worth.
//
// Double entry needs both legs, but demanding that someone tracking net worth
// twice a year also model where every rupee came from would kill the habit. This
// account absorbs the other leg, so recording "I put 50,000 into the PPF" stays a
// single action while the arithmetic underneath stays balanced and the boundary
// logic keeps working.
const ExternalAccount = "External"

// Store provides typed access to the database.
type Store struct {
	DB *db.DB
}

// New wraps a database handle.
func New(d *db.DB) *Store { return &Store{DB: d} }

// Account is a place value sits, and the boundary marker for net worth.
type Account struct {
	ID          int64
	Name        string
	Kind        string // asset, liability, income, expense, equity
	Currency    string
	InNetworth  bool
	Tracking    string // snapshots, transactions, units
	Institution string
	OpenedOn    string
	ClosedOn    string
	Tags        map[string]string
	Notes       string
	SortOrder   int
}

// Closed reports whether the account has been closed.
func (a Account) Closed() bool { return a.ClosedOn != "" }

// Accounts lists accounts, optionally including closed ones.
func (s *Store) Accounts(includeClosed bool) ([]Account, error) {
	q := `SELECT id, name, kind, currency, in_networth, tracking, institution,
		COALESCE(opened_on,''), COALESCE(closed_on,''), tags, notes, sort_order
		FROM accounts`
	if !includeClosed {
		q += ` WHERE closed_on IS NULL`
	}
	q += ` ORDER BY sort_order, name`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var inNW int
		var tags string
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &a.Currency, &inNW, &a.Tracking,
			&a.Institution, &a.OpenedOn, &a.ClosedOn, &tags, &a.Notes, &a.SortOrder); err != nil {
			return nil, err
		}
		a.InNetworth = inNW == 1
		a.Tags = parseTags(tags)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AccountByName finds an account by its unique name.
func (s *Store) AccountByName(name string) (Account, bool, error) {
	accts, err := s.Accounts(true)
	if err != nil {
		return Account{}, false, err
	}
	for _, a := range accts {
		if strings.EqualFold(a.Name, name) {
			return a, true, nil
		}
	}
	return Account{}, false, nil
}

// AddAccount inserts an account and returns its id.
func (s *Store) AddAccount(a Account) (int64, error) {
	if a.Kind == "" {
		a.Kind = "asset"
	}
	if a.Tracking == "" {
		a.Tracking = "snapshots"
	}
	res, err := s.DB.Exec(`INSERT INTO accounts
		(name, kind, currency, in_networth, tracking, institution, opened_on, closed_on, tags, notes, sort_order)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Kind, a.Currency, boolToInt(a.InNetworth), a.Tracking, a.Institution,
		nullIfEmpty(a.OpenedOn), nullIfEmpty(a.ClosedOn), encodeTags(a.Tags), a.Notes, a.SortOrder)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAccount saves changes to an existing account.
func (s *Store) UpdateAccount(a Account) error {
	_, err := s.DB.Exec(`UPDATE accounts SET name=?, kind=?, currency=?, in_networth=?,
		tracking=?, institution=?, opened_on=?, closed_on=?, tags=?, notes=?, sort_order=?
		WHERE id=?`,
		a.Name, a.Kind, a.Currency, boolToInt(a.InNetworth), a.Tracking, a.Institution,
		nullIfEmpty(a.OpenedOn), nullIfEmpty(a.ClosedOn), encodeTags(a.Tags), a.Notes, a.SortOrder, a.ID)
	return err
}

// DeleteAccount removes an account and everything recorded against it. It fails
// if the account has history, because losing history silently is worse than
// making the user close the account instead.
func (s *Store) DeleteAccount(id int64) error {
	var n int
	if err := s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM snapshots WHERE account_id=?) +
		(SELECT COUNT(*) FROM postings  WHERE account_id=?) +
		(SELECT COUNT(*) FROM trades    WHERE account_id=?)`, id, id, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("account has %d recorded entries; close it instead of deleting it", n)
	}
	_, err := s.DB.Exec(`DELETE FROM accounts WHERE id=?`, id)
	return err
}

// externalAccountID returns the counter-party account, creating it on demand.
func (s *Store) externalAccountID() (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM accounts WHERE name=?`, ExternalAccount).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	cur, _ := s.baseCurrency()
	return s.AddAccount(Account{
		Name:       ExternalAccount,
		Kind:       "equity",
		Currency:   cur,
		InNetworth: false,
		Tracking:   "transactions",
		SortOrder:  1000,
		Notes:      "The world outside your net worth. Contributions come from here and withdrawals go to it.",
	})
}

func (s *Store) baseCurrency() (string, error) {
	st, err := s.DB.Settings()
	if err != nil {
		return "INR", err
	}
	return st.BaseCurrency, nil
}

// Balance is an account's value on a date, with the provenance needed to judge
// how much to trust it.
type Balance struct {
	AccountID  int64
	Account    string
	Kind       string
	Currency   string
	InNetworth bool
	Tracking   string
	Balance    money.Amount
	BaseValue  money.Amount
	Known      bool // false when a missing rate or price makes the value unknown
	AnchorDate string
	Confidence string
	Tags       map[string]string
}

// BalancesAt returns every account's balance on a date.
func (s *Store) BalancesAt(date string) ([]Balance, error) {
	rows, err := s.DB.Query(`SELECT account_id, account, kind, currency, in_networth, tracking,
		COALESCE(balance,0), balance_base, COALESCE(anchor_date,''), COALESCE(anchor_confidence,''), tags
		FROM v_balances_base WHERE as_of = ? ORDER BY balance_base DESC`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Balance
	for rows.Next() {
		var b Balance
		var inNW int
		var base sql.NullInt64
		var tags string
		if err := rows.Scan(&b.AccountID, &b.Account, &b.Kind, &b.Currency, &inNW, &b.Tracking,
			&b.Balance, &base, &b.AnchorDate, &b.Confidence, &tags); err != nil {
			return nil, err
		}
		b.InNetworth = inNW == 1
		b.BaseValue = money.Amount(base.Int64)
		b.Known = base.Valid
		b.Tags = parseTags(tags)
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestDate is the most recent date on which anything was recorded. Reports are
// dated from the data, never from the clock, so they are reproducible.
func (s *Store) LatestDate() (string, error) {
	var d sql.NullString
	err := s.DB.QueryRow(`SELECT MAX(d) FROM v_event_dates`).Scan(&d)
	if err != nil {
		return "", err
	}
	return d.String, nil
}

// LastSnapshot is the most recent balance recorded for an account.
type LastSnapshot struct {
	AsOf    string
	Balance money.Amount
	Found   bool
}

// LastSnapshots returns the latest recorded balance per account, which is what
// the update flow shows as the starting point for each entry.
func (s *Store) LastSnapshots() (map[int64]LastSnapshot, error) {
	rows, err := s.DB.Query(`SELECT s.account_id, s.as_of, s.balance FROM snapshots s
		WHERE s.scenario_id = 0 AND s.as_of = (
			SELECT MAX(s2.as_of) FROM snapshots s2
			WHERE s2.account_id = s.account_id AND s2.scenario_id = 0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]LastSnapshot{}
	for rows.Next() {
		var id int64
		var ls LastSnapshot
		if err := rows.Scan(&id, &ls.AsOf, &ls.Balance); err != nil {
			return nil, err
		}
		ls.Found = true
		out[id] = ls
	}
	return out, rows.Err()
}

// Entry is one account's result from an update round: the new balance, and
// optionally how much of the change was money the user added or took out.
//
// Capturing the contribution at the same moment as the balance is what makes
// this whole design work on a twice-a-year rhythm. Without it, a balance that
// grew is indistinguishable from one that was topped up, and every return figure
// is guesswork.
type Entry struct {
	AccountID    int64
	Balance      money.Amount
	Currency     string
	Contribution money.Amount // positive added, negative withdrawn, zero if none
	// ContributionDate defaults to the midpoint of the period since the previous
	// snapshot, the standard assumption when the actual date is unknown.
	ContributionDate string
	Confidence       string
	Note             string
}

// RecordUpdate writes a whole update round in one transaction, so a half-entered
// round can never be left behind.
func (s *Store) RecordUpdate(date string, entries []Entry) error {
	if _, err := time.Parse(DateFormat, date); err != nil {
		return fmt.Errorf("bad date %q: want YYYY-MM-DD", date)
	}
	last, err := s.LastSnapshots()
	if err != nil {
		return err
	}
	var extID int64
	for _, e := range entries {
		if e.Contribution != 0 {
			extID, err = s.externalAccountID()
			if err != nil {
				return err
			}
			break
		}
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, e := range entries {
		conf := e.Confidence
		if conf == "" {
			conf = "exact"
		}
		if _, err := tx.Exec(`INSERT INTO snapshots
			(account_id, as_of, balance, currency, method, confidence, source, note)
			VALUES (?,?,?,?,?,?,'manual',?)
			ON CONFLICT(account_id, as_of, scenario_id) DO UPDATE SET
				balance = excluded.balance, confidence = excluded.confidence,
				note = excluded.note, recorded_at = datetime('now')`,
			e.AccountID, date, int64(e.Balance), e.Currency, "statement", conf, e.Note); err != nil {
			return fmt.Errorf("saving balance for account %d: %w", e.AccountID, err)
		}
		if e.Contribution == 0 {
			continue
		}
		cdate := e.ContributionDate
		if cdate == "" {
			cdate = midpoint(last[e.AccountID].AsOf, date)
		}
		kind := "contribution"
		if e.Contribution < 0 {
			kind = "withdrawal"
		}
		res, err := tx.Exec(`INSERT INTO txns (date, description, source) VALUES (?,?,'update')`,
			cdate, fmt.Sprintf("%s recorded in the %s update", kind, date))
		if err != nil {
			return err
		}
		txnID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO postings (txn_id, account_id, amount, currency, flow_kind)
			VALUES (?,?,?,?,?)`, txnID, e.AccountID, int64(e.Contribution), e.Currency, kind); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO postings (txn_id, account_id, amount, currency, flow_kind)
			VALUES (?,?,?,?,?)`, txnID, extID, int64(-e.Contribution), e.Currency, kind); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.DB.SetUIState("last_update", date)
}

// midpoint returns the date halfway between two dates, used when a contribution
// is known to have happened during a period but not exactly when. It is an
// assumption, and an honest one: assuming the start or the end would bias every
// time-weighted return in a consistent direction.
func midpoint(from, to string) string {
	t2, err := time.Parse(DateFormat, to)
	if err != nil {
		return to
	}
	t1, err := time.Parse(DateFormat, from)
	if err != nil || !t1.Before(t2) {
		return to
	}
	return t1.Add(t2.Sub(t1) / 2).Format(DateFormat)
}

// DeleteSnapshot removes one recorded balance.
func (s *Store) DeleteSnapshot(accountID int64, asOf string) error {
	_, err := s.DB.Exec(`DELETE FROM snapshots WHERE account_id=? AND as_of=? AND scenario_id=0`,
		accountID, asOf)
	return err
}

// Flow is one movement of money across the net worth boundary.
type Flow struct {
	Date        string
	Description string
	Amount      money.Amount
	Known       bool
}

// Flows lists boundary crossings between two dates, inclusive of the end.
func (s *Store) Flows(from, to string) ([]Flow, error) {
	rows, err := s.DB.Query(`SELECT as_of, description, net_flow FROM v_flows
		WHERE as_of > ? AND as_of <= ? ORDER BY as_of`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		var f Flow
		var amt sql.NullInt64
		if err := rows.Scan(&f.Date, &f.Description, &amt); err != nil {
			return nil, err
		}
		f.Amount, f.Known = money.Amount(amt.Int64), amt.Valid
		out = append(out, f)
	}
	return out, rows.Err()
}

// Point is one observation in a series.
type Point struct {
	Date  string
	Value money.Amount
	Known bool
}

// NetWorthSeries is the headline history, one point per date on which anything
// changed.
func (s *Store) NetWorthSeries() ([]Point, error) {
	rows, err := s.DB.Query(`SELECT as_of, networth FROM v_networth ORDER BY as_of`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var v sql.NullInt64
		if err := rows.Scan(&p.Date, &v); err != nil {
			return nil, err
		}
		p.Value, p.Known = money.Amount(v.Int64), v.Valid
		out = append(out, p)
	}
	return out, rows.Err()
}

// ValuationDates are the dates the portfolio was actually measured on. They are
// the hard limit on how finely returns can be attributed.
func (s *Store) ValuationDates() ([]string, error) {
	rows, err := s.DB.Query(`SELECT d FROM v_valuation_dates ORDER BY d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func parseTags(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if sv, ok := v.(string); ok {
			out[k] = sv
		} else {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

func encodeTags(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(m))
	for _, k := range keys {
		ordered[k] = m[k]
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// AccountsWithHoldings reports which accounts have recorded unit trades, and so
// can have a balance derived from quantity times price.
//
// It exists so that an account marked as unit-tracked but holding nothing yet
// still appears in the balance round. Dropping it would make an account silently
// vanish the moment its tracking mode changed, which is the sort of quiet
// disappearance that costs a year of data before anyone notices.
func (s *Store) AccountsWithHoldings() (map[int64]bool, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT account_id FROM trades WHERE scenario_id = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

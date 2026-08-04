package store

import (
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akagr/finance-tools/networth/internal/expr"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/plan"
)

// IncomeAccount and SpendAccount are the counter-parties for money entering and
// leaving net worth through the monthly routing. Keeping them apart (rather than
// reusing the generic External account) is what lets income and spending be told
// apart afterwards, and therefore what makes a savings rate computable.
const (
	IncomeAccount = "Income"
	SpendAccount  = "Spending"
)

// Rules lists the standing allocation instructions.
func (s *Store) Rules() ([]plan.Rule, error) {
	rows, err := s.DB.Query(`SELECT r.id, r.name, r.account_id, a.name, r.stage, r.kind,
		r.basis, r.value, r.formula, r.priority, r.active, r.notes, a.in_networth
		FROM allocation_rules r JOIN accounts a ON a.id = r.account_id
		ORDER BY CASE r.stage WHEN 'setaside' THEN 0 ELSE 1 END, r.priority, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []plan.Rule
	for rows.Next() {
		var r plan.Rule
		var active, inNW int
		if err := rows.Scan(&r.ID, &r.Name, &r.AccountID, &r.Account, &r.Stage, &r.Kind,
			&r.Basis, &r.Value, &r.Formula, &r.Priority, &active, &r.Notes, &inNW); err != nil {
			return nil, err
		}
		r.Active = active == 1
		r.Outside = inNW == 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRule inserts or updates one allocation rule.
func (s *Store) SaveRule(r plan.Rule) (int64, error) {
	if r.Name == "" {
		return 0, fmt.Errorf("a rule needs a name")
	}
	if r.AccountID == 0 {
		return 0, fmt.Errorf("a rule needs an account to route money to")
	}
	// Basis only means anything for a percentage, but the column is constrained,
	// so give the other kinds a harmless default rather than making every caller
	// remember to set a field that will be ignored.
	if r.Basis == "" {
		r.Basis = plan.BasisInvestable
	}
	if r.Stage == "" {
		r.Stage = plan.StageInvest
	}
	if r.Kind == plan.KindFormula {
		// A formula is checked before it is stored, because the moment it is
		// wrong is the moment it is being written, not the month it silently
		// routes nothing.
		if _, err := expr.Parse(r.Formula); err != nil {
			return 0, fmt.Errorf("formula: %w", err)
		}
	}
	if r.ID != 0 {
		_, err := s.DB.Exec(`UPDATE allocation_rules SET name=?, account_id=?, stage=?,
			kind=?, basis=?, value=?, formula=?, priority=?, active=?, notes=? WHERE id=?`,
			r.Name, r.AccountID, string(r.Stage), string(r.Kind), string(r.Basis),
			int64(r.Value), r.Formula, r.Priority, boolToInt(r.Active), r.Notes, r.ID)
		return r.ID, err
	}
	res, err := s.DB.Exec(`INSERT INTO allocation_rules
		(name, account_id, stage, kind, basis, value, formula, priority, active, notes)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		r.Name, r.AccountID, string(r.Stage), string(r.Kind), string(r.Basis),
		int64(r.Value), r.Formula, r.Priority, boolToInt(r.Active), r.Notes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteRule removes a rule. Plans already executed keep their own stored lines,
// so history is unaffected.
func (s *Store) DeleteRule(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM allocation_rules WHERE id=?`, id)
	return err
}

// IncomeAccountID is the account salary is paid into and routed out of.
func (s *Store) IncomeAccountID() (int64, bool) {
	var v string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key='income_account_id'`).Scan(&v); err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id=?`, id).Scan(&n); err != nil || n == 0 {
		return 0, false
	}
	return id, true
}

// SetIncomeAccount records where income lands.
func (s *Store) SetIncomeAccount(id int64) error {
	return s.DB.SetSetting("income_account_id", strconv.FormatInt(id, 10))
}

// Plan is a month's routing, stored.
type Plan struct {
	ID       int64
	AsOf     string
	Income   money.Amount
	Status   string
	Note     string
	Executed bool
	Lines    []PlanLine
}

// PlanLine is one stored routing instruction.
type PlanLine struct {
	ID         int64
	AccountID  int64
	Account    string
	RuleID     int64
	Label      string
	Stage      plan.Stage
	Amount     money.Amount
	Overridden bool
}

// Plans lists every month, newest first.
func (s *Store) Plans(limit int) ([]Plan, error) {
	rows, err := s.DB.Query(`SELECT id, as_of, income, status, note FROM plans
		ORDER BY as_of DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.AsOf, &p.Income, &p.Status, &p.Note); err != nil {
			return nil, err
		}
		p.Executed = p.Status == "executed"
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlanFor loads the stored plan for a month, if there is one.
func (s *Store) PlanFor(asOf string) (Plan, bool, error) {
	var p Plan
	err := s.DB.QueryRow(`SELECT id, as_of, income, status, note FROM plans WHERE as_of=?`,
		asOf).Scan(&p.ID, &p.AsOf, &p.Income, &p.Status, &p.Note)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.Executed = p.Status == "executed"
	p.Lines, err = s.planLines(p.ID)
	return p, true, err
}

func (s *Store) planLines(planID int64) ([]PlanLine, error) {
	rows, err := s.DB.Query(`SELECT l.id, l.account_id, a.name, COALESCE(l.rule_id,0),
		l.label, l.stage, l.amount, l.overridden
		FROM plan_lines l JOIN accounts a ON a.id = l.account_id
		WHERE l.plan_id = ? ORDER BY l.sort_order, l.id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanLine
	for rows.Next() {
		var l PlanLine
		var over int
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Account, &l.RuleID, &l.Label,
			&l.Stage, &l.Amount, &over); err != nil {
			return nil, err
		}
		l.Overridden = over == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

// SavePlan writes a month's computed routing as a draft, replacing any existing
// draft for the same month. An executed plan is never silently rewritten: the
// transactions it produced are already history.
func (s *Store) SavePlan(asOf string, income money.Amount, res plan.Result, note string) (int64, error) {
	if _, err := time.Parse(DateFormat, asOf); err != nil {
		return 0, fmt.Errorf("bad date %q: want YYYY-MM-DD", asOf)
	}
	existing, found, err := s.PlanFor(asOf)
	if err != nil {
		return 0, err
	}
	if found && existing.Executed {
		return 0, fmt.Errorf("the plan for %s has already been executed; revert it first", asOf)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var planID int64
	if found {
		planID = existing.ID
		if _, err := tx.Exec(`UPDATE plans SET income=?, note=? WHERE id=?`,
			int64(income), note, planID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM plan_lines WHERE plan_id=?`, planID); err != nil {
			return 0, err
		}
	} else {
		r, err := tx.Exec(`INSERT INTO plans (as_of, income, status, note) VALUES (?,?,'draft',?)`,
			asOf, int64(income), note)
		if err != nil {
			return 0, err
		}
		if planID, err = r.LastInsertId(); err != nil {
			return 0, err
		}
	}

	for i, l := range res.Lines {
		var ruleID any
		if l.RuleID != 0 {
			ruleID = l.RuleID
		}
		if _, err := tx.Exec(`INSERT INTO plan_lines
			(plan_id, account_id, rule_id, label, stage, amount, overridden, sort_order)
			VALUES (?,?,?,?,?,?,?,?)`,
			planID, l.AccountID, ruleID, l.Label, string(l.Stage), int64(l.Amount),
			boolToInt(l.Overridden), i); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return planID, s.DB.SetUIState("last_plan", asOf)
}

// ExecutePlan turns a routing decision into the transactions that record it.
//
// The money is written the way it actually moves, which is what makes the
// resulting figures correct without any further bookkeeping:
//
//	income        Income  -> the account it is paid into   (enters net worth)
//	set-asides    that account -> Spending                 (leaves net worth)
//	investments   that account -> the target account       (stays inside; nets to zero)
//
// So the month's contribution to net worth is income minus spending, per-account
// flows are right for a per-account return, and none of it had to be classified
// by hand.
//
// Every transaction carries a natural key, so executing twice is refused rather
// than duplicated.
func (s *Store) ExecutePlan(planID int64) error {
	p, err := s.planByID(planID)
	if err != nil {
		return err
	}
	if p.Executed {
		return fmt.Errorf("the plan for %s has already been executed", p.AsOf)
	}
	incomeID, ok := s.IncomeAccountID()
	if !ok {
		return fmt.Errorf("set the account your income is paid into before executing a plan")
	}
	acct, err := s.accountByID(incomeID)
	if err != nil {
		return err
	}
	incomeSrc, err := s.namedAccount(IncomeAccount, "income",
		"Where salary and other income comes from. Outside net worth by definition.")
	if err != nil {
		return err
	}
	spendDst, err := s.namedAccount(SpendAccount, "expense",
		"Where spending goes. Outside net worth, so spending correctly reduces it.")
	if err != nil {
		return err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	post := func(externalID, desc string, legs [][3]any) error {
		r, err := tx.Exec(`INSERT INTO txns (date, description, source, external_id)
			VALUES (?,?,'plan',?)`, p.AsOf, desc, externalID)
		if err != nil {
			return err
		}
		txnID, err := r.LastInsertId()
		if err != nil {
			return err
		}
		for _, leg := range legs {
			if _, err := tx.Exec(`INSERT INTO postings
				(txn_id, account_id, amount, currency, flow_kind) VALUES (?,?,?,?,?)`,
				txnID, leg[0], leg[1], acct.Currency, leg[2]); err != nil {
				return err
			}
		}
		return nil
	}

	if p.Income != 0 {
		if err := post(fmt.Sprintf("plan:%d:income", p.ID),
			"Income for "+p.AsOf, [][3]any{
				{incomeID, int64(p.Income), "contribution"},
				{incomeSrc, -int64(p.Income), "income"},
			}); err != nil {
			return err
		}
	}

	for _, l := range p.Lines {
		if l.Amount == 0 {
			continue
		}
		if l.Stage == plan.StageSetAside {
			// Set-asides target a spending category. If the user pointed one at
			// an account inside net worth (an emergency fund, say), the boundary
			// logic handles it as a transfer with no further help needed.
			dst := l.AccountID
			if dst == 0 {
				dst = spendDst
			}
			if err := post(fmt.Sprintf("plan:%d:line:%d", p.ID, l.ID),
				l.Label+" for "+p.AsOf, [][3]any{
					{incomeID, -int64(l.Amount), "expense"},
					{dst, int64(l.Amount), "expense"},
				}); err != nil {
				return err
			}
			continue
		}
		if l.AccountID == incomeID {
			// Routing money to the account it already sits in is a no-op, not an
			// error; recording it would produce a meaningless zero transaction.
			continue
		}
		if err := post(fmt.Sprintf("plan:%d:line:%d", p.ID, l.ID),
			l.Label+" for "+p.AsOf, [][3]any{
				{incomeID, -int64(l.Amount), "transfer"},
				{l.AccountID, int64(l.Amount), "transfer"},
			}); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE plans SET status='executed', executed_at=datetime('now')
		WHERE id=?`, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RevertPlan undoes an execution, removing the transactions it wrote. Recording
// the wrong income and being unable to take it back would make the tool
// something to be afraid of.
func (s *Store) RevertPlan(planID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM txns WHERE source='plan'
		AND external_id LIKE 'plan:' || ? || ':%'`, planID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE plans SET status='draft', executed_at=NULL WHERE id=?`,
		planID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) planByID(id int64) (Plan, error) {
	var p Plan
	err := s.DB.QueryRow(`SELECT id, as_of, income, status, note FROM plans WHERE id=?`,
		id).Scan(&p.ID, &p.AsOf, &p.Income, &p.Status, &p.Note)
	if err != nil {
		return p, err
	}
	p.Executed = p.Status == "executed"
	p.Lines, err = s.planLines(p.ID)
	return p, err
}

func (s *Store) accountByID(id int64) (Account, error) {
	accts, err := s.Accounts(true)
	if err != nil {
		return Account{}, err
	}
	for _, a := range accts {
		if a.ID == id {
			return a, nil
		}
	}
	return Account{}, fmt.Errorf("account %d no longer exists", id)
}

// namedAccount finds or creates one of the bookkeeping accounts that sit outside
// net worth, so the user never has to create them by hand.
func (s *Store) namedAccount(name, kind, notes string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM accounts WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	cur, _ := s.baseCurrency()
	return s.AddAccount(Account{
		Name: name, Kind: kind, Currency: cur, InNetworth: false,
		Tracking: "transactions", SortOrder: 1000, Notes: notes,
	})
}

// ContributionsSince totals the money that has crossed into net worth for one
// account since a date.
//
// The update screen shows this so that a contribution already recorded by a
// monthly plan is not entered a second time. Double-counting it would inflate
// contributions and understate returns by the same amount, which is exactly the
// error this whole design exists to prevent.
func (s *Store) ContributionsSince(accountID int64, since string) (money.Amount, error) {
	var v sql.NullInt64
	err := s.DB.QueryRow(`SELECT SUM(p.amount) FROM postings p
		JOIN txns t ON t.id = p.txn_id
		JOIN accounts a ON a.id = p.account_id
		WHERE p.account_id = ? AND t.scenario_id = 0 AND t.date > ?`,
		accountID, since).Scan(&v)
	if err != nil {
		return 0, err
	}
	return money.Amount(v.Int64), nil
}

// RecordedContributions returns, per account, the net flow already recorded
// since that account's last valuation.
func (s *Store) RecordedContributions() (map[int64]money.Amount, error) {
	rows, err := s.DB.Query(`SELECT p.account_id, SUM(p.amount)
		FROM postings p JOIN txns t ON t.id = p.txn_id
		WHERE t.scenario_id = 0
		  AND t.date > COALESCE((SELECT MAX(s.as_of) FROM snapshots s
				WHERE s.account_id = p.account_id AND s.scenario_id = 0), '')
		GROUP BY p.account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]money.Amount{}
	for rows.Next() {
		var id int64
		var amt int64
		if err := rows.Scan(&id, &amt); err != nil {
			return nil, err
		}
		out[id] = money.Amount(amt)
	}
	return out, rows.Err()
}

// AccountBalances resolves the selectors a formula can use against the latest
// known balances.
//
// It reads the balances as they stand now, which is what a routing decision
// needs: how much is already in the spending account, how much is owed on the
// cards. Those are facts about today, not about the reporting period.
type AccountBalances struct {
	byName map[string]money.Amount
	byTag  map[string]money.Amount
	byKind map[string]money.Amount
	all    money.Amount
}

// Balance implements expr.Balances.
func (b *AccountBalances) Balance(selector string) (*big.Rat, bool) {
	sel := strings.TrimSpace(selector)
	if sel == "*" {
		return b.all.Rat(), true
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(sel), "tag:"); ok {
		v, found := b.byTag[strings.ToLower(strings.TrimSpace(rest))]
		return v.Rat(), found
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(sel), "kind:"); ok {
		v, found := b.byKind[strings.ToLower(strings.TrimSpace(rest))]
		return v.Rat(), found
	}
	v, found := b.byName[strings.ToLower(sel)]
	return v.Rat(), found
}

// Selectors lists every selector that currently matches something, so the
// interface can show what a formula is allowed to refer to instead of leaving it
// to be guessed.
func (b *AccountBalances) Selectors() []string {
	var out []string
	for k := range b.byName {
		out = append(out, k)
	}
	for k := range b.byTag {
		out = append(out, "tag:"+k)
	}
	for k := range b.byKind {
		out = append(out, "kind:"+k)
	}
	sort.Strings(out)
	return out
}

// Balances loads the current balance of every account, indexed by the ways a
// formula can select them.
func (s *Store) Balances() (*AccountBalances, error) {
	b := &AccountBalances{
		byName: map[string]money.Amount{},
		byTag:  map[string]money.Amount{},
		byKind: map[string]money.Amount{},
	}
	latest, err := s.LatestDate()
	if err != nil {
		return nil, err
	}
	accts, err := s.Accounts(false)
	if err != nil {
		return nil, err
	}
	// Every account is selectable by name even with no balance yet, so a formula
	// written before the first balance round still resolves.
	for _, a := range accts {
		b.byName[strings.ToLower(a.Name)] = 0
		b.byKind[strings.ToLower(a.Kind)] += 0
		for k, v := range a.Tags {
			b.byTag[strings.ToLower(k+"="+v)] += 0
		}
	}
	if latest == "" {
		return b, nil
	}

	bals, err := s.BalancesAt(latest)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Account{}
	for _, a := range accts {
		byID[a.ID] = a
	}
	for _, bal := range bals {
		a, ok := byID[bal.AccountID]
		if !ok {
			continue
		}
		b.byName[strings.ToLower(bal.Account)] = bal.Balance
		b.byKind[strings.ToLower(bal.Kind)] += bal.Balance
		if bal.InNetworth {
			b.all += bal.Balance
		}
		for k, v := range a.Tags {
			b.byTag[strings.ToLower(k+"="+v)] += bal.Balance
		}
	}
	return b, nil
}

// ComputePlan applies the current rules to an income figure, resolving any
// formulas against today's balances.
func (s *Store) ComputePlan(income money.Amount, overrides map[int64]money.Amount) (plan.Result, error) {
	rules, err := s.Rules()
	if err != nil {
		return plan.Result{}, err
	}
	bals, err := s.Balances()
	if err != nil {
		return plan.Result{}, err
	}
	return plan.Compute(income, rules, overrides, bals), nil
}

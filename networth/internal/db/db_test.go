package db

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")

	d, created, err := Open(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created {
		t.Error("first Open should report the database as created")
	}
	if got := d.Meta("format"); got != "networth" {
		t.Errorf("format meta = %q, want networth", got)
	}
	if got := d.Meta("scale"); got != "100000000" {
		t.Errorf("scale meta = %q, want 100000000", got)
	}
	d.Close()

	d2, created2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	if created2 {
		t.Error("reopening an existing database should not report it as created")
	}
}

func TestOpenRefusesForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Exec(`CREATE TABLE unrelated (x INTEGER)`)
	d.Exec(`DROP TABLE app_meta`)
	d.Close()

	if _, _, err := Open(path); err == nil {
		t.Fatal("expected Open to refuse a database that is not ours")
	}
}

func TestViewsAreReinstalledOnEveryOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an older formula layer being left behind by a previous version.
	if _, err := d.Exec(`DROP VIEW v_networth`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d2, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	var n int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='view' AND name='v_networth'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("v_networth should have been recreated on open")
	}
}

// TestAllViewsQueryable is the guard that matters most: the whole derived layer
// is SQL text, so a typo in it would otherwise only surface at runtime, in
// whichever report happened to touch the broken view first.
func TestAllViewsQueryable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	seed(t, d)

	rows, err := d.Query(`SELECT name FROM sqlite_master WHERE type='view' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	if len(names) < 15 {
		t.Fatalf("expected the full view layer, found only %d views", len(names))
	}
	for _, n := range names {
		if _, err := d.Exec(`SELECT * FROM ` + n + ` LIMIT 5`); err != nil {
			t.Errorf("view %s is not queryable: %v", n, err)
		}
	}
}

func TestSavedQueriesInstalled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)

	qs, err := d.SavedQueries()
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) == 0 {
		t.Fatal("no built-in saved queries were installed")
	}
	for _, q := range qs {
		if q.Description == "" {
			t.Errorf("saved query %q has no description; it would not be discoverable", q.Name)
		}
		if _, err := d.RunQuery(q.SQL, 10); err != nil {
			t.Errorf("saved query %q does not run: %v", q.Name, err)
		}
	}
}

// seed inserts a small but structurally complete dataset: two accounts inside
// the net worth boundary, one outside it, a contribution crossing the boundary,
// a transfer that does not, and two valuation dates.
func seed(t *testing.T, d *DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO accounts (id, name, kind, currency, in_networth, tracking, tags) VALUES
			(1, 'Bank',   'asset', 'INR', 1, 'snapshots', '{"asset_class":"cash"}'),
			(2, 'Equity', 'asset', 'INR', 1, 'snapshots', '{"asset_class":"equity"}'),
			(3, 'Salary', 'income', 'INR', 0, 'transactions', '{}')`,
		`INSERT INTO snapshots (account_id, as_of, balance, currency) VALUES
			(1, '2025-01-01', 10000000000000, 'INR'),
			(2, '2025-01-01', 50000000000000, 'INR'),
			(1, '2025-07-01', 12000000000000, 'INR'),
			(2, '2025-07-01', 58000000000000, 'INR')`,
		`INSERT INTO txns (id, date, description) VALUES (1, '2025-03-01', 'Salary into bank')`,
		`INSERT INTO postings (txn_id, account_id, amount, currency, flow_kind) VALUES
			(1, 1,  5000000000000, 'INR', 'contribution'),
			(1, 3, -5000000000000, 'INR', 'income')`,
		`INSERT INTO txns (id, date, description) VALUES (2, '2025-04-01', 'Bank to equity')`,
		`INSERT INTO postings (txn_id, account_id, amount, currency, flow_kind) VALUES
			(2, 1, -3000000000000, 'INR', 'transfer'),
			(2, 2,  3000000000000, 'INR', 'transfer')`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
}

// TestInternalTransferIsNotAContribution locks the single most important
// property of the model: money moved between two accounts you own must not
// register as new money, or every return figure is inflated.
func TestInternalTransferIsNotAContribution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)

	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM v_flows WHERE txn_id = 2`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("a transfer between two accounts inside the boundary was counted as a flow")
	}

	var flow int64
	if err := d.QueryRow(`SELECT net_flow FROM v_flows WHERE txn_id = 1`).Scan(&flow); err != nil {
		t.Fatalf("the salary transaction should be an external flow: %v", err)
	}
	if flow != 5000000000000 {
		t.Errorf("external flow = %d, want 5000000000000", flow)
	}
}

// TestAttributionIdentity checks that change always decomposes exactly into
// contributions plus market return. This identity is the entire reason the tool
// exists, so it is asserted rather than assumed.
func TestAttributionIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)

	rows, err := d.Query(`SELECT start_date, end_date, change, net_contribution, market_return FROM v_attribution`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var start, end string
		var change, contrib, ret int64
		if err := rows.Scan(&start, &end, &change, &contrib, &ret); err != nil {
			t.Fatal(err)
		}
		n++
		if change != contrib+ret {
			t.Errorf("%s..%s: change %d != contribution %d + return %d", start, end, change, contrib, ret)
		}
		// Seeded: net worth 6,00,000 -> 7,00,000 with 50,000 contributed.
		if contrib != 5000000000000 {
			t.Errorf("%s..%s: contribution = %d, want 5000000000000", start, end, contrib)
		}
		if ret != 5000000000000 {
			t.Errorf("%s..%s: market return = %d, want 5000000000000", start, end, ret)
		}
	}
	if n != 1 {
		t.Errorf("expected one attribution period, got %d", n)
	}
}

func TestCheckFindsUnbalancedTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)

	// Exactly what a careless hand-edit in an external SQL client looks like.
	if _, err := d.Exec(`INSERT INTO txns (id, date, description) VALUES (9, '2025-05-01', 'oops')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO postings (txn_id, account_id, amount, currency) VALUES (9, 1, 100, 'INR')`); err != nil {
		t.Fatal(err)
	}

	issues, err := d.Check()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range issues {
		if i.Check == "unbalanced" {
			found = true
		}
	}
	if !found {
		t.Error("check did not report the unbalanced transaction")
	}
}

func TestMissingExchangeRateIsUnknownNotZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nw.db")
	d, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)

	// A dollar account with no published rate must make net worth explicitly
	// unknown. Quietly leaving it out would understate net worth with no sign.
	if _, err := d.Exec(`INSERT INTO accounts (id, name, kind, currency, in_networth, tracking)
		VALUES (4, 'IBKR', 'asset', 'USD', 1, 'snapshots')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO snapshots (account_id, as_of, balance, currency)
		VALUES (4, '2025-07-01', 1000000000000, 'USD')`); err != nil {
		t.Fatal(err)
	}

	var unknown int
	if err := d.QueryRow(`SELECT unknown_accounts FROM v_networth WHERE as_of='2025-07-01'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 {
		t.Errorf("unknown_accounts = %d, want 1", unknown)
	}

	issues, _ := d.Check()
	found := false
	for _, i := range issues {
		if i.Check == "missing_fx" {
			found = true
		}
	}
	if !found {
		t.Error("check did not report the missing exchange rate")
	}
}

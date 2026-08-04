-- Migration 0001: the initial schema.
--
-- Design notes that the rest of the application depends on:
--
--   * The database is the only source of truth. Everything needed to interpret
--     it - the money scale, the base currency, display preferences - lives in
--     app_meta/settings, so another machine (or another person's copy) needs no
--     configuration files.
--   * Money and quantities are INTEGERs scaled by 1e8. They are exact, they sort
--     correctly, and SUM()/AVG() work in ad-hoc SQL. REAL would silently lose
--     exactness; a decimal string could not be summed.
--   * Facts live in tables; everything derived lives in views, which the
--     application drops and recreates from embedded SQL on every open. Nothing
--     derived is ever stored, so hand-editing the database can never leave a
--     stale cache behind.
--   * Every fact carries as_of (when it was true) and recorded_at (when it was
--     learned), because late-arriving statements and corrections are normal and
--     bitemporality cannot be retrofitted.

CREATE TABLE app_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
) STRICT;

-- settings are user-facing preferences. They live here rather than in a config
-- file so that the database stays self-contained and portable.
CREATE TABLE settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT ''
) STRICT;

-- scenarios separate reality from what-ifs. Scenario 0 is reality; every view
-- filters to it by default so projections can never contaminate history.
CREATE TABLE scenarios (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

INSERT INTO scenarios (id, name, notes)
VALUES (0, 'actual', 'Reality. Never delete this row.');

-- accounts are the boundary of net worth.
--
-- in_networth is the single most important column in the schema. With it, a
-- transfer between two accounts you own nets to zero and is automatically
-- excluded from contribution figures, while money arriving from outside (salary)
-- or leaving (spending) is automatically a real flow. Without it, moving cash
-- from a bank to a fund looks like a contribution and inflates the fund's return
-- denominator, which is the classic spreadsheet error.
--
-- tracking says where the balance comes from:
--   snapshots    - you periodically state the balance (EPF, PPF, property)
--   transactions - the balance is the sum of postings (bank, credit card)
--   units        - the balance is quantity x price (equities, mutual funds)
-- Accounts may mix: a snapshot anchors the balance and postings after it apply
-- as deltas, so partial data always degrades gracefully.
CREATE TABLE accounts (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL CHECK (kind IN ('asset','liability','income','expense','equity')),
    currency    TEXT NOT NULL,
    in_networth INTEGER NOT NULL DEFAULT 0 CHECK (in_networth IN (0,1)),
    tracking    TEXT NOT NULL DEFAULT 'snapshots' CHECK (tracking IN ('snapshots','transactions','units')),
    opened_on   TEXT,
    closed_on   TEXT,
    institution TEXT NOT NULL DEFAULT '',
    -- tags is a JSON object of arbitrary dimensions: asset_class, liquidity,
    -- goal, owner, jurisdiction, risk. This is the escape valve that means a new
    -- way of slicing the portfolio is a WHERE clause, not a schema migration.
    tags        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tags)),
    notes       TEXT NOT NULL DEFAULT '',
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK (closed_on IS NULL OR opened_on IS NULL OR closed_on >= opened_on)
) STRICT;

CREATE INDEX idx_accounts_networth ON accounts(in_networth, kind);

-- txns and postings are double entry, kept deliberately light. Postings of a
-- transaction must sum to zero within each currency; a currency conversion is
-- recorded as two postings in different currencies plus the rate, so nothing is
-- silently invented.
CREATE TABLE txns (
    id          INTEGER PRIMARY KEY,
    date        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'manual',
    -- external_id is the natural key from the source system, so re-importing a
    -- broker statement updates rather than duplicates.
    external_id TEXT,
    scenario_id INTEGER NOT NULL DEFAULT 0 REFERENCES scenarios(id),
    tags        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tags)),
    recorded_at TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

CREATE INDEX idx_txns_date ON txns(date, scenario_id);
CREATE UNIQUE INDEX idx_txns_external ON txns(source, external_id) WHERE external_id IS NOT NULL;

CREATE TABLE postings (
    id         INTEGER PRIMARY KEY,
    txn_id     INTEGER NOT NULL REFERENCES txns(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    amount     INTEGER NOT NULL,
    currency   TEXT NOT NULL,
    -- flow_kind annotates why money moved, for reporting. It never overrides the
    -- in_networth boundary calculation, which is structural.
    flow_kind  TEXT NOT NULL DEFAULT 'other'
        CHECK (flow_kind IN ('contribution','withdrawal','transfer','income','expense','fee','tax','other')),
    tags       TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tags))
) STRICT;

CREATE INDEX idx_postings_account ON postings(account_id, txn_id);

-- snapshots are balance assertions: "on this date this account held this much".
-- For snapshot-tracked accounts they are the data. For transaction-tracked
-- accounts they are a reconciliation check that catches entry errors a
-- spreadsheet would silently absorb.
--
-- method and confidence keep uncertainty visible: a flat valued from a broker's
-- guess is not the same kind of fact as a fund NAV, and reports must be able to
-- say so rather than laundering it into a precise-looking number.
CREATE TABLE snapshots (
    id          INTEGER PRIMARY KEY,
    account_id  INTEGER NOT NULL REFERENCES accounts(id),
    as_of       TEXT NOT NULL,
    balance     INTEGER NOT NULL,
    currency    TEXT NOT NULL,
    method      TEXT NOT NULL DEFAULT 'statement'
        CHECK (method IN ('statement','valuation','estimate','derived')),
    confidence  TEXT NOT NULL DEFAULT 'exact'
        CHECK (confidence IN ('exact','estimate','rough')),
    source      TEXT NOT NULL DEFAULT 'manual',
    note        TEXT NOT NULL DEFAULT '',
    scenario_id INTEGER NOT NULL DEFAULT 0 REFERENCES scenarios(id),
    recorded_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (account_id, as_of, scenario_id)
) STRICT;

CREATE INDEX idx_snapshots_asof ON snapshots(as_of, scenario_id);

-- instruments, prices and trades let quantity be separated from valuation. That
-- separation is what turns a twice-a-year habit into a daily net worth series:
-- units change rarely and are entered by hand, prices are fetched automatically.
-- An FX rate is just the price of a currency, and CPI is just another series, so
-- multi-currency reporting and inflation-adjusted returns are the same query
-- with a different instrument.
CREATE TABLE instruments (
    id       INTEGER PRIMARY KEY,
    symbol   TEXT NOT NULL UNIQUE,
    name     TEXT NOT NULL DEFAULT '',
    kind     TEXT NOT NULL DEFAULT 'equity'
        CHECK (kind IN ('equity','fund','bond','currency','index','commodity','crypto','other')),
    currency TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tags)),
    notes    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE prices (
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    as_of         TEXT NOT NULL,
    price         INTEGER NOT NULL,
    source        TEXT NOT NULL DEFAULT 'manual',
    recorded_at   TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (instrument_id, as_of)
) STRICT;

-- trades hold signed quantity deltas; a position is their running sum.
CREATE TABLE trades (
    id            INTEGER PRIMARY KEY,
    account_id    INTEGER NOT NULL REFERENCES accounts(id),
    instrument_id INTEGER NOT NULL REFERENCES instruments(id),
    as_of         TEXT NOT NULL,
    quantity      INTEGER NOT NULL,
    price         INTEGER NOT NULL DEFAULT 0,
    fees          INTEGER NOT NULL DEFAULT 0,
    currency      TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'manual',
    external_id   TEXT,
    scenario_id   INTEGER NOT NULL DEFAULT 0 REFERENCES scenarios(id),
    recorded_at   TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

CREATE INDEX idx_trades_account ON trades(account_id, as_of);
CREATE UNIQUE INDEX idx_trades_external ON trades(source, external_id) WHERE external_id IS NOT NULL;

-- saved_queries are named formulas. They live in the database so they travel
-- with the data and are available to any SQL client, not just this application.
CREATE TABLE saved_queries (
    name        TEXT PRIMARY KEY,
    sql         TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    builtin     INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0,1)),
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- sync_state checkpoints importers and price fetches so runs are resumable and
-- idempotent without any state outside the database.
CREATE TABLE sync_state (
    source      TEXT PRIMARY KEY,
    cursor      TEXT NOT NULL DEFAULT '',
    last_run_at TEXT,
    note        TEXT NOT NULL DEFAULT ''
) STRICT;

-- ui_state holds application state - last view, cursor positions, the date of
-- the last balance update round. Keeping it here is what makes handing someone
-- the file hand over the whole application, not just its data.
CREATE TABLE ui_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

-- audit_log records every change, because hand-editing in an external SQL client
-- is an expected workflow and there has to be a way back from a bad UPDATE.
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    at         TEXT NOT NULL DEFAULT (datetime('now')),
    table_name TEXT NOT NULL,
    row_id     INTEGER,
    op         TEXT NOT NULL CHECK (op IN ('insert','update','delete')),
    before     TEXT,
    after      TEXT
) STRICT;

-- Invariant: the postings of a transaction must balance within each currency.
--
-- This is deliberately NOT a trigger. SQLite has no deferred constraints, so an
-- AFTER INSERT trigger would reject the first posting of every balanced pair and
-- make hand-editing in an external SQL client painful - the opposite of the goal.
-- Instead it is expressed as a view that `networth check` reports on and every
-- report consults, so a violation is loud but never blocks a legitimate edit
-- half-way through. See views.sql (v_check_unbalanced).

CREATE TRIGGER trg_accounts_audit_update
AFTER UPDATE ON accounts
BEGIN
    INSERT INTO audit_log (table_name, row_id, op, before, after)
    VALUES ('accounts', OLD.id, 'update',
            json_object('name', OLD.name, 'kind', OLD.kind, 'currency', OLD.currency,
                        'in_networth', OLD.in_networth, 'tracking', OLD.tracking,
                        'closed_on', OLD.closed_on, 'tags', OLD.tags),
            json_object('name', NEW.name, 'kind', NEW.kind, 'currency', NEW.currency,
                        'in_networth', NEW.in_networth, 'tracking', NEW.tracking,
                        'closed_on', NEW.closed_on, 'tags', NEW.tags));
END;

CREATE TRIGGER trg_snapshots_audit_update
AFTER UPDATE ON snapshots
BEGIN
    INSERT INTO audit_log (table_name, row_id, op, before, after)
    VALUES ('snapshots', OLD.id, 'update',
            json_object('account_id', OLD.account_id, 'as_of', OLD.as_of, 'balance', OLD.balance),
            json_object('account_id', NEW.account_id, 'as_of', NEW.as_of, 'balance', NEW.balance));
END;

CREATE TRIGGER trg_snapshots_audit_delete
AFTER DELETE ON snapshots
BEGIN
    INSERT INTO audit_log (table_name, row_id, op, before, after)
    VALUES ('snapshots', OLD.id, 'delete',
            json_object('account_id', OLD.account_id, 'as_of', OLD.as_of,
                        'balance', OLD.balance, 'currency', OLD.currency), NULL);
END;

INSERT INTO settings (key, value, description) VALUES
    ('base_currency',  'INR',    'Currency every report is expressed in.'),
    ('grouping',       'indian', 'Digit grouping for display: indian, western or none.'),
    ('fy_start_month', '4',      'First month of the financial year (4 = April).'),
    ('decimals',       '0',      'Decimal places shown for money in reports.');

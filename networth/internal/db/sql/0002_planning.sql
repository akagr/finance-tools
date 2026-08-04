-- Migration 0002: monthly allocation planning.
--
-- This exists because the twice-a-year balance round can say *that* money was
-- added but not reliably *how much* - it depends on the user remembering. The
-- monthly routing decision ("income arrived; how much goes where?") is a
-- separate habit that already produces exactly that number, so capturing it here
-- makes the contribution figure a record rather than a recollection, and lifts
-- the number of known cashflow dates from two a year to twelve.
--
-- The money is modelled the way it actually moves: income lands in one account,
-- then routes out to spending and to investments. That single choice makes
-- several things correct for free -
--
--   * portfolio contribution = income - spending, because only those two cross
--     the net worth boundary while the routing between your own accounts nets to
--     zero;
--   * per-account flows are right, so a per-account return is meaningful;
--   * savings rate is derivable rather than a separate figure to maintain.
--
-- Treating each investment as its own contribution would have been simpler and
-- would have double-counted money as it moved.

-- allocation_rules are the reusable template: the standing answer to "where does
-- income go?". A plan applies them to one month's actual income.
CREATE TABLE allocation_rules (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    -- stage orders the waterfall. Everything at 'setaside' comes off income
    -- first (spending, EMI, insurance); what survives is investable, and the
    -- 'invest' rules divide that.
    stage      TEXT NOT NULL DEFAULT 'invest' CHECK (stage IN ('setaside','invest')),
    -- kind and basis together cover how people actually think about this: a
    -- fixed rupee amount, a percentage of income, a percentage of what is left
    -- after spending, or "whatever remains".
    kind       TEXT NOT NULL CHECK (kind IN ('percent','fixed','remainder','formula')),
    -- 'remaining' is what is left of the investable amount after the rules with
    -- a lower priority number have taken their share. It is what makes "a fixed
    -- instalment first, then split the rest" expressible without a formula.
    basis      TEXT NOT NULL DEFAULT 'investable'
        CHECK (basis IN ('income','investable','remaining')),
    -- value is scaled by 1e8 like every other number here: a fixed amount in
    -- currency units, or a percentage as a fraction (40% is 0.40).
    value      INTEGER NOT NULL DEFAULT 0,
    -- formula holds an arithmetic expression for kind='formula'. Real routing
    -- decisions refer to other accounts - "top the spending account up to cover
    -- a lakh plus the credit cards, less what is already there" - and no fixed
    -- set of rule types covers that. See internal/expr.
    formula    TEXT NOT NULL DEFAULT '',
    priority   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

CREATE INDEX idx_rules_order ON allocation_rules(active, stage, priority);

-- plans are one month's application of the rules to real income. A plan is a
-- draft until it is executed, at which point it writes the transactions that
-- record where the money went.
CREATE TABLE plans (
    id          INTEGER PRIMARY KEY,
    as_of       TEXT NOT NULL UNIQUE,
    income      INTEGER NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','executed')),
    executed_at TEXT,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- plan_lines are the computed routing, stored rather than recomputed so that a
-- month executed under one set of rules is not silently rewritten when the rules
-- change later. History has to stay what actually happened.
CREATE TABLE plan_lines (
    id         INTEGER PRIMARY KEY,
    plan_id    INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    rule_id    INTEGER REFERENCES allocation_rules(id) ON DELETE SET NULL,
    label      TEXT NOT NULL,
    stage      TEXT NOT NULL CHECK (stage IN ('setaside','invest')),
    amount     INTEGER NOT NULL,
    -- overridden marks a line the user changed by hand, so the interface can
    -- show which figures came from the rules and which did not.
    overridden INTEGER NOT NULL DEFAULT 0 CHECK (overridden IN (0,1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE (plan_id, account_id, label)
) STRICT;

CREATE INDEX idx_plan_lines_plan ON plan_lines(plan_id, sort_order);

-- The account income is paid into, and therefore routed out of. Stored as a
-- setting so the database stays self-contained.
INSERT INTO settings (key, value, description) VALUES
    ('income_account_id', '', 'Account salary is paid into and routed out of.');

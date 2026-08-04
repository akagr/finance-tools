-- The formula layer.
--
-- Every view here is dropped and recreated by the application each time it opens
-- a database. That is the resolution of an apparent conflict: the data must live
-- in the file (so it is portable and self-contained), but formulas are code and
-- belong in version control (so they can be fixed and improved). Installing them
-- on open gives both, and means someone else's database picks up the current
-- formula layer automatically - including when they open it in sqlite3 or any
-- other SQL client.
--
-- Nothing derived is ever stored in a table. If it were, a hand-edited fact
-- would silently disagree with a cached total, which is exactly the failure mode
-- of a spreadsheet.
--
-- Conventions:
--   * All money is INTEGER scaled by 1e8 (see money package and app_meta.scale).
--   * Everything filters scenario_id = 0 (reality) unless it exists to expose
--     scenarios.
--   * Views never call date('now'): reports must be reproducible, so the as-of
--     date is always passed in by the caller.

DROP VIEW IF EXISTS v_quality;
DROP VIEW IF EXISTS v_check_stale;
DROP VIEW IF EXISTS v_check_reconcile;
DROP VIEW IF EXISTS v_check_missing_fx;
DROP VIEW IF EXISTS v_check_unbalanced;
DROP VIEW IF EXISTS v_attribution_quality;
DROP VIEW IF EXISTS v_attribution;
DROP VIEW IF EXISTS v_periods;
DROP VIEW IF EXISTS v_valuation_dates;
DROP VIEW IF EXISTS v_account_flows;
DROP VIEW IF EXISTS v_flows;
DROP VIEW IF EXISTS v_networth_daily;
DROP VIEW IF EXISTS v_balances_daily;
DROP VIEW IF EXISTS v_networth;
DROP VIEW IF EXISTS v_allocation;
DROP VIEW IF EXISTS v_balances_base;
DROP VIEW IF EXISTS v_balances;
DROP VIEW IF EXISTS v_holdings;
DROP VIEW IF EXISTS v_positions;
DROP VIEW IF EXISTS v_fx;
DROP VIEW IF EXISTS v_currencies;
DROP VIEW IF EXISTS v_calendar;
DROP VIEW IF EXISTS v_event_dates;
DROP VIEW IF EXISTS v_data_range;
DROP VIEW IF EXISTS v_config;

-- v_config flattens settings into a single row, so other views can reference
-- (SELECT base_currency FROM v_config) instead of repeating a lookup.
CREATE VIEW v_config AS
SELECT
    (SELECT value FROM settings WHERE key = 'base_currency')  AS base_currency,
    (SELECT value FROM settings WHERE key = 'grouping')       AS grouping,
    CAST((SELECT value FROM settings WHERE key = 'fy_start_month') AS INTEGER) AS fy_start_month,
    100000000 AS scale;

-- v_data_range is bounded by the data itself and never by the wall clock, so
-- every derived series is reproducible.
CREATE VIEW v_data_range AS
WITH d(x) AS (
    SELECT as_of FROM snapshots WHERE scenario_id = 0
    UNION SELECT date   FROM txns   WHERE scenario_id = 0
    UNION SELECT as_of  FROM trades WHERE scenario_id = 0
    UNION SELECT as_of  FROM prices
)
SELECT MIN(x) AS start_date, MAX(x) AS end_date FROM d;

-- v_event_dates is the set of dates on which anything actually changed. Balances
-- are step functions, so evaluating them only at event dates is both exact and
-- far cheaper than a daily spine.
CREATE VIEW v_event_dates AS
SELECT as_of AS d FROM snapshots WHERE scenario_id = 0
UNION SELECT date  FROM txns   WHERE scenario_id = 0
UNION SELECT as_of FROM trades WHERE scenario_id = 0;

-- v_calendar is a daily spine across the data range, for charting and for
-- ad-hoc queries that want an evenly spaced series.
CREATE VIEW v_calendar AS
WITH RECURSIVE r(d) AS (
    SELECT start_date FROM v_data_range WHERE start_date IS NOT NULL
    UNION ALL
    SELECT date(d, '+1 day') FROM r WHERE d < (SELECT end_date FROM v_data_range)
)
SELECT d FROM r;

CREATE VIEW v_currencies AS
SELECT DISTINCT currency FROM accounts
UNION SELECT DISTINCT currency FROM instruments
UNION SELECT base_currency FROM v_config;

-- v_fx gives the rate from each currency to the base currency on each event
-- date, carried forward from the last published rate.
--
-- Convention: a foreign currency is an instrument with kind='currency', symbol =
-- the currency code being priced, and currency = the base currency. An FX rate
-- is therefore just a price, and CPI or any other index can be carried the same
-- way, which is what makes inflation-adjusted reporting the same query with a
-- different series.
CREATE VIEW v_fx AS
SELECT
    e.d AS as_of,
    c.currency,
    CASE
        WHEN c.currency = (SELECT base_currency FROM v_config) THEN 100000000
        ELSE (
            SELECT p.price FROM prices p
            JOIN instruments i ON i.id = p.instrument_id
            WHERE i.kind = 'currency' AND i.symbol = c.currency AND p.as_of <= e.d
            ORDER BY p.as_of DESC LIMIT 1
        )
    END AS rate
FROM v_event_dates e
CROSS JOIN v_currencies c;

-- v_positions is the running quantity of each instrument in each account.
CREATE VIEW v_positions AS
SELECT
    e.d AS as_of,
    x.account_id,
    x.instrument_id,
    (SELECT COALESCE(SUM(tr.quantity), 0) FROM trades tr
      WHERE tr.account_id = x.account_id
        AND tr.instrument_id = x.instrument_id
        AND tr.scenario_id = 0
        AND tr.as_of <= e.d) AS quantity
FROM v_event_dates e
CROSS JOIN (SELECT DISTINCT account_id, instrument_id FROM trades WHERE scenario_id = 0) x;

-- v_holdings values each position at the last known price.
--
-- quantity and price are both scaled by 1e8, so their product must be divided by
-- 1e8 once. That product can exceed int64, so it is computed in floating point
-- and rounded back to a scaled integer. This is the one place exactness is
-- knowingly given up, and it is safe: a market price is an observation with its
-- own error, far larger than the 1e-8 rounding here. Sums of money elsewhere
-- stay exact integers.
CREATE VIEW v_holdings AS
SELECT
    p.as_of,
    p.account_id,
    p.instrument_id,
    i.symbol,
    i.name AS instrument_name,
    i.kind AS instrument_kind,
    i.currency,
    p.quantity,
    pr.price,
    CASE WHEN pr.price IS NULL THEN NULL
         ELSE CAST(ROUND(p.quantity * (pr.price / 100000000.0)) AS INTEGER) END AS value
FROM v_positions p
JOIN instruments i ON i.id = p.instrument_id
LEFT JOIN prices pr ON pr.instrument_id = p.instrument_id AND pr.as_of = (
    SELECT MAX(p2.as_of) FROM prices p2
    WHERE p2.instrument_id = p.instrument_id AND p2.as_of <= p.as_of)
WHERE p.quantity <> 0;

-- v_balances is the balance of every account on every event date.
--
-- The balance is built from a snapshot anchor plus the postings recorded after
-- it. That single rule covers all three tracking modes and, more importantly,
-- degrades gracefully: an account with only snapshots works, an account with
-- only postings works, and an account with both is anchored to the most recent
-- assertion so old transaction gaps cannot corrupt today's figure.
CREATE VIEW v_balances AS
SELECT
    e.d          AS as_of,
    a.id         AS account_id,
    a.name       AS account,
    a.kind,
    a.currency,
    a.in_networth,
    a.tracking,
    a.institution,
    a.tags,
    sn.as_of     AS anchor_date,
    sn.confidence AS anchor_confidence,
    CASE WHEN a.tracking = 'units' THEN (
            SELECT SUM(h.value) FROM v_holdings h
            WHERE h.account_id = a.id AND h.as_of = e.d)
    ELSE
        COALESCE(sn.balance, 0) + COALESCE((
            SELECT SUM(p.amount) FROM postings p
            JOIN txns t ON t.id = p.txn_id
            WHERE p.account_id = a.id
              AND t.scenario_id = 0
              AND t.date <= e.d
              AND t.date > COALESCE(sn.as_of, '')), 0)
    END AS balance
FROM v_event_dates e
CROSS JOIN accounts a
LEFT JOIN snapshots sn ON sn.id = (
    SELECT s.id FROM snapshots s
    WHERE s.account_id = a.id AND s.scenario_id = 0 AND s.as_of <= e.d
    ORDER BY s.as_of DESC, s.recorded_at DESC LIMIT 1)
WHERE (a.opened_on IS NULL OR a.opened_on <= e.d)
  AND (a.closed_on IS NULL OR a.closed_on >= e.d);

-- v_balances_base adds the base-currency value of each balance.
--
-- When an account is already in the base currency the rate is exactly 1e8 and
-- the integer amount passes through untouched, so the common case stays exact.
-- A NULL rate yields a NULL balance rather than a zero: a missing exchange rate
-- must show up as "unknown", never quietly shrink net worth.
CREATE VIEW v_balances_base AS
SELECT
    b.*,
    f.rate,
    CASE
        WHEN b.balance IS NULL THEN NULL
        WHEN f.rate = 100000000 THEN b.balance
        WHEN f.rate IS NULL THEN NULL
        ELSE CAST(ROUND(b.balance * (f.rate / 100000000.0)) AS INTEGER)
    END AS balance_base
FROM v_balances b
LEFT JOIN v_fx f ON f.as_of = b.as_of AND f.currency = b.currency;

-- v_networth is the headline series: one row per event date.
--
-- unknown_accounts is carried alongside the total on purpose. SUM() skips NULLs,
-- so an account with a missing price or exchange rate would otherwise vanish
-- from net worth without a trace. Any consumer that ignores this column is
-- reporting a number it has not earned.
CREATE VIEW v_networth AS
SELECT
    as_of,
    SUM(CASE WHEN in_networth = 1 THEN balance_base END)                        AS networth,
    SUM(CASE WHEN in_networth = 1 AND kind = 'asset'     THEN balance_base END) AS assets,
    SUM(CASE WHEN in_networth = 1 AND kind = 'liability' THEN balance_base END) AS liabilities,
    COUNT(*) FILTER (WHERE in_networth = 1 AND balance_base IS NULL)            AS unknown_accounts,
    COUNT(*) FILTER (WHERE in_networth = 1)                                     AS account_count
FROM v_balances_base
GROUP BY as_of;

-- v_networth_daily forward-fills the headline series onto every calendar day,
-- for charts and for joining against any other daily series.
CREATE VIEW v_networth_daily AS
SELECT
    c.d AS as_of,
    (SELECT n.networth FROM v_networth n WHERE n.as_of <= c.d ORDER BY n.as_of DESC LIMIT 1) AS networth
FROM v_calendar c;

CREATE VIEW v_balances_daily AS
SELECT
    c.d AS as_of,
    a.id AS account_id,
    a.name AS account,
    (SELECT b.balance_base FROM v_balances_base b
      WHERE b.account_id = a.id AND b.as_of <= c.d
      ORDER BY b.as_of DESC LIMIT 1) AS balance_base
FROM v_calendar c
CROSS JOIN accounts a;

-- v_allocation slices the latest balances by any tag, which is how a new way of
-- looking at the portfolio becomes a query rather than a schema change.
CREATE VIEW v_allocation AS
SELECT
    b.as_of,
    b.account_id,
    b.account,
    b.currency,
    b.balance_base,
    json_extract(b.tags, '$.asset_class') AS asset_class,
    json_extract(b.tags, '$.liquidity')   AS liquidity,
    json_extract(b.tags, '$.owner')       AS owner,
    json_extract(b.tags, '$.goal')        AS goal,
    json_extract(b.tags, '$.risk')        AS risk
FROM v_balances_base b
WHERE b.in_networth = 1;

-- v_flows is the contribution/withdrawal series, and the reason the boundary
-- flag exists.
--
-- The net effect of a transaction on net worth is the sum of its postings to
-- accounts inside the boundary. Move money from a bank to a brokerage and both
-- legs are inside, so it nets to zero and is correctly *not* a contribution.
-- Salary arrives from outside and nets positive; spending nets negative. No
-- classification by hand, no way to get it wrong.
CREATE VIEW v_flows AS
SELECT
    t.id   AS txn_id,
    t.date AS as_of,
    t.description,
    SUM(CASE WHEN a.in_networth = 1 THEN
        CASE
            WHEN f.rate = 100000000 THEN p.amount
            WHEN f.rate IS NULL THEN NULL
            ELSE CAST(ROUND(p.amount * (f.rate / 100000000.0)) AS INTEGER)
        END
    ELSE 0 END) AS net_flow
FROM txns t
JOIN postings p ON p.txn_id = t.id
JOIN accounts a ON a.id = p.account_id
LEFT JOIN v_fx f ON f.as_of = t.date AND f.currency = p.currency
WHERE t.scenario_id = 0
GROUP BY t.id, t.date, t.description
HAVING net_flow IS NULL OR net_flow <> 0;

-- v_account_flows is the same idea one level down: money entering or leaving a
-- single account, including transfers from other accounts you own. This is the
-- cashflow series for a per-account return.
CREATE VIEW v_account_flows AS
SELECT
    t.date AS as_of,
    p.account_id,
    a.name AS account,
    SUM(p.amount) AS flow,
    SUM(CASE WHEN p.flow_kind = 'contribution' THEN p.amount ELSE 0 END) AS contributions,
    SUM(CASE WHEN p.flow_kind = 'withdrawal'   THEN p.amount ELSE 0 END) AS withdrawals
FROM txns t
JOIN postings p ON p.txn_id = t.id
JOIN accounts a ON a.id = p.account_id
WHERE t.scenario_id = 0
GROUP BY t.date, p.account_id, a.name
HAVING SUM(p.amount) <> 0;

-- Valuation dates are the dates on which the portfolio was actually measured.
-- Returns can only be split into sub-periods at these points, and how many there
-- are is a hard limit on the precision of any time-weighted figure - which is
-- why reports state the count rather than implying more accuracy than exists.
CREATE VIEW v_valuation_dates AS
SELECT DISTINCT as_of AS d FROM snapshots WHERE scenario_id = 0;

CREATE VIEW v_periods AS
SELECT d AS start_date, LEAD(d) OVER (ORDER BY d) AS end_date
FROM v_valuation_dates;

-- v_attribution is the answer to the question a balance-only spreadsheet cannot
-- answer: of the change in net worth over this period, how much did I put in and
-- how much did the market give me?
--
--   change = net contribution + market return
--
-- Because contributions are derived structurally from the account boundary, this
-- identity holds by construction rather than by bookkeeping discipline.
CREATE VIEW v_attribution AS
SELECT
    p.start_date,
    p.end_date,
    n0.networth AS start_value,
    n1.networth AS end_value,
    n1.networth - n0.networth AS change,
    COALESCE((SELECT SUM(fl.net_flow) FROM v_flows fl
               WHERE fl.as_of > p.start_date AND fl.as_of <= p.end_date), 0) AS net_contribution,
    n1.networth - n0.networth
        - COALESCE((SELECT SUM(fl.net_flow) FROM v_flows fl
                     WHERE fl.as_of > p.start_date AND fl.as_of <= p.end_date), 0) AS market_return
FROM v_periods p
JOIN v_networth n0 ON n0.as_of = p.start_date
JOIN v_networth n1 ON n1.as_of = p.end_date
WHERE p.end_date IS NOT NULL;

-- v_attribution_quality measures how much of the attribution above can be
-- trusted.
--
-- If an account's balance moved but no flow was recorded against it, the
-- difference lands in "market return" whether or not it was really a deposit.
-- That is the single most dangerous silent error in this whole model, so the
-- share of the portfolio sitting in such accounts is computed explicitly and
-- reported next to the number it undermines.
CREATE VIEW v_attribution_quality AS
SELECT
    p.start_date,
    p.end_date,
    SUM(ABS(COALESCE(b1.balance_base, 0))) AS covered_value,
    SUM(CASE WHEN NOT EXISTS (
            SELECT 1 FROM v_account_flows af
            WHERE af.account_id = b1.account_id
              AND af.as_of > p.start_date AND af.as_of <= p.end_date)
         AND COALESCE(b1.balance_base, 0) <> COALESCE(b0.balance_base, 0)
        THEN ABS(COALESCE(b1.balance_base, 0)) ELSE 0 END) AS unexplained_value
FROM v_periods p
JOIN v_balances_base b1 ON b1.as_of = p.end_date AND b1.in_networth = 1
LEFT JOIN v_balances_base b0 ON b0.as_of = p.start_date AND b0.account_id = b1.account_id
WHERE p.end_date IS NOT NULL
GROUP BY p.start_date, p.end_date;

-- Checks. These exist because the database is meant to be edited directly, in
-- this application or in any SQL client, so its invariants cannot be assumed to
-- hold. They report rather than block: a violation should be loud and fixable,
-- never a reason a legitimate edit fails half-way through.

CREATE VIEW v_check_unbalanced AS
SELECT t.id AS txn_id, t.date, t.description, p.currency, SUM(p.amount) AS imbalance
FROM txns t JOIN postings p ON p.txn_id = t.id
GROUP BY t.id, p.currency
HAVING SUM(p.amount) <> 0;

CREATE VIEW v_check_missing_fx AS
SELECT DISTINCT f.as_of, f.currency
FROM v_fx f
WHERE f.rate IS NULL
  AND f.currency IN (SELECT currency FROM accounts WHERE in_networth = 1);

-- For a transaction-tracked account a snapshot is a claim that can be checked
-- against the postings. Disagreement means a missing or duplicated transaction.
CREATE VIEW v_check_reconcile AS
SELECT
    s.account_id,
    a.name AS account,
    s.as_of,
    s.balance AS asserted,
    (SELECT COALESCE(SUM(p.amount), 0) FROM postings p
      JOIN txns t ON t.id = p.txn_id
      WHERE p.account_id = s.account_id AND t.scenario_id = 0 AND t.date <= s.as_of) AS derived,
    s.balance - (SELECT COALESCE(SUM(p.amount), 0) FROM postings p
      JOIN txns t ON t.id = p.txn_id
      WHERE p.account_id = s.account_id AND t.scenario_id = 0 AND t.date <= s.as_of) AS difference
FROM snapshots s
JOIN accounts a ON a.id = s.account_id
WHERE s.scenario_id = 0 AND a.tracking = 'transactions'
  AND s.balance <> (SELECT COALESCE(SUM(p.amount), 0) FROM postings p
      JOIN txns t ON t.id = p.txn_id
      WHERE p.account_id = s.account_id AND t.scenario_id = 0 AND t.date <= s.as_of);

-- An account whose last valuation is far behind the rest of the portfolio drags
-- an unknown amount of staleness into the headline figure.
CREATE VIEW v_check_stale AS
SELECT
    a.id AS account_id,
    a.name AS account,
    (SELECT MAX(s.as_of) FROM snapshots s WHERE s.account_id = a.id AND s.scenario_id = 0) AS last_valued,
    (SELECT MAX(d) FROM v_event_dates) AS latest_data,
    CAST(julianday((SELECT MAX(d) FROM v_event_dates))
       - julianday((SELECT MAX(s.as_of) FROM snapshots s WHERE s.account_id = a.id AND s.scenario_id = 0))
        AS INTEGER) AS days_stale
FROM accounts a
WHERE a.closed_on IS NULL AND a.in_networth = 1 AND a.tracking <> 'transactions';

-- v_quality is the one view a report should consult before printing a number.
CREATE VIEW v_quality AS
SELECT 'unbalanced' AS check_name, 'error' AS severity,
       'transaction ' || txn_id || ' (' || date || ') does not balance in ' || currency AS detail
FROM v_check_unbalanced
UNION ALL
SELECT 'missing_fx', 'error',
       'no exchange rate for ' || currency || ' on ' || as_of
FROM v_check_missing_fx
UNION ALL
SELECT 'unreconciled', 'error',
       account || ' asserted balance on ' || as_of || ' disagrees with its transactions'
FROM v_check_reconcile
UNION ALL
SELECT 'stale', 'warning',
       account || ' last valued ' || COALESCE(last_valued, 'never') ||
       COALESCE(' (' || days_stale || ' days before the latest data)', '')
FROM v_check_stale
WHERE last_valued IS NULL OR days_stale > 120
UNION ALL
SELECT 'rough_valuation', 'warning',
       account || ' balance on ' || as_of || ' is a ' || anchor_confidence || ', not a statement'
FROM v_balances_base
WHERE in_networth = 1 AND anchor_confidence IN ('estimate', 'rough')
  AND as_of = (SELECT MAX(d) FROM v_event_dates);

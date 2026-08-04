-- Built-in saved queries: named formulas, reinstalled on every open and stored
-- in the database so they are available to any SQL client, not only this
-- application. Users may add their own with `networth query save`; those are
-- never touched here.
--
-- Format: each query begins with "-- name:" and optionally "-- desc:".

-- name: networth
-- desc: Net worth at every date on which anything changed.
SELECT as_of, networth, assets, liabilities, unknown_accounts
FROM v_networth
ORDER BY as_of;

-- name: balances
-- desc: Latest balance of every account, largest first.
SELECT account, kind, currency, balance, balance_base, anchor_date, anchor_confidence
FROM v_balances_base
WHERE as_of = (SELECT MAX(d) FROM v_event_dates)
ORDER BY balance_base DESC;

-- name: attribution
-- desc: Change in net worth split into what you added and what the market gave.
SELECT start_date, end_date, start_value, end_value, change, net_contribution, market_return
FROM v_attribution
ORDER BY start_date;

-- name: allocation
-- desc: Latest portfolio split by asset class.
SELECT COALESCE(asset_class, 'untagged') AS asset_class,
       SUM(balance_base) AS value,
       ROUND(100.0 * SUM(balance_base) / (SELECT SUM(balance_base) FROM v_allocation
             WHERE as_of = (SELECT MAX(d) FROM v_event_dates)), 1) AS pct
FROM v_allocation
WHERE as_of = (SELECT MAX(d) FROM v_event_dates)
GROUP BY 1
ORDER BY value DESC;

-- name: flows
-- desc: Every movement of money across the net worth boundary.
SELECT as_of, description, net_flow
FROM v_flows
ORDER BY as_of DESC;

-- name: yearly
-- desc: Net worth at each year end, with the change over the year.
SELECT yr.as_of, n.networth,
       n.networth - LAG(n.networth) OVER (ORDER BY yr.as_of) AS change
FROM (
    SELECT strftime('%Y', d) AS y, MAX(d) AS as_of FROM v_calendar GROUP BY y
) yr
JOIN v_networth_daily n ON n.as_of = yr.as_of
ORDER BY yr.as_of;

-- name: stale
-- desc: Accounts whose valuation is furthest out of date.
SELECT account, last_valued, days_stale
FROM v_check_stale
WHERE last_valued IS NULL OR days_stale > 0
ORDER BY days_stale DESC NULLS FIRST;

-- name: quality
-- desc: Everything that makes a reported number less trustworthy than it looks.
SELECT severity, check_name, detail FROM v_quality ORDER BY severity, check_name;

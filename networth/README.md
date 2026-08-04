# networth

Track net worth over time in one SQLite file, and tell **the money you added**
apart from **the money your investments earned**.

That distinction is the reason this exists. A spreadsheet of balances can show
that net worth went up ₹14 lakh; it cannot show that ₹7 lakh of it was salary you
saved. So its XIRR quietly rewards you for depositing money, and there is no way
to tell a good year from a generous one.

```
  Net worth                                                as of 2026-01-01
  ₹1,43,10,000   ▁▁▁▂▃▃▄▄▅▆▆▇█
  assets ₹1.86 Cr   liabilities ₹-43.3 L

  Since 2025-07-01  ·  6 months
    You put in                    +₹6,85,000
    Markets gave                  +₹7,55,000   what you actually earned
    ────────────────────────────────────────
    Change                       +₹14,40,000
    48% of the change was money you added, not growth.

  Annualised return
    Money-weighted                     11.7%   your money's return, timing included
    Time-weighted                      10.4%   the portfolio's own return
    Difference                         +1.3%   contribution timing helped
    from 7 valuations
```

Run `networth` with no arguments and it opens there.

It replaces two spreadsheets, not one. The **monthly** sheet works out where
income goes; the **twice-yearly** one records balances. Keeping both in one place
is what makes the numbers above trustworthy: routing income monthly means
contributions are a *record* rather than a recollection, and twelve known
cashflow dates a year instead of two sharpens every return figure that rests on
them.

> Not financial advice. Every figure is a draft to check, and the audit trail
> exists so it can be checked.

## Install

```sh
cd networth
go build -o networth ./cmd/networth
```

## Use it

```sh
networth                       # the interface — this is the normal way to use it
networth --db ~/mine.db        # or point it at any file; created if missing
networth plan --income 300000  # where should this month's money go?
networth plan --rules          # the standing routing rules
networth report                # a summary on stdout
networth report --format md    # or markdown, csv, json
networth query attribution     # run a saved query
networth query 'SELECT * FROM v_networth'
networth check                 # anything that makes the numbers less trustworthy
networth doctor                # the health of the file itself
networth backup                # a safe, atomic copy
```

The database is found in this order: `--db`, then `$NETWORTH_DB`, then
`./networth.db` if it exists, then `~/.local/share/networth/networth.db`.

### The monthly routine

Press `2`, type what came in, and the routing appears as you type:

```
  Monthly plan                                                    2026-08-01

  Income        300000
                = ₹3,00,000

  Move                          To                              Amount
▸ Top up spending               Spending Account             ₹1,31,000  set aside
  Tax FD                        Tax FD                         ₹50,000
  Domestic Equity               Domestic Equity                ₹59,500
  Global Equity                 Global Equity                  ₹23,800
  Debt Fund                     Debt Fund                      ₹17,850
  Arbitrage                     Arbitrage                      ₹17,850

  Set aside               ₹1,31,000
  To invest               ₹1,69,000
  56% of income routed to investments
  net worth this month +₹3,00,000   nothing left your accounts yet
```

`ctrl+s` records it. `ctrl+z` undoes a month. `enter` on any line overrides it
just for this month — which is how "no PPF this month" works without touching the
rule.

#### Getting around

One rule: **while something is open — a form, a list, a field — the screen owns
the keyboard**, and the plain keys mean what the footer says they mean. `tab` and
`shift+tab` move between fields, not between screens, and `esc` backs out one
step. The tab bar dims and its numbers disappear while that is the case, so it is
visible rather than something to discover by pressing a key and having it
ignored.

`ctrl+←` and `ctrl+→` change screen from anywhere, including mid-form, and
`ctrl+c` always quits. When nothing is open, `1`–`6` jump straight to a screen,
`tab`/`shift+tab` cycle, `r` reloads and `q` quits.

Nothing that discards work happens on one keystroke: `esc` on a half-finished
balance round asks first.

#### Filling in forms

Fields only accept what is actually valid. A fixed set of values is a choice you
cycle with `←`/`→` or space, with every option on screen; a reference to
something that already exists — the destination account, a currency, a tag —
completes as you type, with `↑`/`↓` to pick and `tab` to accept. Matching is on
any part of the name, so `card` finds `HDFC Card`.

A rule's destination is strict: it has to be an account that exists, because one
pointing at a name that does not would fail silently every month.

#### Rules

Rules run in **priority order**, in two stages. *Set-asides* come off income
first; whatever survives is what there is to invest, and the *invest* rules
divide it. Because they run in order, a rule sees what the earlier ones left — so
"50,000 to the tax deposit, then split the rest" works as written.

One field says how much, and it takes whichever form fits:

| You write | It means |
|---|---|
| `50000` | a fixed amount |
| `40%` | 40% of what is still unallocated |
| `40% of income` | 40% of gross income |
| `40% of investable` | 40% of income less the set-asides |
| `remainder` | whatever the other rules leave |
| anything else | a formula |

Formulas exist because real routing refers to *other accounts*. If income lands
in one bank account but you spend from another, the transfer has to cover next
month's spending plus what is owed on the cards, less whatever is already sitting
there:

```
100000 + owed("tag:type=credit_card") - balance("Spending Account")
```

Selecting by tag rather than by name means a new credit card is covered the
moment you tag it — so tag each card `type=credit_card` on the Accounts screen
(they are accounts of kind `liability`), and the rule above needs no editing when
you get another one. Available to a formula:

- `income`, `setaside`, `investable`, `allocated`, `remaining`
- `balance(sel…)` signed, `owed(sel…)` positive for debts, `line("rule name")`
- `min`, `max`, `abs`, `round`, `floor`, `ceil`, `clamp`
- selectors: an account name, `tag:key=value`, `kind:liability`, or `*`

The editor shows what a formula comes to *right now*, so a mistake surfaces while
you type it rather than the month it silently routes nothing.

#### What executing a plan records

The money is written the way it actually moves:

```
income        Income  →  the account it is paid into    (enters net worth)
set-asides    that account  →  wherever they point      (spending, if outside)
investments   that account  →  the target account       (stays inside; nets to zero)
```

So the month's contribution is income less what genuinely left, per-account flows
are right for a per-account return, and none of it had to be classified by hand.

**The target account's side of the net worth boundary decides the meaning.**
Topping up a spending account you own is a *transfer* — the money is still yours
until it is spent, so net worth is unchanged. Pointing a set-aside at an expense
account outside net worth records it as spending. Both are legitimate; the screen
shows which you chose and what it does to net worth before you commit.

### The twice-a-year routine

Press `2`, and for each account type the balance and, optionally, how much you
paid in since last time:

```
  Update balances                                       1 of 6  ·  as of 2026-08-04

  EPF   EPFO · asset · INR
  last ₹17,10,000 on 2026-01-01 (7 months ago)

▸ Balance         1800000+50000
                  = ₹18,50,000   +₹1,40,000  (+8.2%)

  of which added  0
                  money you paid in or took out since then — leave blank if none
```

Every amount field takes arithmetic — `450000+62000`, `1200*12`, `(a+b)/2`, `8%`
— so you can type what you actually know instead of reaching for a calculator.
Commas, spaces and `₹` are ignored, and `(1000)` means `-1000`.

A blank balance leaves an account unchanged. A debt typed as a positive number is
read as the amount owed. Before anything is written, a review screen shows the
whole round and the split it produces.

That second field is the entire trick. It is optional on every account, but each
one you fill sharpens the contribution-versus-returns split. If you have been
planning monthly, the contributions are already on record and the screen says so,
so you can leave it blank rather than counting the same money twice.

## Why a database instead of a spreadsheet

**Facts go in tables; formulas are views.** A spreadsheet mixes both in the same
cell, so changing a formula changes your data. Here the facts are append-only and
every derived figure — `v_networth`, `v_flows`, `v_attribution` — is a view,
reinstalled from `internal/db/sql/views.sql` each time the file is opened. A
formula can be rewritten freely; history cannot drift.

**The file is the whole application.** Your data, your settings and the
interface's own state all live in it. Copy it to move everything. Open someone
else's and it works, with no configuration. There is nothing on the side.

**Any question stays answerable.** Press `5` for the query screen, or open the
same file in `sqlite3`, Datasette or a GUI — even while the app is running. The
built-in queries double as worked examples.

```sh
sqlite3 ~/.local/share/networth/networth.db \
  'SELECT * FROM v_attribution ORDER BY start_date'
```

## The data model

Six ideas, and the first one carries most of the weight.

**`accounts.in_networth` is the boundary.** Everything else follows from it
structurally, with nothing to classify by hand:

- move money bank → fund and both legs are inside, so it nets to zero and is
  correctly *not* a contribution
- salary arrives from outside, so it is
- an EMI splits itself: principal is internal (net worth unchanged), interest
  crosses outward

Getting this wrong is the classic spreadsheet error — a transfer inflating the
receiving account's return. Here it is impossible.

**`txns` + `postings`** are double entry, kept light. Postings must sum to zero
per currency. You never have to write both legs by hand: recording a contribution
posts the other side to an `External` account automatically.

**`snapshots`** are balance assertions with `method` and `confidence`. For a
PPF they *are* the data; for a tracked bank account they are a reconciliation
check that catches entry errors a spreadsheet absorbs silently. A property valued
off a broker's guess is not the same kind of fact as a fund NAV, and reports say
so instead of laundering it into a precise-looking number.

**`instruments` + `prices` + `trades`** separate quantity from valuation, which
is what turns a twice-a-year habit into a daily series: units change rarely, and
prices can be fetched. An FX rate is just the price of a currency, so
multi-currency reporting — and inflation-adjusted returns, using CPI as another
series — is the same query with a different instrument.

**`tags`** is a JSON object on accounts and instruments: `asset_class`,
`liquidity`, `goal`, `owner`, `jurisdiction`, anything. This is the escape valve
that makes a new way of slicing the portfolio a `WHERE` clause instead of a
schema migration.

**`allocation_rules`, `plans` and `plan_lines`** hold the monthly routing. Rules
are the reusable template; a plan is one month's application of them to real
income. A plan's lines are stored rather than recomputed, so a month executed
under one set of rules is not silently rewritten when the rules change later —
history has to stay what actually happened.

**`scenarios`** keep what-ifs out of the facts. Scenario 0 is reality and every
view filters to it, so a projection can never contaminate history.

Every fact carries both `as_of` (when it was true) and `recorded_at` (when you
learned it). Cheap now, impossible to retrofit, and it handles EPF's reporting
lag and honest corrections.

## How the returns are computed

Two measures, always shown together, because either alone misleads.

**Money-weighted (XIRR)** is the return on the money actually invested. It is
what your balance did, but it moves when contributions are well or badly timed,
so it is not a verdict on the investments.

**Time-weighted (TWR)** removes the size and timing of contributions, so it
measures the portfolio itself. It is what a fund quotes.

The gap between them *is* the contribution-timing effect — the number this tool
was built to expose.

Sub-period returns use **Modified Dietz**, weighting each flow by the fraction of
the period it was invested. A true TWR needs a valuation on every flow date; with
balances updated twice a year that data does not exist, and pretending otherwise
would invent precision. When the exact date of a contribution is unknown it is
dated to the **midpoint** of the period — assuming either end would bias every
return in a consistent direction.

## Honesty

A number that rests on thin data says so, next to itself:

- an account with a missing price or exchange rate makes net worth **unknown**,
  never quietly smaller — `SUM()` skips NULLs, so `v_networth.unknown_accounts`
  is carried everywhere the total is
- balances that moved with **no contribution recorded** land in "markets gave"
  whether earned or deposited, so the share of the portfolio in that state is
  measured and reported next to the figure it undermines
- the **number of valuations** is printed beside every time-weighted return
- `networth check` reports unbalanced transactions, failed reconciliations,
  missing rates and stale valuations, and exits non-zero on real errors

## Editing the database directly

Expected, not merely tolerated. `STRICT` tables, foreign keys, `CHECK`
constraints and triggers enforce what SQLite can; the rest is in `networth check`,
which reports rather than blocks — a violation should be loud and fixable, never
a reason a legitimate edit fails half-way through. An `audit_log` records changes
so there is a way back from a bad `UPDATE`.

Balance-must-be-zero is deliberately *not* a trigger: SQLite has no deferred
constraints, so it would reject the first posting of every balanced pair and make
hand-editing painful. It lives in `v_check_unbalanced` instead.

## Money is exact

Amounts and quantities are `INTEGER`s scaled by 1e8. They are exact, they sort,
and `SUM()` works in ad-hoc SQL. `REAL` would silently break exactness; a decimal
string could not be summed. int64 at this scale spans about ±9.2e10 units — ample
for a net worth in rupees with eight decimal places left for fund units. The scale
is recorded in `app_meta`, so the file is interpretable without this code.

FX conversion is the one place exactness is knowingly given up, and only for
non-base currencies: a market rate is an observation with its own error, far
larger than 1e-8.

## Backups

The file is the only source of truth, so there is no plain-text fallback:

```sh
networth backup            # atomic VACUUM INTO, keeps the 10 most recent
```

Backups live in `backups/` next to the database. Both are gitignored — the file
holds a complete net worth and every account balance.

## Dependencies

This module uses `modernc.org/sqlite` (pure Go, no cgo) and Bubble Tea, a
deliberate exception to the workspace's stdlib-only convention, scoped to this
module. Driving the only copy of your data through `os/exec` quoting was not an
acceptable trade.

## Layout

```
cmd/networth        CLI and interface entry point
internal/money      exact scaled-integer amounts, parsing and formatting
internal/db         open, migrate, install the view layer, checks, backups
internal/db/sql     schema migrations, views.sql, queries.sql (embedded)
internal/store      typed accounts, snapshots, flows, summaries
internal/plan       the monthly allocation waterfall
internal/expr       the formula language rules are written in
internal/analysis   XIRR, Modified Dietz, chained TWR
internal/report     text, markdown, csv and json output
internal/tui        the interface
```

```sh
go test ./...          # or -race, which is what CI runs
go vet ./... && gofmt -l .
```

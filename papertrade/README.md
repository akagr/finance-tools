# papertrade

Runs a trading strategy **forward in time** on real market data with **simulated fills**, so you
can validate a strategy's live decision-making and your data plumbing *before* risking any
capital. It is the bridge between an offline backtest (see [`../backtest`](../backtest)) and real
trading: same strategies, same cost model, but driven one bar at a time by freshly-fetched prices,
with state that persists between runs.

Pure Go standard library, no external dependencies. Prices come from Yahoo Finance (key-less).

> **Paper trading only — no real orders are placed, ever.** This is a research tool, not a
> trading system, and it is **not investment advice**. It exists to catch problems (bad data,
> a strategy that behaves differently live than in backtest, cost drag) cheaply, on a laptop.

## How it works

You create a paper **account** in a directory, then run `step` once per trading day (by hand or
from cron). Each step:

1. Fetches recent daily closes for the account's symbol (enough history to warm up indicators).
2. Runs the strategy over that history to get a target weight (0–1), exactly as the backtester
   would — no lookahead.
3. Rebalances the paper portfolio toward that weight through a **paper broker** that charges the
   same brokerage/STT/slippage costs as the backtester, and can only spend cash it has.
4. Persists the new state and appends the fill to an auditable log.

Running `step` again on the same bar is a no-op (idempotent), so a daily cron job is safe.

## Quickstart

```sh
cd papertrade

# 1. Create a paper account: trade an SMA 50/200 crossover on the Nifty 50 with ₹1,00,000.
go run ./cmd/papertrade init --dir ./accounts/nifty-sma \
  --symbol NIFTY50 --yahoo '^NSEI' --strategy sma-cross --fast 50 --slow 200 --capital 100000

# 2. Advance it using the latest data (run this once per trading day).
go run ./cmd/papertrade step --dir ./accounts/nifty-sma

# 3. See where the account stands (marked to the latest live quote).
go run ./cmd/papertrade status --dir ./accounts/nifty-sma

# 4. After it's run for a while, review performance vs buy-and-hold.
go run ./cmd/papertrade summary --dir ./accounts/nifty-sma

# 5. Review every simulated fill.
go run ./cmd/papertrade history --dir ./accounts/nifty-sma
```

To run it forward automatically, add a weekday cron entry, e.g.:

```cron
30 16 * * 1-5  cd /path/to/papertrade && go run ./cmd/papertrade step --dir ./accounts/nifty-sma >> step.log 2>&1
```

## Commands

```
papertrade init  --dir <dir> --symbol <label> --yahoo <sym> [--strategy <name>] [strategy flags] [--capital N]
papertrade step  --dir <dir> [--force]
papertrade status --dir <dir>
papertrade summary --dir <dir>
papertrade history --dir <dir>
papertrade version
```

- **init** — create an account. `--symbol` is a label (e.g. `NIFTY50`); `--yahoo` is the Yahoo
  ticker actually fetched (e.g. `^NSEI`, or `NIFTYBEES.NS` for the ETF). Strategy and cost flags
  match the backtester (`--strategy`, `--fast/--slow`, `--lookback`, `--rsi-period/-threshold`,
  `--entry/--exit`, `--vol-target/-lookback`, `--brokerage-bps/--stt-bps/--slippage-bps`).
- **step** — fetch the latest data and act on the newest unprocessed bar. `--force` re-acts on a
  bar already processed (useful for testing). Every processed bar records a marked-to-market
  equity snapshot, so the equity curve is complete even on days with no trade.
- **status** — cash, position, average cost, realised P&L, and — when a live quote is available —
  marked-to-market equity, unrealised P&L and total return *right now*.
- **summary** — performance over the whole tracked period from the equity log: total return,
  CAGR, annualised vol, Sharpe and max drawdown, next to a **buy-and-hold benchmark** over the
  same dates and the edge against it. Needs at least two days of history.
- **history** — the full fills log (date, side, shares, price, cost, equity after).

Each account is a directory holding `account.json` (state), `fills.jsonl` (every trade) and
`equity.jsonl` (a daily marked-to-market snapshot). **Keep account directories out of version
control** — they are your data, not code.

## Relationship to `backtest`

The paper broker uses the same cost model and the strategies are the same rules, so a paper run
should track a backtest of the same strategy closely — divergence is a useful signal that
something (data timing, costs, a live-vs-historical quirk) needs attention. Prove an edge in
`backtest` (and stress-test it with walk-forward, sweeps and Monte-Carlo) *before* paper-trading
it here, and only consider real capital after weeks of clean paper results.

## Method & caveats

- **Daily close-to-close.** A step acts on the latest daily close; it is meant to be run once a
  day. Intraday or real-time execution would need a broker/market-data API (a Phase-4 concern).
- **Simulated fills.** Orders fill immediately at the quoted close plus a slippage penalty, with
  brokerage + STT charged on notional. Real fills differ; treat costs as optimistic.
- **Long-only, no leverage, one asset.** Mirrors the backtester. Buys are capped at available
  cash net of costs, so the account never goes into margin.
- **Money is `float64`** — like `backtest`/`correlation`, this is research/simulation, not the
  exact-money tax path used by `schedule-fa`.
- **Yahoo data is unofficial** and occasionally revises or gaps; it is fine for research, not for
  anything you'd trade real money on without a proper feed.

## Development

```sh
cd papertrade
go test ./...                 # all tests
go test -race ./...           # what CI runs
go vet ./... && gofmt -l .    # gofmt must print nothing
```

The `internal/strategy` and `internal/yahoo` packages are copies of `backtest`'s (the repo keeps
each tool self-contained); keep them in sync when strategies change.

> **Disclaimer:** Paper trading only, no real orders. Not investment advice.

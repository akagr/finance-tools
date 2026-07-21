# backtest

A tiny, offline **backtester** for rule-based strategies on Indian (NSE) daily data. It runs a
strategy *and* a buy-and-hold benchmark over the same price history, charges realistic costs,
and reports whether the strategy actually earned its complexity.

Pure Go standard library, no external dependencies. Data is fetched once from Yahoo Finance
(key-less) into a CSV; everything after that is offline and reproducible.

> **This is a research tool, not a trading system.** A backtest is a *hypothesis fit to the
> past* — it ignores regime change, survivorship, and execution reality, and it flatters
> strategies that overfit. Output is **not investment advice**. Prove an edge here, paper-trade
> it, and only then consider tiny real capital.

## Quickstart

```sh
cd backtest

# 1. Fetch daily closes for the symbols in data/tickers.txt (Yahoo Finance).
go run ./cmd/backtest fetch prices --start 2019-01-01 --end 2024-12-31 > data/nifty.csv

# 2. Backtest a 50/200 SMA crossover on the Nifty 50, vs buy-and-hold.
go run ./cmd/backtest run --prices data/nifty.csv --symbol NIFTY50 \
  --strategy sma-cross --fast 50 --slow 200 --capital 10000

# ...or run every strategy at once and compare them in one sorted table.
go run ./cmd/backtest run --prices data/nifty.csv --symbol NIFTY50 --strategy all --capital 10000
```

Expect most simple rules to **lose to buy-and-hold after costs** — discovering that cheaply is
the entire point.

## Strategies

Every run pits the chosen strategy against a **buy-and-hold** benchmark. Each lives in its own
file under `internal/strategy/`. Pass `--strategy all` to run **every** strategy at once and
compare them in a single table sorted by total return (best first), with the benchmark ranked
in so it is obvious which strategies actually beat it.

| Name        | Style          | Rule                                                                   | Flags                          |
|-------------|----------------|------------------------------------------------------------------------|--------------------------------|
| `sma-cross` | trend (default)| long while the fast **simple** MA is above the slow MA, else cash      | `--fast` `--slow`              |
| `ema-cross` | trend          | same, with **exponential** MAs (reacts sooner, more whipsaws)          | `--fast` `--slow`              |
| `momentum`  | trend          | long while price is above its own level `--lookback` bars ago          | `--lookback`                   |
| `rsi`       | mean-reversion | buy when oversold (RSI below `--rsi-threshold`), else cash             | `--rsi-period` `--rsi-threshold` |
| `donchian`  | breakout       | enter on a new `--entry`-bar high, exit on a new `--exit`-bar low       | `--entry` `--exit`             |
| `buy-hold`  | benchmark      | always fully invested                                                  | —                              |

The trend rules **buy strength**; `rsi` deliberately **buys weakness** — comparing them shows
how a strategy's style interacts with a market's character (e.g. mean-reversion tends to bleed
in a strong bull market).

Add your own by implementing `strategy.Strategy` — a `Target(closes) → weight` method — in a new
file under `internal/strategy/`, then register it in `pipeline.buildStrategy`. Target is called
once per bar in order; most rules are pure functions of the history passed, but a strategy may
carry state across calls for entry/exit hysteresis (see `donchian.go`). It must never read past
the slice it is given (no lookahead).

## Position sizing (volatility targeting)

By default a strategy is all-in or all-out (weight 0 or 1). Pass `--vol-target` to scale the
active strategies' positions so their trailing realised volatility approaches a target — e.g.
`--vol-target 10` aims for 10% annualised vol, trimming exposure when the asset is turbulent:

```sh
go run ./cmd/backtest run --prices data/nifty.csv --symbol NIFTY50 --strategy all --vol-target 10
```

It is an **overlay, not a signal**: the strategy still decides *whether* to be in the market;
sizing decides *how much*. It is **long-only and never levers up** (a retail cash account has no
margin), so it can only reduce exposure — smoothing the ride and cutting drawdowns, usually at
the cost of total return. The buy-and-hold benchmark is deliberately left **unscaled**, so the
comparison stays honest. Holding a fractional weight rebalances as prices drift; the engine's
rebalance band (1% of equity by default) keeps that churn to periodic, low-cost trades rather
than a trade every bar.

## Usage

```
backtest run --prices <csv> [flags]

  --prices         price CSV file (columns: date,symbol,close) (required)
  --symbol         which symbol in the CSV to test (default: first found)
  --strategy       all | sma-cross | ema-cross | momentum | rsi | donchian | buy-hold (default sma-cross)
  --fast           fast MA window, sma-cross/ema-cross (default 20)
  --slow           slow MA window, sma-cross/ema-cross (default 50)
  --lookback       lookback window in bars, momentum (default 120)
  --rsi-period     RSI period, rsi (default 14)
  --rsi-threshold  buy when RSI is below this, rsi (default 30)
  --entry          breakout entry window in bars, donchian (default 20)
  --exit           breakdown exit window in bars, donchian (default 10)
  --vol-target     annualised volatility target in %, e.g. 10; 0 disables sizing (default 0)
  --vol-lookback   trailing bars used to estimate realised volatility (default 20)
  --capital        initial capital in INR (default 100000)
  --brokerage-bps  brokerage per trade, basis points (default 0)
  --stt-bps        securities transaction tax per trade, basis points (default 10)
  --slippage-bps   assumed slippage per trade, basis points (default 5)
  --format         comma-separated: md,csv,json (default md)
  --sort           rank the table by: return | cagr | sharpe | sortino | calmar | drawdown (default return)
  --out            output directory (default: print to stdout)

backtest fetch prices --start <YYYY-MM-DD> --end <YYYY-MM-DD> [--tickers <file>]
```

## CSV format

Prices (long form; one file may hold many symbols):

```
date,symbol,close
2024-06-14,NIFTY50,23465.60
```

NSE cash symbols use a `.NS` Yahoo suffix (e.g. `NIFTYBEES.NS`); indices are prefixed with `^`
(e.g. `^NSEI` for the Nifty 50). Edit `data/tickers.txt` to change what `fetch` pulls.

## Method & caveats

- **Close-to-close, long/flat, fractional shares.** The signal for bar *i* is computed from
  closes up to and including *i* and executed at that same close. Real fills happen later and at
  a different price — `--slippage-bps` is the crude stand-in. Signals never see future bars
  (no lookahead).
- **Costs are charged on every trade's notional**: brokerage + STT + slippage, each in basis
  points. Defaults approximate NSE cash-delivery friction and are deliberately conservative —
  underestimating costs is how backtests lie. A rebalance band (1% of equity by default) stops a
  constant- or fractional-weight target from churning every bar to unwind price drift or its own
  fee drag.
- **Long/flat or fractional, long-only, no leverage.** Base strategies are all-in/all-out;
  `--vol-target` scales the position but never above 100%. No shorting, margin or intraday bars.
- **Metrics**: total return, CAGR (over the actual calendar span), annualised volatility,
  Sharpe and **Sortino** (252 trading days, zero risk-free rate; Sortino penalises only downside
  deviation), max drawdown and **Calmar** (CAGR ÷ max drawdown), plus trades, turnover and
  exposure. Rank the comparison table by any of these with `--sort` — e.g. `--sort sharpe` often
  promotes a lower-return but smoother strategy above buy-and-hold.
- **Money is `float64`**, not `math/big.Rat` — like the sibling `correlation` module, this is
  statistics rather than tax accounting, where a paisa of rounding is immaterial.
- **One asset, one series at a time.** No multi-asset portfolios or corporate-action adjustment
  yet — use adjusted-close symbols where possible.

## Roadmap

This backtester is **Phase 1** of a deliberately staged path from idea to (maybe) live capital.
Money is the *last* step, not the first: each phase must earn its way into the next, and most
ideas should die in Phase 1 or 2 — cheaply, on a laptop, instead of expensively, in the market.

**Phase 1 — Backtesting (here now).** Measure a rule's edge on history against a benchmark.
Delivered so far: multiple strategies with a one-shot `--strategy all` comparison, risk-adjusted
metrics (Sharpe, Sortino, Calmar), a `--sort` to rank the table by any of them, and
volatility-targeted **position sizing** (`--vol-target`) with a configurable engine rebalance
band. Next:

- Further metrics: win rate, average holding period, rolling returns.
- **Corporate-action-adjusted** closes and a `--benchmark` other than buy-and-hold.
- Multi-asset **portfolios** (cross-sectional momentum, equal-risk weighting) and long/short.
- Optional stop-loss / trailing-stop and a configurable rebalance calendar.

**Phase 2 — Robustness & validation.** Stop fooling yourself. A single backtest is the *most*
flattering number a strategy will ever show.

- **Walk-forward / out-of-sample** splits: fit parameters on one window, test on the next.
- **Parameter sweeps** with heatmaps to see whether an edge is a plateau (robust) or a spike
  (overfit), plus a note on multiple-testing / data-mining bias.
- Monte-Carlo trade reshuffling and regime analysis (bull/bear/sideways, high/low vol).
- Sensitivity to costs and slippage — the edge must survive pessimistic assumptions.

**Phase 3 — Paper trading (zero capital).** Wire the surviving strategy to a **live data feed**
and place *simulated* orders for weeks. Validates data plumbing, latency, and real slippage
before a single rupee is at risk. Use a free-API broker (Upstox / Dhan / Fyers) rather than a
paid one. Likely a **new module** (e.g. `papertrade/`), not part of this offline tool.

**Phase 4 — Live, bounded, tiny.** Only if an edge survives Phases 1–3. A rule-based bot *you*
approve — never open-ended discretion — with hard risk limits (max position, daily loss stop,
kill switch), full audit logging, and **SEBI algo registration/tagging** with the broker.
Start with an amount you are entirely willing to lose.

> Honest expectation: most strategies never make it past Phase 2. That is a *success* — the
> whole point of this pipeline is to discover a lack of edge cheaply, not to reach live trading.

## Development

```sh
cd backtest
go test ./...                              # all tests
go test -race ./...                        # what CI runs
go test ./internal/pipeline -update        # refresh golden files after an intended change
go vet ./... && gofmt -l .                 # gofmt must print nothing
```

The golden test in `internal/pipeline` locks the whole offline render path against a synthetic
fixture. After an **intended** output change, run it with `-update` and review the diff of
`internal/pipeline/testdata/golden/*` before committing — never blind-update.

> **Disclaimer:** Not investment advice. Every backtest is a draft to sanity-check yourself.

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

## New here? The core ideas in plain English

If you've never done this before, read this once and the rest will make sense.

- **Backtest** — a simulation. You take a *rule* for buying and selling ("hold the index when
  it's trending up, sit in cash otherwise"), replay it over real historical prices, and see what
  your money would have done. It answers "would this rule have worked?" — nothing more.
- **Strategy (or rule)** — the logic that decides, on each day, how much of your money should be
  in the asset. Here every strategy outputs a **weight** between 0 and 1: `0` = fully in cash,
  `1` = fully invested, `0.5` = half in, half cash.
- **Benchmark** — the thing you must beat to justify any effort. Ours is **buy-and-hold**: buy on
  day one, never trade, just sit. If a clever rule can't beat "do nothing," the rule is worthless.
  Every run shows your strategy *and* buy-and-hold side by side.
- **Bar** — one row of price data. Here, one trading **day** (the day's closing price).
- **Position / exposure** — how invested you are right now. "Flat" means 0% (all cash).
- **Costs** — trading isn't free. Every buy/sell pays brokerage, tax (STT) and **slippage** (the
  price moving against you between deciding and filling). Costs quietly kill most strategies, so
  we always subtract them.
- **Drawdown** — how far you fell from a previous peak. A 38% max drawdown means that at the
  worst moment you were down 38% from your high — the gut-check for "could I actually stomach
  holding this?"

The golden rule: **a good backtest is a reason to investigate further, never a reason to trade.**
It is the most flattering number a strategy will ever show, because it is fitted to the one past
that already happened.

## How a single run works

Each `run` walks day by day through the price history and does four things:

1. **Ask the strategy** for a target weight (0–1) using only the prices *up to and including
   today* — never the future (that would be cheating, called "lookahead").
2. **Rebalance** the portfolio toward that weight, **paying costs** on whatever it trades. To
   avoid pointless churn it only trades when the position has drifted more than ~1% from target.
3. **Mark to market** — record the portfolio's value at today's close.
4. At the end, turn that daily value history (the "equity curve") into the summary **metrics**
   you see in the table, and do the exact same thing for buy-and-hold so you can compare.

The signal is computed at a day's close and executed at that *same* close — a simplification.
Real fills happen a moment later at a slightly worse price, which is what the slippage cost stands
in for. Live results are always worse than a backtest; plan for it.

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

### What each strategy is actually betting on

- **`sma-cross` (simple moving-average crossover)** — a *moving average* is just the average
  price over the last N days, which smooths out the daily noise. This rule holds the asset while
  a **short** average (say 20 days, reacting quickly) sits above a **long** one (say 50 days,
  the slower trend), and moves to cash when the short drops below. The bet: *trends persist* —
  once something is going up, it tends to keep going. `--fast`/`--slow` set the two windows.
- **`ema-cross` (exponential moving-average crossover)** — identical idea, but the averages
  weight recent days more heavily, so they turn sooner. That means it catches trend changes
  earlier but also gets faked out more often in choppy markets ("whipsaws").
- **`momentum`** — the simplest trend bet: are we higher than we were `--lookback` days ago? If
  yes, hold; if no, cash. No averaging, just two price points.
- **`rsi` (Relative Strength Index)** — RSI is a 0–100 gauge of how one-sided recent moves have
  been; low means "sold off hard recently." This rule **buys the dip** — it goes in when RSI is
  below `--rsi-threshold` (default 30, "oversold"). The bet is *mean reversion*: after a sharp
  drop, prices bounce. This is the opposite instinct to the trend rules, and it often loses in a
  steady bull market (it keeps selling winners and buying fallers).
- **`donchian` (channel breakout, the classic "Turtle" rule)** — enter when today's close is the
  highest in the last `--entry` days (a breakout to new highs), and exit when it's the lowest in
  the last `--exit` days. It holds through everything in between, so it rides big trends but
  gives back some profit at every turn.
- **`buy-hold`** — the benchmark: buy once, hold forever. No skill, no trading, minimal cost.

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

## Understanding the output

Here is a real run — `sma-cross` vs buy-and-hold on the Nifty 50, 2019–2024, ₹10,000:

```
| Strategy         |   Total |   CAGR | Ann. vol | Sharpe | Sortino | Max DD | Calmar |     Final | Trades | Exposure |   Costs |
| sma-cross(20/50) | 116.09% | 13.71% |   11.79% |   1.17 |    1.73 | 20.64% |   0.66 | ₹21608.62 |     25 |   67.59% | ₹612.01 |
| buy-hold         | 119.09% | 13.97% |   18.36% |   0.82 |    1.12 | 38.44% |   0.36 | ₹21875.71 |      1 |  100.00% |  ₹14.98 |
```

Read it one column at a time:

- **Strategy** — the rule and its settings, e.g. `sma-cross(20/50)` = simple crossover with a
  20-day fast and 50-day slow average. A `+voltgt(10%/20)` suffix means volatility targeting is on.
- **Total** — total return over the whole period. `116%` means ₹10,000 became ₹21,600. Simple to
  grasp but misleading alone: it ignores how long it took and how scary the ride was.
- **CAGR** (Compound Annual Growth Rate) — that same return as a smooth *per-year* rate. `13.71%`
  means "as if it grew 13.71% every year." The fair way to compare periods of different lengths.
- **Ann. vol** (annualised volatility) — how bumpy the daily ride was, per year. Higher = wilder
  swings. Note buy-hold's `18.36%` vs the strategy's `11.79%`: the strategy was much calmer.
- **Sharpe ratio** — **return per unit of risk** (roughly CAGR ÷ volatility). The most quoted
  number in investing. Higher is better; above 1 is good, above 2 is excellent (and usually too
  good to be true). Here the strategy's `1.17` beats buy-hold's `0.82` — nearly the same money
  with far less turbulence. This is why `--sort sharpe` reshuffles the table.
- **Sortino ratio** — like Sharpe, but it only counts *downside* wobble as risk (nobody minds
  their portfolio jumping *up*). Usually a bit higher than Sharpe; a big gap means the volatility
  was mostly to the upside.
- **Max DD** (maximum drawdown) — the worst peak-to-trough fall you'd have endured. Buy-hold's
  `38.44%` (the 2020 crash) vs the strategy's `20.64%` is the real story: same money, half the
  pain. This number decides whether you could actually hold a strategy without panic-selling.
- **Calmar ratio** — CAGR ÷ max drawdown: return per unit of worst-case pain. Another
  "is the reward worth the fear?" gauge.
- **Final** — the ending pot, starting from your `--capital`.
- **Trades** — how many days it traded. Buy-hold trades once (buy and sit). More trades means
  more cost and more chances to be wrong.
- **Exposure** — the share of days it held any position. `67.59%` means it was invested
  two-thirds of the time and in cash the rest. Buy-hold is `100%` by definition.
- **Costs** — total brokerage + tax + slippage paid. Buy-hold's `₹14.98` vs the strategy's `₹612`
  is the price of all that trading — a real drag the strategy must overcome.

**The note under the table** gives the headline: how many strategies beat buy-and-hold, or a
warning that this one didn't. Take it seriously — beating the benchmark on *past* data is the
bare minimum, not proof of anything.

### So which strategy is "best"?

There is no single answer — it depends what you care about:

- Chasing the biggest number? Sort by `--sort return` or `cagr`.
- Want the smoothest ride / best risk-adjusted return? Sort by `sharpe` or `sortino`.
- Most worried about a gut-wrenching crash? Sort by `drawdown` (lowest is safest) or `calmar`.

In the example, buy-hold "wins" on raw return, but the crossover wins on *every* risk-adjusted
measure — almost the same money with half the drawdown. Which you'd actually prefer is a personal
question about how much volatility you can stomach.

## Reading results honestly (common traps)

- **Beating buy-and-hold once proves nothing.** With six strategies and a handful of parameters,
  *something* will look good on any single history by pure luck. That is why Phase 2 (below)
  exists.
- **Overfitting** — if you tweak `--fast`/`--slow` until the numbers sparkle, you've fitted the
  rule to noise in *this* history; it will disappoint on the next. Prefer settings that work
  across a *range* of values, not a magic single point.
- **Costs and slippage are optimistic here.** Real spreads, taxes and impact are worse, especially
  on small or illiquid names. If an edge only survives at zero cost, it isn't an edge.
- **This uses raw closes.** Dividends and stock splits aren't adjusted for, so use adjusted-close
  symbols where you can. It's also one asset at a time — no portfolio diversification yet.
- **The past is not the future.** A backtest assumes tomorrow's market behaves like the sample.
  Crashes, regime shifts and new regulations don't ask permission.

## Walk-forward: is the edge real, or one lucky stretch?

A single backtest over all history is the *most flattering* number a strategy will ever show. The
antidote is **walk-forward analysis**: chop the timeline into consecutive **out-of-sample folds**
and check the strategy in *each* sub-period separately. A real edge shows up again and again; a
fake one comes from a single favourable regime and vanishes everywhere else.

```sh
# Split 10 years of Nifty into 5 two-year folds and test sma-cross in each.
go run ./cmd/backtest walkforward --prices data/nifty.csv --symbol NIFTY50 \
  --strategy sma-cross --folds 5
```

A real result:

```
| Fold | Period                  | Strategy | Buy & hold |    Edge | Sharpe | Max DD | Beat? |
| 1    | 2015-01-02 → 2017-01-05 |   -6.57% |     -1.45% |  -5.12% |  -0.32 | 18.45% | no    |
| 2    | 2017-01-05 → 2019-01-04 |   22.68% |     29.65% |  -6.98% |   1.19 | 10.48% | no    |
| 3    | 2019-01-04 → 2021-01-05 |   75.70% |     32.37% |  43.33% |   2.37 |  8.32% | yes   |
| 4    | 2021-01-05 → 2022-12-29 |    7.02% |     28.11% | -21.09% |   0.34 | 20.64% | no    |
| 5    | 2022-12-29 → 2024-12-31 |   21.14% |     29.98% |  -8.84% |   0.99 |  8.62% | no    |
```

The lesson is stark: sma-cross beat buy-and-hold in **1 of 5 folds**. That one fold (2019–2021)
contains the COVID crash, where the trend rule went to cash and dodged the fall — almost its
*entire* apparent edge came from a single event. In every calm period it lost. A plain full-period
backtest hides this; the fold view exposes it.

How to read it:

- **Edge** — strategy return minus buy-and-hold return for that fold. Positive = beat the
  benchmark that period.
- **Beat?** — did the strategy out-return buy-and-hold in that fold? You want **yes across most
  folds**, not a single huge win dragging up the average.
- The strategy runs *continuously* across the whole history (so its moving averages stay warmed
  up); the folds only slice up the resulting equity curve for measurement.

`walkforward` takes the same strategy and cost flags as `run`, plus `--folds N` (default 4). It
needs a single non-benchmark strategy (not `all`).

### `--optimize`: the acid test (walk-forward optimisation)

Plain `walkforward` tests a *fixed* rule across folds. The stronger version re-chooses the
parameters on each fold's **training** data and tests them on the **next unseen** fold — the way
you'd actually have to pick parameters live, with no knowledge of the future. Add `--optimize`:

```sh
go run ./cmd/backtest walkforward --prices data/nifty.csv --symbol NIFTY50 \
  --strategy sma-cross --optimize --folds 5 --metric sharpe
```

Each fold sweeps the grid on all *prior* data, keeps the best parameters (by `--metric`), and
reports how they did on the following period. A real result:

```
| Fold | Period                  | Params           | Strategy | Buy & hold |    Edge | Beat? |
| 1    | 2016-09-06 → 2018-05-07 | fast=50 slow=180 |    7.44% |     19.82% | -12.38% | no    |
| 2    | 2018-05-07 → 2020-01-10 | fast=20 slow=200 |   -1.25% |     14.38% | -15.64% | no    |
| 3    | 2020-01-10 → 2021-09-03 | fast=20 slow=60  |   60.66% |     41.34% |  19.33% | yes   |
| 4    | 2021-09-03 → 2023-05-03 | fast=20 slow=60  |  -13.16% |      4.42% | -17.58% | no    |
| 5    | 2023-05-03 → 2024-12-31 | fast=10 slow=80  |   28.18% |     30.71% |  -2.53% | no    |
```

Two red flags leap out. First, the **best parameters jump around** every fold (50/180, then
20/200, then 20/60, then 10/80) — a stable edge would keep landing near the same settings.
Second, out-of-sample the strategy beat buy-and-hold in only **1 of 5 folds**. Verdict: on this
data, sma-cross has **no robust, transferable edge** — the settings that won in training simply
didn't carry forward. A plain full-period backtest, which looked competitive, hid all of this.

That "failure" is the tool doing its job. Finding out here, for free, that a rule doesn't survive
honest out-of-sample testing is *exactly* why you backtest before risking money.

`--optimize` uses the same grid controls as `sweep` (`--param name:min:max:step`, up to twice, or
a per-strategy default) and `--metric` to pick the winner. It needs `folds+1` segments of data
(the first is the initial training window).

By default the training window is **anchored** (expanding — each fold trains on *all* prior data).
Add `--rolling` to train on a fixed-length **trailing** window instead, so a strategy adapts to
recent conditions and distant history can't dominate the fit. Comparing anchored vs rolling is
itself informative: if the edge only appears under one, it's fragile.

## Parameter sweeps: robust plateau, or overfit spike?

Every strategy has knobs (`--fast`, `--slow`, `--lookback`, …). It is dangerously easy to try
values until one sparkles — but that number is fitted to *this* history's noise and won't repeat.
A **sweep** runs the strategy across a whole grid of values and shows the *shape* of the result:

- A **broad plateau** of good values → the edge is robust; it doesn't depend on picking the exact
  magic number, which is what you want.
- A **lone spike** surrounded by poor values → almost certainly overfit; that one setting got
  lucky on this sample and will disappoint live.

```sh
# Sweep sma-cross's two windows and colour the grid by Sharpe.
go run ./cmd/backtest sweep --prices data/nifty.csv --symbol NIFTY50 \
  --strategy sma-cross --metric sharpe
```

A real 2-D grid (rows = `fast`, columns = `slow`, each cell is the Sharpe ratio):

```
| fast\slow |   60 |    80 |  100 |  120 |  140 |  160 |  180 |  200 |
| 10        | 0.91 | 1.08◄ | 0.93 | 0.77 | 0.79 | 0.71 | 0.68 | 0.63 |
| 20        | 0.99 |  0.92 | 0.73 | 0.67 | 0.66 | 0.60 | 0.63 | 0.55 |
| 30        | 0.70 |  0.67 | 0.67 | 0.59 | 0.46 | 0.53 | 0.54 | 0.50 |
| 40        | 0.64 |  0.50 | 0.58 | 0.53 | 0.48 | 0.50 | 0.45 | 0.47 |
| 50        | 0.63 |  0.75 | 0.72 | 0.59 | 0.48 | 0.42 | 0.41 | 0.42 |
```

The `◄` marks the best cell (fast=10/slow=80, Sharpe 1.08). More importantly, the good values form
a **connected region** in the short-fast corner that fades smoothly — a fairly robust surface, not
an isolated fluke. If the best cell had been `2.0` with `0.3` all around it, you'd distrust it.

**How to use it:**

- **One parameter** → a table, one row per value, with all metrics and the best row marked. Great
  for `momentum --lookback` or exploring a single knob.
- **Two parameters** → the heatmap grid above, cells coloured by `--metric`
  (`return|cagr|sharpe|sortino|calmar|drawdown`, default `sharpe`). Invalid combinations (e.g. a
  crossover with `fast ≥ slow`) show as `—`.
- Specify the grid with `--param name:min:max:step`, up to twice. With no `--param`, a sensible
  default grid is chosen for the strategy. Example:
  `sweep --strategy rsi --param rsi-period:5:25:5 --param rsi-threshold:20:40:5 --metric calmar`.

Reach for a **narrow, isolated** best cell as a warning, not a discovery. Prefer a strategy whose
good region is wide — you'll be picking parameters blind on future data, so you want to land
*somewhere* in the good zone, not on a knife-edge.

### Cost sensitivity

You can sweep the **trading-cost** knobs too — `slippage-bps`, `brokerage-bps`, `stt-bps` — to see
how fast an edge decays as costs rise. Set the strategy's own parameters as fixed flags and sweep
the cost:

```sh
# How does ema-cross(20/50)'s return hold up as slippage goes from 0 to 30 bps?
go run ./cmd/backtest sweep --prices data/nifty.csv --symbol NIFTY50 \
  --strategy ema-cross --fast 20 --slow 50 --param slippage-bps:0:30:5 --metric return
```

A strategy whose advantage survives only at unrealistically low costs has no real edge — and live
costs are always worse than a backtest's. A gentle, linear decay (rather than a cliff) is the
reassuring shape.

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

backtest walkforward --prices <csv> --strategy <name> [--folds N] [--optimize] [same flags as run/sweep]
  --folds          number of consecutive out-of-sample folds (default 4)
  --optimize       re-fit parameters on each training window before testing the next fold
  --rolling        with --optimize: train on a fixed trailing window instead of all prior data
  --param/--metric with --optimize: grid to search and metric to pick the winner (as in sweep)

backtest sweep --prices <csv> --strategy <name> [--param name:min:max:step ...] [same cost flags]
  --param          parameter to sweep as name:min:max:step (repeatable, up to 2; default per strategy)
                   names: fast|slow|lookback|rsi-period|rsi-threshold|entry|exit|slippage-bps|brokerage-bps|stt-bps|vol-target
  --metric         grid metric: return | cagr | sharpe | sortino | calmar | drawdown (default sharpe)

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
flattering number a strategy will ever show. Delivered so far: **walk-forward** analysis
(`walkforward`), **parameter sweeps** (`sweep`) that map the performance surface (including
**cost-sensitivity** sweeps over slippage/brokerage/STT), and **walk-forward optimisation**
(`walkforward --optimize`) that re-fits parameters on each training window and tests them
out-of-sample — the most honest estimate here of live performance. Next:

- Monte-Carlo trade reshuffling and regime analysis (bull/bear/sideways, high/low vol).

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

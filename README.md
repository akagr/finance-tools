# finance-tools

[![CI](https://github.com/akagr/finance-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/akagr/finance-tools/actions/workflows/ci.yml)

A monorepo of small, focused personal-finance tools built from broker/market data —
Indian tax filing and beyond. Each tool is an isolated Go module under its own directory,
tied together by a root `go.work` workspace.

> The badge and module paths assume the repo slug `github.com/akagr/finance-tools`; adjust if
> your remote differs (also in `schedule-fa/go.mod` and `go.work`).

## Tools

| Tool            | Directory                      | Status           | What it does                                                                                                                      |
|-----------------|--------------------------------|------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| **schedule-fa** | [`schedule-fa/`](schedule-fa/) | complete (M0–M7) | Generates a ready-to-use **Schedule FA** (Foreign Assets) report for the Indian ITR from **Interactive Brokers (IBKR)** holdings. |
| **correlation** | [`correlation/`](correlation/) | in progress      | Computes return **correlations** across assets (e.g. VWRA vs Nifty 50) to assess how diversified a portfolio really is.           |
| **backtest**    | [`backtest/`](backtest/)       | in progress      | Offline **backtester** for rule-based strategies on NSE daily data (SMA/EMA crossover, momentum, RSI, Donchian breakout vs buy-and-hold, realistic costs). Research only — not advice. |

## Layout

```
finance-tools/
  go.work            # ties all tool modules together
  schedule-fa/       # each tool: its own go.mod, cmd/, internal/, docs/, data/
  …                  # future tools as sibling directories
```

## Algorithmic-trading roadmap

The **backtest** tool is Phase 1 of a deliberately staged path from idea to (maybe) live
capital — money is the last step, not the first:

1. **Backtesting** *(done)* — measure a rule's edge on history vs a benchmark; compare strategies,
   rank by risk-adjusted metrics, and size positions by volatility.
2. **Robustness & validation** *(in progress)* — walk-forward folds, parameter sweeps and
   walk-forward optimisation (re-fit parameters out-of-sample) already stress-test an edge;
   Monte-Carlo, regime and cost sensitivity next.
3. **Paper trading** *(zero capital)* — a live data feed with simulated orders for weeks;
   likely a new `papertrade/` module.
4. **Live, bounded, tiny** — only if an edge survives, as an approved rule-based bot with hard
   risk limits and SEBI algo registration.

Most ideas should die in steps 1–2 — cheaply, on a laptop. See
[`backtest/README.md`](backtest/README.md#roadmap) for the detailed roadmap.

## Building

Requires Go (not currently installed on this machine — `brew install go`).

```sh
go build ./...        # from repo root, builds every module in the workspace
```

> **Disclaimer:** Nothing here is tax advice. Output is a working draft to be verified by
> the taxpayer or a qualified professional before filing.

## License

[MIT](LICENSE) © Akash Agrawal
</content>

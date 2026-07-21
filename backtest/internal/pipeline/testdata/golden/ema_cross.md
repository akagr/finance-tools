# Backtest — SYNTH

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Initial capital: ₹100000.00
- Costs: brokerage 0 bps, STT 10 bps, slippage 5 bps (per trade)

| Strategy        |  Total |   CAGR | Ann. vol | Sharpe | Max DD |      Final | Trades | Exposure |   Costs |
| --------------- | -----: | -----: | -------: | -----: | -----: | ---------: | -----: | -------: | ------: |
| ema-cross(5/20) | 11.39% | 26.96% |    6.58% |   3.50 |  4.19% | ₹111385.01 |      6 |   45.00% | ₹927.18 |
| buy-hold        | 12.08% | 28.72% |    8.94% |   2.75 |  8.24% | ₹111913.70 |      1 |  100.00% | ₹149.78 |

> The strategy did not beat buy-and-hold after costs over this period — the expected outcome for most simple rules, and exactly why you backtest before deploying capital.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

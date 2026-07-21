# Parameter sweep — SYNTH (sma-cross)

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Metric: **sharpe** (best 7.55)

Rows = `fast`, columns = `slow`; each cell is **sharpe** (◄ marks the best).

| fast\slow |    10 |   20 |    30 |
| --------- | ----: | ---: | ----: |
| 5         | 7.55◄ | 4.01 |  2.20 |
| 10        |     — | 2.28 |  0.21 |
| 15        |     — | 0.35 | -1.88 |

> Look at the shape, not the single best cell. A broad region of good values means the edge is robust to the exact parameters; a lone peak surrounded by poor values is almost certainly overfit to this history and will not repeat.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

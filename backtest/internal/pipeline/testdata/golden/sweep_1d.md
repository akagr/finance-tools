# Parameter sweep — SYNTH (momentum)

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Metric: **cagr** (best 54.33%)

| lookback | Return |   CAGR | Sharpe | Sortino | Max DD | Calmar | Trades |        |
| -------: | -----: | -----: | -----: | ------: | -----: | -----: | -----: | ------ |
|       10 | 21.65% | 54.33% |   6.70 |   19.07 |  1.96% |  27.65 |      6 | ◄ best |
|       20 |  5.46% | 12.49% |   1.79 |    3.07 |  4.80% |   2.60 |      6 |        |
|       30 | -4.15% | -8.96% |  -1.52 |   -2.15 |  9.65% |  -0.93 |      4 |        |
|       40 |  8.90% | 20.77% |   2.50 |    4.53 |  7.77% |   2.67 |      1 |        |

> Look at the shape, not the single best cell. A broad region of good values means the edge is robust to the exact parameters; a lone peak surrounded by poor values is almost certainly overfit to this history and will not repeat.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

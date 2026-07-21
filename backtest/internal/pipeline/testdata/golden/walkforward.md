# Walk-forward — SYNTH (sma-cross(3/8))

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Out-of-sample folds: 4

| Fold | Period                  | Strategy | Buy & hold |   Edge | Sharpe | Max DD | Beat? |
| ---- | ----------------------- | -------: | ---------: | -----: | -----: | -----: | ----- |
| 1    | 2023-01-02 → 2023-02-10 |    0.48% |     -0.70% |  1.19% |   1.46 |  0.90% | yes   |
| 2    | 2023-02-10 → 2023-03-24 |   14.29% |     14.69% | -0.40% |  17.49 |  0.39% | no    |
| 3    | 2023-03-24 → 2023-05-05 |    7.00% |      0.31% |  6.69% |   9.18 |  0.62% | yes   |
| 4    | 2023-05-05 → 2023-06-16 |    4.99% |     -1.88% |  6.87% |   7.80 |  0.83% | yes   |

> The strategy beat buy-and-hold in 3 of 4 out-of-sample folds. Consistency across folds is the signal to look for; an edge concentrated in one fold is usually luck or a single favourable regime, not a repeatable strategy.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

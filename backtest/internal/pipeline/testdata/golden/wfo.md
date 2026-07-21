# Walk-forward optimisation — SYNTH (sma-cross)

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Out-of-sample folds: 3
- Parameters re-fit each fold on prior data, chosen by **sharpe**
- Training window: anchored (expanding)

| Fold | Period                  | Params        | Strategy | Buy & hold |   Edge | Sharpe | Max DD | Beat? |
| ---- | ----------------------- | ------------- | -------: | ---------: | -----: | -----: | -----: | ----- |
| 1    | 2023-02-10 → 2023-03-24 | fast=3 slow=8 |   14.29% |     14.69% | -0.40% |  17.49 |  0.39% | no    |
| 2    | 2023-03-24 → 2023-05-05 | fast=3 slow=8 |    7.00% |      0.31% |  6.69% |   9.18 |  0.62% | yes   |
| 3    | 2023-05-05 → 2023-06-16 | fast=3 slow=8 |    4.99% |     -1.88% |  6.87% |   7.80 |  0.83% | yes   |

> Parameters were re-fit on prior data only (anchored (all prior data)) and tested on the next unseen fold, so this is a genuine out-of-sample result: the strategy beat buy-and-hold in 2 of 3 folds. If the winning parameters keep changing wildly between folds, or the edge vanishes here despite looking good in a plain backtest, the rule was overfit.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

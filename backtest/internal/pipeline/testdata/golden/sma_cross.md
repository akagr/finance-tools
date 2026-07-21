# Backtest — SYNTH

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Initial capital: ₹100000.00
- Costs: brokerage 0 bps, STT 10 bps, slippage 5 bps (per trade)

| Strategy        |  Total |   CAGR | Ann. vol | Sharpe | Sortino | Max DD | Calmar |      Final | Trades | Exposure |   Costs |
| --------------- | -----: | -----: | -------: | -----: | ------: | -----: | -----: | ---------: | -----: | -------: | ------: |
| sma-cross(5/20) | 12.78% | 30.50% |    6.41% |   4.01 |    8.33 |  3.57% |   8.53 | ₹112777.38 |      6 |   43.33% | ₹931.29 |
| buy-hold        | 12.08% | 28.72% |    8.94% |   2.75 |    4.81 |  8.24% |   3.49 | ₹111913.70 |      1 |  100.00% | ₹149.78 |

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

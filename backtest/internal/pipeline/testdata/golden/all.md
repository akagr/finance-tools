# Backtest — SYNTH

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Initial capital: ₹100000.00
- Costs: brokerage 0 bps, STT 10 bps, slippage 5 bps (per trade)

| Strategy         |   Total |    CAGR | Ann. vol | Sharpe | Sortino | Max DD | Calmar |      Final | Trades | Exposure |   Costs |
| ---------------- | ------: | ------: | -------: | -----: | ------: | -----: | -----: | ---------: | -----: | -------: | ------: |
| donchian(20/10)  |  14.48% |  34.89% |    5.24% |   5.50 |   16.51 |  1.88% |  18.58 | ₹114478.14 |      4 |   33.33% | ₹643.44 |
| buy-hold         |  12.08% |  28.72% |    8.94% |   2.75 |    4.81 |  8.24% |   3.49 | ₹111913.70 |      1 |  100.00% | ₹149.78 |
| ema-cross(20/50) |   0.56% |   1.24% |    6.31% |   0.22 |    0.34 |  7.77% |   0.16 | ₹100558.74 |      1 |   59.17% | ₹149.78 |
| momentum(120)    |   0.00% |   0.00% |    0.00% |   0.00 |    0.00 |  0.00% |   0.00 | ₹100000.00 |      0 |    0.00% |   ₹0.00 |
| rsi(14<30)       |  -9.39% | -19.61% |    3.76% |  -5.53 |   -5.65 |  9.39% |  -2.09 |  ₹90611.78 |      5 |   33.33% | ₹723.70 |
| sma-cross(20/50) | -11.12% | -22.97% |    4.15% |  -6.00 |   -6.14 | 12.26% |  -1.87 |  ₹88878.10 |      3 |   45.83% | ₹434.04 |

> 1 of 5 strategies beat buy-and-hold after costs over this period. Beating a benchmark on past data is not an edge — it is a hypothesis to validate out-of-sample before risking capital.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

# Backtest — SYNTH

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Initial capital: ₹100000.00
- Costs: brokerage 0 bps, STT 10 bps, slippage 5 bps (per trade)

| Strategy   |   Total |    CAGR | Ann. vol | Sharpe | Max DD |      Final | Trades | Exposure |   Costs |
| ---------- | ------: | ------: | -------: | -----: | -----: | ---------: | -----: | -------: | ------: |
| buy-hold   |  12.08% |  28.72% |    8.94% |   2.75 |  8.24% | ₹111913.70 |      1 |  100.00% | ₹149.78 |
| rsi(14<45) | -10.34% | -21.47% |    4.75% |  -4.84 | 10.34% |  ₹89656.72 |      5 |   41.67% | ₹724.26 |

> The strategy did not beat buy-and-hold after costs over this period — the expected outcome for most simple rules, and exactly why you backtest before deploying capital.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

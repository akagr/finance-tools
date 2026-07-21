# Regime analysis — SYNTH (sma-cross(3/8))

- Period: 2023-01-02 → 2023-06-16 (120 bars)
- Trend split: price vs its 20-bar average; volatility split: 10-bar realised vol vs sample median

| Regime                | Days | Strategy | Buy & hold |   Edge | Sharpe |
| --------------------- | ---: | -------: | ---------: | -----: | -----: |
| Trend · Bull          |   50 |   24.66% |     21.63% |  3.03% |  16.32 |
| Trend · Bear          |   49 |    2.99% |    -11.97% | 14.96% |   5.13 |
| Volatility · High vol |   55 |   11.56% |      3.61% |  7.94% |   7.62 |
| Volatility · Low vol  |   54 |   14.37% |      0.68% | 13.69% |  11.59 |

> The strategy's edge over buy-and-hold is largest in the Bear regime (+15.0%) and weakest in Bull (+3.0%). An edge concentrated in one regime only pays off when that regime recurs — size your confidence accordingly.
> Regime labels are descriptive: the high/low-vol split uses the whole sample's median, so this explains the past rather than being a lookahead-free trading signal.

_NOTE: not investment advice. A backtest is a hypothesis, not a forecast — it is fit to the past, ignores regime change, and flatters strategies that overfit. Costs and slippage are estimates; live results will be worse._

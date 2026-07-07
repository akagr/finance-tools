# Asset correlation report

- Period: 2024-01-05 → 2024-03-01 (8 weekly returns)
- Returns: log
- Currency basis: native (per-asset local currency, no FX conversion)

## Correlation matrix

|            | VWRA | ^NSEI |
|------------|------|------|
| **VWRA** | 1.0000 | 0.5163 |
| **^NSEI** | 0.5163 | 1.0000 |

## Per-asset stats

| Asset | Currency | Mean return | Volatility (per period) | Volatility (annualised) |
|-------|----------|-------------|-------------------------|-------------------------|
| VWRA | USD | 0.89% | 0.80% | 5.79% |
| ^NSEI | INR | 0.65% | 0.85% | 6.14% |

## Pairwise correlations (95% CI)

| Pair | r | 95% CI |
|------|---|--------|
| VWRA – ^NSEI | 0.5163 | [-0.2961, 0.8953] |

## Notes

- Native mode with mixed currencies (INR, USD): these correlations blend asset and FX co-movement. Pass --base-currency (with --fx) to normalise to one currency.
- Small sample: only 8 weekly return observations. Correlations are noisy; prefer a longer window or a lower frequency.

> NOTE: not investment advice. Correlations are backward-looking, sample-dependent, and unstable in crises (they often rise toward 1 exactly when diversification is needed most).


# Daily price data (exact peak, mode B)

The exact peak engine (`--prices`) needs a daily per-unit **close** price for each
instrument over the calendar year. Supply a CSV (or a directory of them):

```csv
date,symbol,isin,close,currency
2024-06-14,AAPL,US0378331005,212.50,USD
2024-12-31,AAPL,US0378331005,250.00,USD
```

- `date` — `YYYY-MM-DD` (or `YYYYMMDD`).
- `symbol` and/or `isin` — at least one; rows are keyed by both so either matches.
- `close` — per-unit close in `currency`.
- `currency` — optional, defaults to `USD`.

Lookups use **preceding-trading-day fallback** (markets are closed on weekends/holidays),
so you only need rows for actual trading days. Coverage must span every day a position is
held, *and* the SBI TTBR must exist for those days; otherwise that day is skipped, the
security's peak is marked approximate, and the Table A2 peak falls back to the upper-bound
sum (the run reports this).

## Fetching

Use the helper, which pulls from the Yahoo Finance chart API (no key) and writes this format:

```sh
scripts/fetch-prices.py <year>            # reads scripts/tickers.txt
```

Edit `scripts/tickers.txt` (one line per holding: `symbol  yahoo-symbol  isin  [currency]`).
Yahoo symbols: US tickers are plain (e.g. `IBKR`); LSE uses `.L` (e.g. `VWRA.L`, the USD
line — `VWRP.L` is the GBP line). The script writes the **raw** close (not adjusted).

Price files are **not vendored** and are gitignored — they can be large and are user-specific.

> Splits/mergers: the position series is reconstructed from trades and is **not**
> split-adjusted, so a corporate action in the year makes the pre-action peak unreliable —
> those securities are already flagged for manual review.

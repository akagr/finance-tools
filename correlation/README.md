# correlation

Computes return **correlations** across two or more assets — e.g. VWRA vs Nifty 50 — so you
can see how diversified a portfolio really is. A correlation near **1** means two assets move
together (little diversification); near **0** means independent; **negative** means they hedge
each other.

Pure Go standard library, no external dependencies. Offline and CSV-driven: reproducible, and
your holdings never leave your machine.

## Quickstart

```sh
cd correlation

# 1. Fetch daily closes for the assets in scripts/tickers.txt (Yahoo Finance).
python3 scripts/fetch.py prices 2020-01-01 2024-12-31 > data/prices.csv

# 2. Correlate them (weekly log returns, printed as Markdown).
go run ./cmd/correlation compute --prices data/prices.csv
```

Add more assets by editing `scripts/tickers.txt` (any number of symbols).

### Comparing across currencies (e.g. USD ETF vs INR index)

By default each asset is correlated in its **own** currency. For a rupee investor the return
that matters is in **INR**, which folds in USD/INR moves. Fetch an FX series and normalise:

```sh
python3 scripts/fetch.py fx 2020-01-01 2024-12-31 USD:INR=X > data/fx.csv
go run ./cmd/correlation compute --prices data/prices.csv --base-currency INR --fx data/fx.csv
```

Converting to a common currency typically **changes** the correlation — that shift is exactly
the diversification (or lack of it) the FX exposure adds.

> **Keep the FX window in sync with prices.** The FX series must span the whole price date
> range, so fetch both over the *same* dates. If you later re-fetch prices over a longer window
> (or add an older asset), re-fetch `fx.csv` too — otherwise `compute` stops with an error
> naming the gap and the range to re-fetch.

## Usage

```
correlation compute --prices <csv|dir> [flags]

  --prices            price CSV file or directory (required)
  --default-currency  currency for rows that omit one (default USD)
  --base-currency     convert every series to this currency first (native mode if empty)
  --fx                FX CSV, required when --base-currency needs conversions
  --frequency         daily | weekly | monthly (default weekly)
  --returns           log | simple (default log)
  --format            comma-separated: md,csv,json (default md)
  --out               output directory (default: print to stdout)
```

## CSV formats

Prices (long form; one file may hold many symbols):

```
date,symbol,close,currency
2024-06-14,VWRA,118.42,USD
2024-06-14,^NSEI,23465.60,INR
```

FX (`rate` = value of one unit of `currency` in the base currency):

```
date,currency,rate
2024-06-14,USD,83.55
```

## Method & caveats

- **Resample, then difference.** Assets trade on different exchange calendars (LSE vs NSE), so
  series are resampled to a common **frequency** (last close in each period) and intersected
  onto shared periods before computing returns. Weekly (the default) reduces the noise that
  non-synchronous trading across time zones adds to daily correlations.
- **Pearson correlation** of period returns, with a 95% confidence interval (Fisher
  z-transform). A wide interval means the estimate is uncertain — usually too few observations.
- **Correlations are unstable.** They are backward-looking, sample-dependent, and tend to rise
  toward 1 in crises — exactly when diversification is needed most. Treat the number as a
  guide, not a guarantee.
- Numbers here are `float64` (statistics, not money accounting) — a deliberate departure from
  the `math/big.Rat` rule used elsewhere in this repo.

## Development

```sh
cd correlation
go test ./...                              # all tests
go test -race ./...                        # what CI runs
go test ./internal/pipeline -update        # refresh golden files after an intended change
go vet ./... && gofmt -l .                 # gofmt must print nothing
```

> **Disclaimer:** Not investment advice. Output is a working draft to sanity-check yourself.

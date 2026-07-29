# SBI TTBR rate data

The `fx` package reads SBI **TT Buying Rate** history in the community
**[SBI FX RateKeeper](https://github.com/sahilgupta/sbi-fx-ratekeeper)** format — one CSV
per currency, with the currency in the filename:

```
SBI_REFERENCE_RATES_USD.csv
SBI_REFERENCE_RATES_EUR.csv
```

Columns (only `DATE` and `TT BUY` are used; others are ignored):

```csv
DATE,PDF FILE,TT BUY,TT SELL,BILL BUY,BILL SELL,FOREX TRAVEL CARD BUY,FOREX TRAVEL CARD SELL,CN BUY,CN SELL
2024-12-31 09:00,https://.../2024-12-31.pdf,85.55,86.40,85.49,86.55,85.05,86.75,84.75,86.95
```

- `DATE` — `YYYY-MM-DD HH:MM` (date-only `YYYY-MM-DD` also accepted).
- `TT BUY` — INR per 1 unit of the currency. A value of `0.00` means SBI did not
  publish that day; such rows are **skipped**, so the preceding-working-day fallback applies.
- If a date has multiple rows (intraday revisions), the **last** one wins.

## Getting the data

There is no free official SBI TTBR API. Download the per-currency CSVs from the RateKeeper
repo (or a fork) into this directory:

```sh
curl -L -o data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  https://raw.githubusercontent.com/sahilgupta/sbi-fx-ratekeeper/main/csv_files/SBI_REFERENCE_RATES_USD.csv
```

The data is **not bundled** in the repo (it is third-party and updated daily). Load a whole
directory with `fx.LoadRateKeeperDir(dir)` or a single file with
`(*CSVStore).LoadRateKeeperFile(cur, path)`; the `--rates` CLI flag (wired in M3) points at
either. Coverage starts ~Jan 2020.

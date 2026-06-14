# schedule-fa

A Go CLI that turns **Interactive Brokers (IBKR)** holdings into a ready-to-use
**Schedule FA** (Foreign Assets) report for the Indian ITR — handling the calendar-year
basis, SBI TT buying-rate conversion to INR, and peak/closing/initial values per
security, with a full audit trail.

See [`docs/schedule-fa-ibkr-plan.md`](docs/schedule-fa-ibkr-plan.md) for the research,
challenges, locked decisions, architecture, and milestones.

## Status

- **M1 — IBKR ingest** ✅ — parses a downloaded Activity Flex XML (account, open positions
  with lot detail, trades, dividends with withholding matched), constrained to the calendar
  year. `generate` prints a parse summary.
- **M2 — FX engine** ✅ — `fx.CSVStore` reads the community SBI FX RateKeeper format and
  converts to INR with preceding-working-day fallback and per-figure audit records.
- **M3 — Table A3 + reports** ✅ — approximate peak (mode C) + row builder produce Table A3
  (initial/peak/closing/dividend/proceeds in INR, audit trail, review flags), rendered to
  **Markdown, CSV, and JSON**. `generate` runs the full pipeline and writes the report.
- **M5 — Table A2 + edge cases** ✅ — custodial-account row, `--entities` metadata override
  (address/ZIP/country code/nature), RSU vesting dates, and corporate-action review flags.
- **M4 — Exact peak** ✅ — `--prices` enables mode B: daily share reconstruction valued
  against a daily price series (preceding-trading-day fallback) × TTBR, plus a **true Table
  A2 peak** (max daily NAV). Mode C remains the fallback when no prices are supplied.
- **M6 — Flex Web Service** ✅ — `--flex-token` + `--flex-query` pull the statement online
  (no manual XML download); `--save-statement` keeps a copy.

Next: M7 (HTML/PDF polish).

## Usage (target)

```sh
# 1. SBI TTBR rates (see data/ttbr/README.md)
curl -L -o data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  https://raw.githubusercontent.com/sahilgupta/sbi-fx-ratekeeper/main/csv_files/SBI_REFERENCE_RATES_USD.csv

# 2. Daily prices for exact peak (edit scripts/tickers.txt first)
scripts/fetch-prices.py 2026

# 3. Generate (calendar year is enforced; output defaults under gitignored private/)
schedulefa generate \
  --year 2026 \
  --statement private/flex-2026.xml \              # IBKR Activity Flex Query, XML
  --rates data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  --prices data/prices/prices-2026.csv \           # omit for approximate peak (mode C)
  --entities data/entities/entities.csv \          # address/ZIP/country-code overrides
  --out private/report --format md,csv,json
```

Instead of `--statement`, pull the statement online from the IBKR **Flex Web Service**
(create a token under Client Portal → Settings → Flex Web Service):

```sh
schedulefa generate --year 2026 \
  --flex-token <token> --flex-query <activity-flex-query-id> \
  --save-statement private/flex-2026.xml \         # optional: keep the raw XML
  --rates data/ttbr/SBI_REFERENCE_RATES_USD.csv --prices data/prices/prices-2026.csv
```

> Keep real Flex exports under `private/` (gitignored) — they contain your account
> number, address, and holdings and must never be committed. For a full Schedule FA you
> want a **complete past calendar year** (e.g. a Jan 1–Dec 31 export), not a year-to-date one.

## Build

```sh
go build ./cmd/schedulefa       # from the schedule-fa/ directory
```

> **Disclaimer:** Not tax advice. Output is a working draft to be verified before filing.
</content>

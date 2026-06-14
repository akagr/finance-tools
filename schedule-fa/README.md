# schedule-fa

A Go CLI that turns **Interactive Brokers (IBKR)** holdings into a ready-to-use
**Schedule FA** (Foreign Assets) report for the Indian ITR — handling the calendar-year
basis, SBI TT buying-rate conversion to INR, and peak/closing/initial values per
security, with a full audit trail.

See [`docs/schedule-fa-ibkr-plan.md`](docs/schedule-fa-ibkr-plan.md) for the research,
challenges, locked decisions, architecture, and milestones.

## Status

**M1 — IBKR ingest.** Parses a downloaded Activity Flex XML (account, open positions with
lot detail, trades, dividends with withholding matched), constrained to the calendar year;
`generate` prints a parse summary. Downstream stages (fx → peak → build → report) are still
stubs. Tested against `internal/ibkr/testdata/sample_flex.xml`.

## Usage (target)

```sh
schedulefa generate \
  --year 2024 \                          # CALENDAR year (Jan 1 – Dec 31), enforced
  --statement private/flex-2024.xml \    # IBKR Activity Flex Query, XML output (offline mode)
  --rates data/ttbr/usd.csv \            # optional SBI TTBR override
  --out ./report --format md,csv,json
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

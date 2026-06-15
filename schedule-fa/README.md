# schedule-fa

A Go CLI that turns **Interactive Brokers (IBKR)** holdings into a ready-to-use
**Schedule FA** (Foreign Assets) report for the Indian ITR — handling the calendar-year
basis, SBI TT buying-rate conversion to INR, and peak/closing/initial values per
security, with a full audit trail. Outputs Markdown, CSV, JSON, and a printable HTML.

See [`docs/schedule-fa-ibkr-plan.md`](docs/schedule-fa-ibkr-plan.md) for the research,
challenges, decisions, architecture, and milestones.

> **Disclaimer:** Not tax advice. The output is a working draft to verify (ideally with a
> CA) before filing. You remain responsible for what you file.

## Example output

The printable HTML report (rendered from synthetic sample data — no real holdings):

<p align="center">
  <img src="docs/img/report-sample-v2.png" alt="Sample Schedule FA HTML report" width="640">
</p>

Complete example reports in every format live in
[`internal/pipeline/testdata/golden/`](internal/pipeline/testdata/golden/) —
[`report.md`](internal/pipeline/testdata/golden/report.md),
[`report.csv`](internal/pipeline/testdata/golden/report.csv),
[`report.json`](internal/pipeline/testdata/golden/report.json), and
[`report.html`](internal/pipeline/testdata/golden/report.html). These are the golden fixtures
the offline pipeline is tested against, so they always reflect the tool's current output.

---

## Getting your data from IBKR

You need an **Activity Flex Query** (it defines what goes in the statement). Then either
**download its XML** (offline) or pull it via the **Flex Web Service** with a token (online).
Menu names differ slightly across IBKR's portal versions — both are given below.

### Step 1 — Create the Activity Flex Query

1. Log in to **IBKR Client Portal** (<https://www.interactivebrokers.com> → Login).
2. Open **Performance & Reports → Flex Queries** (older menu: **Reports → Flex Queries**).
3. Next to **Activity Flex Query**, click the **＋** (Create).
4. Name it e.g. `ScheduleFA`. Set:
   - **Format:** `XML`
   - **Period:** `Last Calendar Year` (or `Custom` with the range **Jan 1 – Dec 31** of the
     tax year — Schedule FA is on the **calendar** year, not the Apr–Mar financial year)
   - **Date Format:** leave the default (`yyyyMMdd`)
5. Turn on these **sections** (open each, then "Select All" fields — extra fields are
   ignored by the tool, so over-selecting is safe):
   - **Account Information**
   - **Open Positions** — set the option **Lot Details = Yes** (a.k.a. "Position Lots")
   - **Trades** (Executions)
   - **Cash Transactions** — make sure types **Dividends** and **Withholding Tax** are included
   - **Corporate Actions** (optional — lets the tool flag splits/mergers)
   - **Financial Instrument Information** (a.k.a. **Securities Info**)
6. **Save** the query.

### Step 2a — Offline: download the XML

On the Flex Queries page, click the query's **Run** (▶) icon → pick the year → **Download**
the XML. Save it under `schedule-fa/private/` (gitignored), then use `--statement`.

### Step 2b — Online: token + Query ID (no manual download)

- **Query ID** — on the Flex Queries page the ID is the number shown beside the query (e.g.
  `123456`); it's also in the query's edit screen. Pass it as `--flex-query`.
- **Token** — open **Settings → Account Settings → Flex Web Service** (older menu:
  **Reports → Settings → Flex Web Service Configuration**). Click **Configure**, set status
  **Active**, pick a token validity period, and **Generate**. Copy the long token string
  (shown once). Pass it as `--flex-token`. One token works for all your Flex Queries.

> **Treat the token like a password** — it grants read access to your statements. Don't
> commit it; prefer an env var (e.g. `--flex-token "$IBKR_FLEX_TOKEN"`).

---

## Generating the report

```sh
# (one-time) build the CLI from the schedule-fa/ directory
go build -o schedulefa ./cmd/schedulefa

# 1. SBI TTBR rates  (see data/ttbr/README.md)
curl -L -o data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  https://raw.githubusercontent.com/sahilgupta/sbi-fx-ratekeeper/main/csv_files/SBI_REFERENCE_RATES_USD.csv

# 2. Daily prices for the exact peak  (edit scripts/tickers.txt first; see data/prices/README.md)
scripts/fetch-prices.py 2026
```

**Offline** (downloaded XML):

```sh
./schedulefa generate \
  --year 2026 \                                    # CALENDAR year (Jan 1–Dec 31), enforced
  --statement private/flex-2026.xml \
  --rates data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  --prices data/prices/prices-2026.csv \           # omit → approximate peak (mode C)
  --entities data/entities/entities.csv \          # address/ZIP/country-code overrides
  --out private/report --format md,csv,json,html
```

**Online** (Flex Web Service — no manual download):

```sh
./schedulefa generate --year 2026 \
  --flex-token "$IBKR_FLEX_TOKEN" --flex-query 123456 \
  --save-statement private/flex-2026.xml \         # optional: keep a copy of the raw XML
  --rates data/ttbr/SBI_REFERENCE_RATES_USD.csv \
  --prices data/prices/prices-2026.csv \
  --entities data/entities/entities.csv \
  --out private/report --format md,csv,html
```

Outputs land in `--out` (default `private/report/`): `report.md`, `report.csv`,
`report.json`, `report.html`. **For a PDF**, open `report.html` in a browser and choose
**Print → Save as PDF** (the page has print-tuned styling). The **CSV** is for transcribing
into the ITR utility; the **Markdown/HTML** carry a per-figure audit trail (source amount,
TTBR, and the exact rate date used) and a reconciliation summary.

> Keep real Flex exports and reports under `private/` (gitignored) — they contain your
> account number, address, and holdings, and must never be committed. Use a **complete past
> calendar year** export for a real filing (a year-to-date export is only a partial draft).

---

## Status

All milestones complete (M0–M7):

- **M1 — Ingest** — parse a downloaded Activity Flex XML (account, lot-detailed positions,
  trades, dividends with withholding matched), constrained to the calendar year.
- **M2 — FX** — SBI FX RateKeeper TTBR data; INR conversion with preceding-working-day
  fallback and per-figure audit records.
- **M3 — Table A3 + reports** — A3 rows (initial/peak/closing/dividend/proceeds in INR) with
  audit trail and review flags; Markdown / CSV / JSON renderers.
- **M5 — Table A2 + edge cases** — custodial-account row; `--entities` metadata override;
  RSU vesting dates; corporate-action flags; ISD country codes.
- **M4 — Exact peak** — `--prices` enables mode B (daily share reconstruction × daily price
  × TTBR) plus a true Table A2 peak (max daily NAV). Mode C is the fallback.
- **M6 — Flex Web Service** — `--flex-token` + `--flex-query` online pull; `--save-statement`.
- **M7 — HTML** — printable, self-contained `report.html` (Print → Save as PDF).

## Build & test

```sh
go build ./cmd/schedulefa      # from schedule-fa/
go test ./...
```

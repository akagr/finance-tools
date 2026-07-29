# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A monorepo of Go tools for Indian investors, all zero-dependency (Go stdlib only), Go 1.26,
in one `go.work` workspace:

- **`itr-foreign/`** — a CLI that turns Interactive Brokers (IBKR) holdings into Indian ITR
  schedules: `generate` builds **Schedule FA** (Foreign Assets, calendar year) and `fsi` builds
  **Schedule FSI + TR** (foreign income and tax relief, financial year) with a Form 67
  worksheet. The most developed tool; most of this doc is about it.
- **`correlation/`** — computes return **correlations** across assets to gauge portfolio
  diversification (CSV/Yahoo-driven, md/csv/json output).
- **`backtest/`** — an offline **backtester** for rule-based strategies on NSE daily data
  (trend, momentum, mean-reversion and breakout rules vs a buy-and-hold benchmark, realistic
  costs). Each strategy lives in its own file under `internal/strategy/` and is registered in
  `pipeline.buildStrategy`; `--strategy all` compares them in one table, `--sort` ranks it, and
  `--vol-target` adds a volatility-targeting position-sizing overlay (the benchmark stays
  unscaled). A `walkforward` command splits history into out-of-sample folds (with `--optimize`
  re-fitting parameters per fold), and a `sweep` command maps the metric surface over a 1-D/2-D
  parameter grid (plateau vs overfit spike), a `montecarlo` command bootstraps daily returns to
  gauge how much of a result is luck, and a `regime` command splits performance by market state
  (bull/bear, high/low vol). A research tool: output is not advice, and a backtest is a hypothesis
  fit to the past, not a forecast. See `backtest/README.md`.
- **`papertrade/`** — runs a strategy **forward** on live Yahoo data with **simulated** fills and
  a persistent account (`account.json`, `fills.jsonl`, `equity.jsonl` under a `--dir`). Commands:
  `init`, `step` (act on the newest unprocessed daily bar; idempotent per bar; records a daily
  marked-to-market equity snapshot even with no trade), `status` (marked to market), `summary`
  (performance over the tracked period vs a buy-and-hold benchmark, via `internal/perf`),
  `history`, `list` (multi-account overview under a `--root`), and `export` (equity/fills to CSV).
  Reuses copies of backtest's `strategy` and `yahoo` packages (kept in sync manually,
  per the isolated-module convention); a `broker.PaperBroker` mirrors the backtest cost model and
  buys are capped to available cash. Places **no real orders**. See `papertrade/README.md`.

## Commands

This is a `go.work` workspace. **Run all `go` commands from inside `itr-foreign/`** — `go ...
./...` from the repo root fails (`directory prefix . does not contain modules listed in
go.work`). `go` here is installed via **asdf** (`~/.asdf/shims/go`), so it's only on PATH in a
login shell; non-login shells may need the full shim path.

```sh
cd itr-foreign
go test ./...                          # all tests
go test -race ./...                    # what CI runs
go test ./internal/peak -run Compute   # a single package / test
go vet ./...
gofmt -l .                             # CI fails if this prints anything; gofmt -w . to fix
go build ./cmd/itrforeign
go run ./cmd/itrforeign fa --year 2026 --statement <file.xml> --rates <ttbr.csv> [--prices <p.csv>] [--entities <e.csv>]
go run ./cmd/itrforeign fetch-prices --year 2026 [--tickers <file>] [--out <file>]  # Yahoo daily closes → prices CSV
go run ./cmd/itrforeign fsi --fy 2025-26 --statement <file.xml> --rates <ttbr.csv> --tin <TIN> --marginal-rate 30
```

Golden tests (they lock the whole offline render path for **both** schedules — `report.*` for
FA and `report-fsi.*` + `schedule-fsi.json` for FSI): after an **intended** output change,
`go test ./internal/pipeline -update`, then review the diff of
`internal/pipeline/testdata/golden/*` before committing. Never blind-update.

CI: `.github/workflows/ci.yml` runs gofmt + vet + build + `test -race` on the module.

## Architecture

Pipeline (one stage per package, lower-level deps only):

```
                    ibkr (parse Flex XML / online pull)  →  model.Statement
                                        │
        ┌───────────────────────────────┴────────────────────────────────┐
        │ Schedule FA — CALENDAR year                 Schedule FSI/TR — FINANCIAL year
        │   fx (SBI TTBR at the event date)             rule115 (TTBR at the month end
        │   peak (per-security + true A2 NAV peak)        BEFORE the event; 128(8) for tax)
        │        ↓                                      gains (24-month term, 23-Jul-2024
        │   fa.Build (Tables A2 + A3)             split, per-leg vs net-gain FX)
        │                                                     ↓
        │                                              fsi.Build (country × head + TR + Form 67)
        └───────────────────────────────┬────────────────────────────────┘
                                 report (md/csv/json/html, + ITD JSON fragment)
```

`internal/itr` holds the lookups the ITR itself defines (country codes, IB entity metadata) and
is shared by both branches, so FA and FSI can never disagree about a country code.

- **`internal/pipeline.BuildReport`** (FA) and **`.BuildFSI`** (FSI) are the orchestration
  seams shared by the CLI and the golden tests. `cmd/itrforeign/main.go` does only I/O (load statement/rates/prices/entities,
  render, print); it must not re-implement pipeline logic. When adding pipeline steps, put
  them here, not in `main`.
- **`internal/model`** holds broker-agnostic domain types. **Money is always exact
  `math/big.Rat`, never float64** — every figure gets multiplied by an FX rate and is rounded
  only at the render step.

### Invariants that are easy to get wrong

- **Which year depends on the schedule.** Schedule FA covers the CALENDAR year Jan 1–Dec 31
  (the CLI enforces it; "closing" = 31-Dec). Schedule FSI/TR covers the Apr–Mar FINANCIAL year.
  `ibkr` windows on an `ibkr.Period`; `model.Statement.Year` is set **only** for a whole
  calendar year and is 0 otherwise, so FA code cannot silently consume an FY statement.
- **SBI TT *Buying* Rate (TTBR), not RBI.** `fx` reads the community "SBI FX RateKeeper" CSV
  format. Conversion uses the rate as on the relevant date with **preceding-working-day
  fallback** (and `TT BUY = 0.00` non-publish days are skipped). Every INR figure carries an
  `fx.Conversion` audit record (source amount, rate, actual rate date used). The conversion
  *date* per field (initial→lot date, closing→31-Dec, dividend→pay date, proceeds→sell date)
  is documented in `fa/build.go`.
- **Rule 115 ≠ the Schedule FA convention.** FA discloses assets at the TTBR of the *event
  date*. The income schedules compute income at the TTBR of the **last day of the month before**
  the event (31 March for "other income" such as broker interest), and foreign tax at the same
  month-end-before rule under **Rule 128(8)**. Both are correct in their own schedule and the
  rupee figures will differ — never "reconcile" them. See `internal/rule115`.
- **Foreign shares are not listed securities.** Long term only after **24 months** (not 12);
  s.111A and s.112A do not apply, so no ₹1.25 lakh exemption and no 20% STCG. LTCG is 12.5%
  without indexation from 23 Jul 2024, 20% with indexation before (separated and flagged, since
  indexation is not computed). Schedule CG rows **A5** (STCG) and **B8** (LTCG).
- **Capital-gains FX has two defensible methods** (`--cg-fx`): `per-leg` (default) and
  `net-gain`. They differ materially when the rupee has moved. The choice is always printed;
  never bury it.
- **Column (d) of Schedule FSI is an assumption, not a derivation.** "Tax payable in India"
  depends on the taxpayer's whole return, so it comes from `--marginal-rate`/`--surcharge`/
  `--cess` and is echoed in the report. Never guess it silently.
- **Peak value is maximised in INR**, not USD. Two modes: **C** (approximate, default — values
  at trade dates + 31-Dec close) and **B** (`--prices`, exact daily reconstruction). Mode B
  also yields a *true* Table A2 NAV peak; without it, A2 peak is a flagged upper-bound sum.
- **Country code = the ITR country-code list** (Ireland=353, **US=2**, Canada=1), not a serial
  number and *not* plain ISD codes: the list is ISD-derived, but since the US and Canada share
  ISD +1 the ITD assigns Canada 1 and the US 2. Authoritative source is the `enum` /
  `description` of `CountryCodeExcludingIndia` in the ITD's published ITR-2 JSON schema (the
  offline utility validates against it). The same enum backs Schedules FA, FSI and TR.
- **IBKR quirks already handled** (regression-tested — don't reintroduce): a single instrument
  can have multiple `OpenPosition` rows (SUMMARY + LOT, or several LOT rows) that must be
  **aggregated**; `vestingDate` can be a *future* lock-up date and must **not** be used as the
  acquisition date (use holding-period/open date); `levelOfDetail="CLOSED_LOT"` trade rows (and
  nested `<Lot>` elements) are the per-lot breakdown of a closing trade and must **not** also be
  counted as executions, or proceeds double; withholding is posted as a debit and is **negated**
  rather than abs'd so a later reversal nets it down; withholding that matches no distribution
  is kept in `UnmatchedWithholding` (dropping it forfeits the foreign tax credit).
- Review flags (`NeedsReview`) trip only on real data gaps (missing country/address, FX gaps,
  corporate actions) — not on the always-approximate mode-C peak.

## Data & privacy

- **Real IBKR exports and generated reports contain account numbers + holdings.** They live
  under `itr-foreign/private/` which is **gitignored — never commit them.** Caveat that already
  bit once: gitignore does **not** support inline comments; keep comments on their own line or
  a `private/   # ...` rule silently matches nothing.
- TTBR data (`data/ttbr/*.csv`) and prices (`data/prices/*.csv`) are third-party/user data,
  **not vendored** (gitignored); fetch via the README curl / `itrforeign fetch-prices` (Yahoo
  chart API). `data/entities/*.csv` (issuer address/ZIP/country-code overrides) **is** committed.
- Test fixtures are synthetic (e.g. account `U1234567`, "Jane Doe"). Keep them that way.

## Notes

- Module path `github.com/akagr/finance-tools/...` is a placeholder; if the real remote differs,
  update `itr-foreign/go.mod`, `go.work`, and the README CI badge.
- Output is **not tax advice** — every report is a draft to verify; keep the disclaimer and the
  audit trail intact so a professional can check every number.

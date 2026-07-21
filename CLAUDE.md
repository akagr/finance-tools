# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A monorepo of Go tools for Indian investors, all zero-dependency (Go stdlib only), Go 1.26,
in one `go.work` workspace:

- **`schedule-fa/`** — a CLI that turns Interactive Brokers (IBKR) holdings into a **Schedule
  FA** (Foreign Assets) report for the Indian ITR. The most developed tool; most of this doc
  is about it.
- **`correlation/`** — computes return **correlations** across assets to gauge portfolio
  diversification (CSV/Yahoo-driven, md/csv/json output).
- **`backtest/`** — an offline **backtester** for rule-based strategies on NSE daily data
  (trend, momentum, mean-reversion and breakout rules vs a buy-and-hold benchmark, realistic
  costs). Each strategy lives in its own file under `internal/strategy/` and is registered in
  `pipeline.buildStrategy`; `--strategy all` compares them in one table, `--sort` ranks it, and
  `--vol-target` adds a volatility-targeting position-sizing overlay (the benchmark stays
  unscaled). A `walkforward` command splits history into out-of-sample folds to check an edge is
  consistent, not a lucky stretch. A research tool: output is not advice, and a backtest is a
  hypothesis fit to the past, not a forecast. See `backtest/README.md`.

## Commands

This is a `go.work` workspace. **Run all `go` commands from inside `schedule-fa/`** — `go ...
./...` from the repo root fails (`directory prefix . does not contain modules listed in
go.work`). `go` here is installed via **asdf** (`~/.asdf/shims/go`), so it's only on PATH in a
login shell; non-login shells may need the full shim path.

```sh
cd schedule-fa
go test ./...                          # all tests
go test -race ./...                    # what CI runs
go test ./internal/peak -run Compute   # a single package / test
go vet ./...
gofmt -l .                             # CI fails if this prints anything; gofmt -w . to fix
go build ./cmd/schedulefa
go run ./cmd/schedulefa generate --year 2026 --statement <file.xml> --rates <ttbr.csv> [--prices <p.csv>] [--entities <e.csv>]
go run ./cmd/schedulefa fetch-prices --year 2026 [--tickers <file>] [--out <file>]  # Yahoo daily closes → prices CSV
```

Golden test (locks the whole offline render path): after an **intended** output change,
`go test ./internal/pipeline -update`, then review the diff of
`internal/pipeline/testdata/golden/*` before committing. Never blind-update.

CI: `.github/workflows/ci.yml` runs gofmt + vet + build + `test -race` on the module.

## Architecture

Pipeline (one stage per package, lower-level deps only):

```
ibkr (parse Flex XML / online pull)  →  model.Statement
        ↓                fx (SBI TTBR → INR, audit)
   peak (per-security peak + true A2 NAV peak)
        ↓
   schedulefa.Build (Tables A2 + A3)  →  report (md/csv/json/html)
```

- **`internal/pipeline.BuildReport`** is the orchestration seam shared by the CLI and the
  golden test. `cmd/schedulefa/main.go` does only I/O (load statement/rates/prices/entities,
  render, print); it must not re-implement pipeline logic. When adding pipeline steps, put
  them here, not in `main`.
- **`internal/model`** holds broker-agnostic domain types. **Money is always exact
  `math/big.Rat`, never float64** — every figure gets multiplied by an FX rate and is rounded
  only at the render step.

### Invariants that are easy to get wrong

- **Calendar year, not financial year.** Schedule FA covers Jan 1–Dec 31. The CLI enforces a
  calendar year; `ibkr` drops dated records outside it; "closing" = 31-Dec.
- **SBI TT *Buying* Rate (TTBR), not RBI.** `fx` reads the community "SBI FX RateKeeper" CSV
  format. Conversion uses the rate as on the relevant date with **preceding-working-day
  fallback** (and `TT BUY = 0.00` non-publish days are skipped). Every INR figure carries an
  `fx.Conversion` audit record (source amount, rate, actual rate date used). The conversion
  *date* per field (initial→lot date, closing→31-Dec, dividend→pay date, proceeds→sell date)
  is documented in `schedulefa/build.go`.
- **Peak value is maximised in INR**, not USD. Two modes: **C** (approximate, default — values
  at trade dates + 31-Dec close) and **B** (`--prices`, exact daily reconstruction). Mode B
  also yields a *true* Table A2 NAV peak; without it, A2 peak is a flagged upper-bound sum.
- **Country code = ISD telephone code** (US=1, Ireland=353), not a serial number.
- **IBKR quirks already handled** (regression-tested — don't reintroduce): a single instrument
  can have multiple `OpenPosition` rows (SUMMARY + LOT, or several LOT rows) that must be
  **aggregated**; `vestingDate` can be a *future* lock-up date and must **not** be used as the
  acquisition date (use holding-period/open date).
- Review flags (`NeedsReview`) trip only on real data gaps (missing country/address, FX gaps,
  corporate actions) — not on the always-approximate mode-C peak.

## Data & privacy

- **Real IBKR exports and generated reports contain account numbers + holdings.** They live
  under `schedule-fa/private/` which is **gitignored — never commit them.** Caveat that already
  bit once: gitignore does **not** support inline comments; keep comments on their own line or
  a `private/   # ...` rule silently matches nothing.
- TTBR data (`data/ttbr/*.csv`) and prices (`data/prices/*.csv`) are third-party/user data,
  **not vendored** (gitignored); fetch via the README curl / `schedulefa fetch-prices` (Yahoo
  chart API). `data/entities/*.csv` (issuer address/ZIP/ISD-code overrides) **is** committed.
- Test fixtures are synthetic (e.g. account `U1234567`, "Jane Doe"). Keep them that way.

## Notes

- Module path `github.com/akagr/finance-tools/...` is a placeholder; if the real remote differs,
  update `schedule-fa/go.mod`, `go.work`, and the README CI badge.
- Output is **not tax advice** — every report is a draft to verify; keep the disclaimer and the
  audit trail intact so a professional can check every number.

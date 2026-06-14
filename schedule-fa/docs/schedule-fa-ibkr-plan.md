# Schedule FA Generator from IBKR Holdings — Research & Plan

A Go CLI that ingests Interactive Brokers (IBKR) data and produces a ready-to-use
**Schedule FA** (Foreign Assets) report for the Indian Income Tax Return (ITR-2 / ITR-3).

> **Status:** planning / research. Nothing in `docs/` is tax advice. The generated
> report is a *working draft* to hand to a CA or to transcribe into the ITR utility —
> the taxpayer remains responsible for correctness.

---

## 1. What Schedule FA actually requires

Schedule FA is the disclosure of foreign assets that a **Resident and Ordinarily
Resident (ROR)** individual must file. It is *disclosure*, not taxation — but omission
carries penalties under the **Black Money (Undisclosed Foreign Income and Assets) Act,
2015** (penalty up to ₹10 lakh per year), so accuracy matters.

### 1.1 The reporting period is the CALENDAR year, not the financial year

This is the single most important and most error-prone rule.

- For **AY 2025-26 (FY 2024-25)**, Schedule FA covers the **calendar year 1-Jan-2024 to
  31-Dec-2024** ("the relevant accounting period").
- "Closing value" = value as on **31-Dec**, not 31-Mar.
- This mismatch between the FA period (calendar year) and the rest of the ITR (financial
  year) is a primary source of confusion and the main reason a dedicated tool helps.

### 1.2 Which tables apply to IBKR

| Table                | Title                            | Applies to IBKR?                                                                                          |
|----------------------|----------------------------------|-----------------------------------------------------------------------------------------------------------|
| **A2**               | Foreign Custodial Account        | **Yes** — the IBKR brokerage account itself is a custodial account. One row for the account.              |
| **A3**               | Foreign Equity and Debt Interest | **Yes** — one row **per security** held at any time during the calendar year (stocks, ETFs, RSUs, bonds). |
| A1                   | Foreign Depository Account       | Only if you hold an IBKR cash/bank-like depository balance (usually report cash under A2).                |
| D / table for income | Income from foreign sources      | Dividends/interest may also surface here depending on form.                                               |

> **Open design question:** Most filers report each holding in **A3** and *also* report the
> overall account in **A2**. We must decide whether the tool emits A2, A3, or both.
> Recommended: emit **both**, with A2 as the account-level summary and A3 as the
> per-security detail. See §6.

### 1.3 Table A3 columns (per security)

The generator must produce, per security held *at any time* in the calendar year:

1. Country name & **country code** (e.g. United States / 2 — ISO/ITR code list).
2. Name of entity (issuer, e.g. "Apple Inc").
3. Address of entity.
4. ZIP code.
5. Nature of entity (e.g. "Listed company / ETF").
6. **Date of acquiring the interest** (first acquisition date of the holding).
7. **Initial value of the investment** (cost at acquisition), in **INR**.
8. **Peak value of investment during the period**, in **INR**.
9. **Closing value** (as on 31-Dec), in **INR**.
10. **Total gross amount paid/credited** with respect to the holding during the period
    (i.e. **dividends**), in **INR**.
11. **Total gross proceeds from sale/redemption** during the period, in **INR**.

### 1.4 Table A2 columns (the account)

Institution name (Interactive Brokers LLC), address, ZIP, country, **account number**,
status, account opening date, **peak balance**, **closing balance**, and gross
interest/dividend credited during the period — all in INR.

---

## 2. Currency conversion — the rule that drives the whole design

All values are reported in **INR**, converted using the **SBI TT Buying Rate (TTBR)** —
the Telegraphic Transfer Buying Rate published by State Bank of India. This is mandated
(Rule 115 / the FA instructions), **not** optional and **not** the RBI reference rate.

The rate to use depends on the field:

| Field           | Convert using TTBR as on…                                              |
|-----------------|------------------------------------------------------------------------|
| Initial value   | date of acquisition of each lot                                        |
| Peak value      | the date on which the peak (in INR terms) occurred                     |
| Closing value   | 31-Dec (or last working day with a published rate)                     |
| Dividend income | date credited (commonly approximated to 31-Dec closing rate; see §5.3) |
| Sale proceeds   | date of sale                                                           |

**Subtlety:** the peak must be computed in **INR**, i.e. `units × price(USD) × TTBR` on
each day, and the max taken over the calendar year — *not* the USD peak converted once.
In practice the USD peak and INR peak usually fall on the same/near date, but they can
diverge when the rupee moves sharply. The tool should compute it correctly (daily INR
series) and document the assumption.

---

## 3. Where the data comes from — IBKR

IBKR exposes everything we need through **Flex Queries** and the **Flex Web Service**.

### 3.1 Flex Web Service (programmatic)

Two-step, token-based, XML/CSV over HTTPS (no full OAuth):

1. **SendRequest** — `GET https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService/SendRequest?t=<token>&q=<queryId>&v=3`
   → returns a numeric **ReferenceCode**.
2. **GetStatement** — `GET .../FlexWebService/GetStatement?t=<token>&q=<referenceCode>&v=3`
   → returns the statement (poll until ready; IBKR returns a "generation in progress"
   code briefly).

The user creates an **Activity Flex Query** in Client Portal and a Flex Web Service
**token**, then passes both to the tool. We will support **XML output** (cleanest to
parse) and also accept a **manually downloaded statement file** (offline mode).

### 3.2 Flex Query sections we need

- **Open Positions** (with lots) — for closing holdings at 31-Dec.
- **Trades / Lots** — acquisition dates and costs; sale proceeds and dates.
- **Cash Transactions** — **Dividends** and **Payment-in-lieu / Withholding tax**.
- **Financial Instrument Info** — symbol → issuer name, asset class, ISIN, listing country.
- **Account Information** — account number, name, base currency, account open date.

> The statement must be pulled for the **calendar year (Jan 1 – Dec 31)**. IBKR statements
> are date-ranged by request; the tool must enforce the calendar-year window even though
> the user thinks in financial years.

### 3.3 The hard part: per-day position history for peak value

Open Positions gives you *year-end* holdings. The **peak** needs the value of each
holding on **every day** it was held. Options:

- **(A) Daily positions Flex Query** — IBKR can emit a daily "Open Positions" snapshot if
  configured; heavy but exact.
- **(B) Reconstruct positions from trades** — start from year-open position (from prior
  year statement or the Jan-1 snapshot) and walk trades forward to get a daily share-count
  series, then multiply by a daily price series. Needs an external price source.
- **(C) Approximate** — peak ≈ max(closing value, max trade-day value). Cheap, defensible
  for buy-and-hold, but **not exact** and must be clearly labelled.

**Recommendation:** Start with **(B)** for share counts (deterministic from IBKR data) +
a pluggable **daily price provider**; fall back to **(C)** with a visible warning when
prices are unavailable. See §5 challenges.

---

## 4. The other hard part: SBI TTBR historical rates

There is **no official free SBI TTBR API**. SBI publishes a daily "FOREX CARD RATES" PDF;
historical archives are not cleanly downloadable. Strategy:

- Ship a **bundled CSV** of historical USD (and EUR/GBP/etc.) TTBR rates, dated, that the
  user can extend.
- Allow a **user-supplied rates CSV** override (`--rates rates.csv`).
- **Holiday / missing-day rule:** if no rate is published for a date (weekend/holiday),
  use the rate of the **immediately preceding** working day. Implement this lookup
  explicitly and log which fallback date was used.
- Provide a small fetcher/updater as a *separate* concern (best-effort scraper of a known
  public mirror), kept out of the core path so a broken upstream never blocks report
  generation.

This is the component most likely to need manual review by the user, so the report must
show **the rate and date used for every converted figure** (audit trail), not just the
final INR number.

---

## 5. Challenges & risks (ranked)

1. **Peak value computation** (§3.3) — needs daily position × daily price × daily TTBR.
   Biggest source of inaccuracy. Mitigation: reconstruct shares from trades; pluggable
   price source; explicit approximation mode with warnings.
2. **SBI TTBR historical data** (§4) — no official API. Mitigation: bundled + user CSV +
   preceding-working-day fallback + full audit trail.
3. **Calendar-year vs financial-year** (§1.1) — easy to get wrong silently. Mitigation:
   the tool takes a *calendar year* as input and refuses FY-shaped ranges.
4. **Lot / cost-basis accuracy** — acquisition date & "initial value" per holding;
   multiple lots, partial sales, FIFO. Mitigation: consume IBKR lot data directly rather
   than recomputing FIFO where possible.
5. **Corporate actions** — splits, symbol changes, mergers, spin-offs, stock dividends
   distort share counts and cost basis. Mitigation: consume IBKR's CorporateActions
   section; flag anything unrecognised for manual review.
6. **RSUs / vesting / ESPP** — "date of acquiring interest" = vesting date; cost basis
   nuances. Often the actual user need. Mitigation: handle vesting events as acquisitions.
7. **Dividend gross vs net of withholding tax** — Schedule FA wants **gross** dividend.
   IBKR shows the dividend and the 25% US withholding separately. Mitigation: sum gross,
   keep withholding visible (also relevant for Schedule TR / FTC, out of scope for v1).
8. **Entity metadata** (issuer address, ZIP, nature, country code) — IBKR gives symbol &
   ISIN but not a tidy address. Mitigation: maintain a small reference table keyed by
   ISIN/symbol; default US exchange-listed → United States; allow user overrides.
9. **Closing value source** — 31-Dec mark price. IBKR statement carries year-end mark;
   for mid-year-exited positions closing value is 0 (but they still appear in A3 with
   peak + proceeds). Mitigation: include exited positions; closing = 0.
10. **Multiple accounts / joint holdings / base currency ≠ USD** — Mitigation: per-account
    processing; convert from each instrument's currency, not just USD.
11. **Not tax advice / liability** — Mitigation: prominent disclaimers; audit trail so a
    professional can verify every number.

---

## 6. Output

- **Human-readable report**: a printable table (Markdown/HTML/PDF) laid out exactly like
  Schedule FA Table A2 and A3, in INR, **with a companion audit sheet** showing the
  USD figure, the TTBR, and the rate date behind every INR value.
- **Machine-readable**: CSV/JSON matching the ITR utility's import schema where one
  exists, so values can be pasted/imported into the income-tax e-filing utility.
- **Reconciliation summary**: totals, count of holdings, any rows flagged for manual
  review (corporate actions, missing prices, missing rates).

---

## 7. Proposed architecture (Go)

```
tax-tools/
  cmd/schedulefa/        # CLI entrypoint (stdlib flag + subcommand router; no external deps in v1)
  internal/
    ibkr/                # Flex Web Service client + XML statement parser
    model/               # domain types: Account, Lot, Trade, Dividend, Holding
    fx/                  # TTBR rate store, lookup w/ preceding-day fallback
    prices/              # daily price provider interface + impls (IBKR-derived, external)
    peak/                # daily position reconstruction + peak value engine
    schedulefa/          # build A2/A3 rows from holdings + fx
    report/              # renderers: markdown, csv, json, (later) pdf/html
  data/
    ttbr/                # bundled historical SBI TTBR CSVs
    entities/            # ISIN/symbol -> issuer name, address, country code
  docs/
    schedule-fa-ibkr-plan.md   # this file
```

### Data flow

```
IBKR Flex (XML)  ──parse──▶  model.{Trades, Positions, Dividends, Account}
                                   │
        ┌──────────────────────────┼─────────────────────────────┐
        ▼                          ▼                              ▼
 peak: daily shares × price   acquisition lots            dividends (gross)
        │                          │                              │
        └────────── fx (TTBR by date, INR) ────────────────────────┘
                                   │
                                   ▼
                    schedulefa.Build → A2 + A3 rows (INR + audit)
                                   │
                                   ▼
                 report: markdown / csv / json (+ audit sheet)
```

### CLI sketch

```
schedulefa generate \
  --year 2024 \                       # CALENDAR year (enforced)
  --flex-token <t> --flex-query <q> \ # OR --statement statement.xml
  --rates data/ttbr/usd-2024.csv \    # optional override
  --prices prices-2024.csv \          # optional daily prices for exact peak
  --out report/ --format md,csv,json
```

---

## 8. Milestones

- **M0 — Skeleton:** ✅ repo, CLI scaffold, domain model, disclaimers.
- **M1 — IBKR ingest:** ✅ parse a downloaded Activity Flex **XML** (offline): account,
  open positions (+ lot detail), trades, dividends (with withholding matched), securities
  info. Year-constrained. Tolerant decode + flexible date parsing. Test in
  `internal/ibkr/ibkr_test.go` against `testdata/sample_flex.xml`; `generate` prints a
  parse summary.
- **M2 — FX engine:** TTBR CSV store + preceding-working-day fallback + audit records.
- **M3 — A3 (buy & hold):** closing value, initial value, dividends, proceeds in INR with
  approximate peak (mode C). End-to-end report for the simple case.
- **M4 — Exact peak:** daily share reconstruction from trades + pluggable price provider
  (mode B). Mode C stays as labelled fallback.
- **M5 — A2 + edge cases:** custodial account row; exited positions; corporate actions &
  RSU handling; entity metadata table; manual-review flags.
- **M6 — Flex Web Service:** online pull (SendRequest/GetStatement, polling).
- **M7 — Renderers & UX:** HTML/PDF, reconciliation summary, README, sample data.

---

## 9. Decisions — LOCKED (2026-06-14)

1. **Emit both A2 and A3.** A2 = account-level custodial summary, A3 = per-security detail.
2. **Peak: approximate (mode C) for v1**, exact daily reconstruction (mode B) in M4. Mode C
   output is always labelled "approximate" with a manual-review flag.
3. **Offline downloaded statement (XML) first**; Flex Web Service online pull deferred to M6.
4. **Scope = Schedule FA only for v1.** `fx` and `model` packages designed to be reused by
   future tools (Schedule TR/FSI/CG) but those are out of scope now.
5. **USD-first**, but `Currency` is plumbed through every value and the `fx` store is
   keyed by currency from day one, so adding EUR/GBP/etc. is data-only, not code.

### Repo shape (locked)

This repo is a **monorepo of tax tools**. Each tool is an isolated Go module under its own
top-level directory, tied together by a root `go.work`:

```
tax-tools/
  go.work                 # workspace tying all tool modules together
  README.md               # repo index
  schedule-fa/            # THIS tool (module github.com/akagr/tax-tools/schedule-fa)
    go.mod
    docs/  cmd/  internal/  data/
  <future-tool>/          # e.g. schedule-cg/, form-67/ …
```

> Module path `github.com/akagr/tax-tools/schedule-fa` is a placeholder derived from the
> account name; change it in `go.mod` + `go.work` if the real remote differs.

---

## 10. Sources

- [Schedule FA (Foreign Assets) Disclosure in ITR — Quicko](https://learn.quicko.com/schedule-foreign-assets-in-income-tax-return)
- [Understanding Schedule FA — Taxmann](https://www.taxmann.com/post/blog/understanding-schedule-fa-guide-on-disclosing-foreign-assets-income-in-itr-filing-for-indian-residents/)
- [Schedule FA under the Black Money Law — Taxmann](https://www.taxmann.com/post/blog/understand-schedule-fa-in-itr-forms-and-implications-under-the-black-money-law/)
- [Resident Indians Holding U.S. Stocks — ClearTax](https://cleartax.in/s/resident-indians-should-disclose-us-stocks-in-itr)
- [Schedule FA disclosure — Tax2win](https://tax2win.in/guide/schedule-fa-disclosure-of-foreign-assets-in-itr)
- [Schedule FA step-by-step AY 2025-26 — Endovia Wealth](https://www.endoviawealth.com/how-to-declare-foreign-assets-in-itr-schedule-fa-step-by-step-guide-ay-2025-26/)
- [What the SBI TTBR rate is — Rovia](https://www.rovia.one/resources/taxation/what-the-sbi-ttbr-rate-is-and-why-it-matters-for-your-taxes)
- [SBI TTBR rate explained — Paasa](https://paasa.com/blog/sbi-ttbr-rate-explained)
- [Flex Web Service — IBKR Campus](https://www.interactivebrokers.com/campus/ibkr-api-page/flex-web-service/)
- [Flex Web Service Version 3 — IBKR docs](https://www.ibkrguides.com/complianceportal/complianceportal/flexweb3.htm)
- [Flex Query Output Format — IBKR Campus](https://www.interactivebrokers.com/campus/glossary-terms/flex-query-output-format/)
- [IBKR Flex Query Setup — TrackYourPortfol.io](https://trackyourportfol.io/blog/ibkr-flex-query-setup)
</content>
</invoke>

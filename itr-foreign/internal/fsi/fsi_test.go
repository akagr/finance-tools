package fsi

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akagr/finance-tools/itr-foreign/internal/entities"
	"github.com/akagr/finance-tools/itr-foreign/internal/fx"
	"github.com/akagr/finance-tools/itr-foreign/internal/gains"
	"github.com/akagr/finance-tools/itr-foreign/internal/model"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func testStore(t *testing.T, rates map[string]string) fx.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SBI_REFERENCE_RATES_USD.csv")
	csv := "DATE,PDF FILE,TT BUY,TT SELL\n"
	for d, r := range rates {
		csv += d + " 09:00,x," + r + ",99.00\n"
	}
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	store := fx.NewCSVStore()
	if err := store.LoadRateKeeperFile(model.USD, path); err != nil {
		t.Fatal(err)
	}
	return store
}

func opts() Options {
	return Options{
		FYStart:      2025,
		TIN:          "XXXXX1234X",
		MarginalRate: big.NewRat(30, 1),
		Surcharge:    new(big.Rat),
		Cess:         big.NewRat(4, 1),
		CGMethod:     gains.PerLeg,
	}
}

func headOf(c CountryRow, h Head) HeadRow {
	for _, r := range c.Heads {
		if r.Head == h {
			return r
		}
	}
	panic("no such head: " + h)
}

func usStock() model.Instrument {
	return model.Instrument{Symbol: "AAA", ISIN: "US0000000001", ListingCtry: "US"}
}

// Relief is the LOWER of the foreign tax and the Indian tax on the same income —
// the whole point of the schedule, and the figure Schedule TR carries forward.
func TestReliefIsTheLowerOfTheTwoTaxes(t *testing.T) {
	store := testStore(t, map[string]string{"2025-07-31": "80.00", "2026-03-31": "80.00"})

	// US withholding is 25% but the Indian slab assumption here is 30%, so the
	// Indian tax is higher and the credit is capped at the foreign tax.
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		Dividends: []model.Dividend{{
			Instrument: usStock(), PayDate: day("2025-08-14"),
			Gross:       model.NewMoney(model.USD, big.NewRat(100, 1)),
			Withholding: model.NewMoney(model.USD, big.NewRat(25, 1)),
		}},
	}
	rep, err := Build(st, store, opts())
	if err != nil {
		t.Fatal(err)
	}
	oth := headOf(rep.Countries[0], HeadOtherSources)
	if got, want := oth.Income.FloatString(2), "8000.00"; got != want {
		t.Errorf("income = %s, want %s (GROSS of withholding)", got, want)
	}
	if got, want := oth.TaxPaidOutside.FloatString(2), "2000.00"; got != want {
		t.Errorf("tax paid outside = %s, want %s", got, want)
	}
	// 8000 × 30% = 2400, plus 4% cess = 2496.
	if got, want := oth.TaxPayableIndia.FloatString(2), "2496.00"; got != want {
		t.Errorf("tax payable in India = %s, want %s", got, want)
	}
	if got, want := oth.Relief.FloatString(2), "2000.00"; got != want {
		t.Errorf("relief = %s, want %s (the lower of the two)", got, want)
	}
	if got, want := oth.DTAAArticle, ArticleDividends; got != want {
		t.Errorf("DTAA article = %q, want %q", got, want)
	}

	// And the other way round: a low Indian rate caps the credit, with no
	// carry-forward of the excess.
	o := opts()
	o.MarginalRate = big.NewRat(5, 1)
	rep, err = Build(st, store, o)
	if err != nil {
		t.Fatal(err)
	}
	oth = headOf(rep.Countries[0], HeadOtherSources)
	if got, want := oth.Relief.FloatString(2), "416.00"; got != want {
		t.Errorf("relief = %s, want %s (capped at the Indian tax)", got, want)
	}
	if rep.TRRelief.Cmp(oth.Relief) != 0 {
		t.Errorf("Schedule TR relief %s should equal the FSI relief %s",
			rep.TRRelief.FloatString(2), oth.Relief.FloatString(2))
	}
}

// Surcharge on s.112 long-term gains is capped at 15% even when the taxpayer's
// slab surcharge is higher.
func TestSurchargeCappedOnLongTermGains(t *testing.T) {
	store := testStore(t, map[string]string{"2022-12-31": "80.00", "2025-07-31": "80.00"})
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		RealizedLots: []model.RealizedLot{{
			Instrument: usStock(),
			OpenDate:   day("2023-01-10"), CloseDate: day("2025-08-20"),
			Quantity: big.NewRat(10, 1),
			Cost:     model.NewMoney(model.USD, big.NewRat(1000, 1)),
			Proceeds: model.NewMoney(model.USD, big.NewRat(2000, 1)),
		}},
	}
	o := opts()
	o.Surcharge = big.NewRat(37, 1) // the top slab surcharge
	rep, err := Build(st, store, o)
	if err != nil {
		t.Fatal(err)
	}
	cg := headOf(rep.Countries[0], HeadCapitalGains)
	// Gain 80000 × 12.5% = 10000; surcharge capped at 15% = 1500; cess 4% = 460.
	if got, want := cg.TaxPayableIndia.FloatString(2), "11960.00"; got != want {
		t.Errorf("LTCG tax = %s, want %s (surcharge capped at 15%%)", got, want)
	}
}

// A loss is not "income included in Part B-TI": it must not appear in column (b)
// as a negative, and the taxpayer must be told where it does belong.
func TestCapitalLossExcludedFromIncomeAndFlagged(t *testing.T) {
	store := testStore(t, map[string]string{"2024-12-31": "80.00", "2025-07-31": "80.00"})
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		RealizedLots: []model.RealizedLot{{
			Instrument: usStock(),
			OpenDate:   day("2025-01-10"), CloseDate: day("2025-08-20"),
			Quantity: big.NewRat(10, 1),
			Cost:     model.NewMoney(model.USD, big.NewRat(2000, 1)),
			Proceeds: model.NewMoney(model.USD, big.NewRat(1500, 1)),
		}},
	}
	rep, err := Build(st, store, opts())
	if err != nil {
		t.Fatal(err)
	}
	c := rep.Countries[0]
	cg := headOf(c, HeadCapitalGains)
	if cg.Income.Sign() != 0 {
		t.Errorf("column (b) = %s, want 0 — a loss is not income", cg.Income.FloatString(2))
	}
	if cg.TaxPayableIndia.Sign() != 0 {
		t.Errorf("tax payable on a loss = %s, want 0", cg.TaxPayableIndia.FloatString(2))
	}
	if !strings.Contains(c.ReviewNote, "set-off") {
		t.Errorf("review note should point at the s.70/71 set-off rules, got %q", c.ReviewNote)
	}
	// The tie-out still carries the real (negative) figure for Schedule CG.
	if rep.TieOut.ScheduleCGA5.Sign() >= 0 {
		t.Errorf("Schedule CG tie-out = %s, want the actual loss", rep.TieOut.ScheduleCGA5.FloatString(2))
	}
}

// Income is sourced where the ISSUER is, not where the security is listed: a
// US-listed Irish UCITS ETF is Irish-source, and an entity override wins.
func TestSourceCountryFollowsTheIssuer(t *testing.T) {
	store := testStore(t, map[string]string{"2025-07-31": "80.00", "2026-03-31": "80.00"})
	dir := t.TempDir()
	path := filepath.Join(dir, "entities.csv")
	csv := "isin,symbol,entity_name,address,zip,country_code,nature\n" +
		"US9999999999,ADR,Some ADR,,,353,Fund\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	ents, err := entities.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		Dividends: []model.Dividend{{
			// A US ISIN, but the override says the issuer is Irish.
			Instrument: model.Instrument{Symbol: "ADR", ISIN: "US9999999999", ListingCtry: "US"},
			PayDate:    day("2025-08-14"),
			Gross:      model.NewMoney(model.USD, big.NewRat(100, 1)),
		}},
	}
	o := opts()
	o.Entities = ents
	rep, err := Build(st, store, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Countries) != 1 {
		t.Fatalf("countries = %d, want 1", len(rep.Countries))
	}
	if got, want := rep.Countries[0].CountryCode, "353"; got != want {
		t.Errorf("country code = %s, want %s (the issuer's, not the listing venue's)", got, want)
	}
	if got, want := rep.Countries[0].CountryName, "Ireland"; got != want {
		t.Errorf("country name = %s, want %s", got, want)
	}
}

// A missing TIN would be rejected by the ITR utility, so it has to be flagged
// rather than silently left blank.
func TestMissingTINIsFlagged(t *testing.T) {
	store := testStore(t, map[string]string{"2025-07-31": "80.00", "2026-03-31": "80.00"})
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		Dividends: []model.Dividend{{
			Instrument: usStock(), PayDate: day("2025-08-14"),
			Gross: model.NewMoney(model.USD, big.NewRat(100, 1)),
		}},
	}
	o := opts()
	o.TIN = ""
	rep, err := Build(st, store, o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Countries[0].ReviewNote, "identification number") {
		t.Errorf("a missing TIN must be flagged, got %q", rep.Countries[0].ReviewNote)
	}
}

// Interest paid is an expense, not negative income: netting it silently against
// dividends would understate the income offered to tax.
func TestInterestPaidIsExcludedNotNetted(t *testing.T) {
	store := testStore(t, map[string]string{"2026-03-31": "80.00"})
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		Account: model.Account{IBEntity: "IBLLC-US"},
		Interest: []model.Interest{
			{Date: day("2025-12-31"), Amount: model.NewMoney(model.USD, big.NewRat(50, 1))},
			{Date: day("2026-01-15"), Amount: model.NewMoney(model.USD, big.NewRat(-20, 1))},
		},
	}
	rep, err := Build(st, store, opts())
	if err != nil {
		t.Fatal(err)
	}
	c := rep.Countries[0]
	if got, want := c.CountryCode, "2"; got != want {
		t.Errorf("broker interest country = %s, want %s (the broker entity's)", got, want)
	}
	if got, want := headOf(c, HeadOtherSources).Income.FloatString(2), "4000.00"; got != want {
		t.Errorf("interest income = %s, want %s (the debit excluded, not netted)", got, want)
	}
	if !strings.Contains(c.ReviewNote, "s.57") {
		t.Errorf("the excluded interest expense must be surfaced, got %q", c.ReviewNote)
	}
}

func TestFYAndAYLabels(t *testing.T) {
	r := &Report{FYStart: 2025}
	if got, want := r.FYLabel(), "2025-26"; got != want {
		t.Errorf("FY label = %s, want %s", got, want)
	}
	if got, want := r.AYLabel(), "2026-27"; got != want {
		t.Errorf("AY label = %s, want %s", got, want)
	}
	r = &Report{FYStart: 2099}
	if got, want := r.FYLabel(), "2099-00"; got != want {
		t.Errorf("FY label at the century boundary = %s, want %s", got, want)
	}
}

// A security that paid several substitute dividends, or several debit-interest
// rows, must not repeat the same warning once per transaction — the review note
// is meant to be read.
func TestReviewNotesAreNotRepeated(t *testing.T) {
	store := testStore(t, map[string]string{
		"2025-07-31": "80.00", "2025-08-29": "80.00", "2025-10-31": "80.00", "2026-03-31": "80.00",
	})
	pil := func(d string) model.Dividend {
		return model.Dividend{
			Instrument: model.Instrument{Symbol: "IBKR", ISIN: "US45841N1072", ListingCtry: "US"},
			PayDate:    day(d),
			Gross:      model.NewMoney(model.USD, big.NewRat(10, 1)),
			Kind:       model.DividendInLieu,
		}
	}
	st := &model.Statement{
		From: day("2025-04-01"), To: day("2026-03-31"),
		Account:   model.Account{IBEntity: "IBLLC-US"},
		Dividends: []model.Dividend{pil("2025-08-14"), pil("2025-09-05"), pil("2025-11-20")},
		Interest: []model.Interest{
			{Date: day("2025-08-14"), Amount: model.NewMoney(model.USD, big.NewRat(-3, 1))},
			{Date: day("2025-09-05"), Amount: model.NewMoney(model.USD, big.NewRat(-4, 1))},
		},
	}
	rep, err := Build(st, store, opts())
	if err != nil {
		t.Fatal(err)
	}
	note := rep.Countries[0].ReviewNote

	if n := strings.Count(note, "payment in lieu of dividend"); n != 1 {
		t.Errorf("payment-in-lieu warning appears %d times, want 1:\n%s", n, note)
	}
	if n := strings.Count(note, "interest PAID"); n != 1 {
		t.Errorf("interest-paid warning appears %d times, want 1:\n%s", n, note)
	}
	// The aggregated interest note should carry the total, not one row's amount.
	if !strings.Contains(note, "7") {
		t.Errorf("interest-paid note should total the debits (7 USD):\n%s", note)
	}
}

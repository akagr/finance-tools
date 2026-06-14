package schedulefa

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/entities"
	"github.com/akagr/tax-tools/schedule-fa/internal/fx"
	"github.com/akagr/tax-tools/schedule-fa/internal/model"
	"github.com/akagr/tax-tools/schedule-fa/internal/peak"
)

type fakeStore struct{ rate *big.Rat }

func (f fakeStore) RateOn(cur model.Currency, d time.Time) (fx.Rate, error) {
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return fx.Rate{Currency: cur, Date: day, INRPerUnit: f.rate}, nil
}

func usd(v int64) model.Money { return model.NewMoney(model.USD, big.NewRat(v, 1)) }
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestBuildA3(t *testing.T) {
	inst := model.Instrument{Symbol: "AAA", ISIN: "X1", Name: "Alpha Inc", AssetClass: "STK", ListingCtry: "US", Currency: model.USD}
	yearEnd := day(2024, 12, 31)
	st := &model.Statement{
		Year:          2024,
		OpenPositions: []model.Position{{Instrument: inst, Date: yearEnd, Quantity: big.NewRat(10, 1), MarkPrice: usd(150)}},
		Lots:          []model.Lot{{Instrument: inst, OpenDate: day(2024, 3, 15), Quantity: big.NewRat(10, 1), CostBasis: usd(1000)}},
		Trades:        []model.Trade{{Instrument: inst, Date: day(2024, 3, 15), Side: model.Buy, Quantity: big.NewRat(10, 1), Price: usd(100)}},
		Dividends:     []model.Dividend{{Instrument: inst, PayDate: day(2024, 6, 13), Gross: usd(25)}},
	}
	store := fakeStore{big.NewRat(80, 1)}
	peaks, err := peak.Compute(st, store, peak.ModeApprox, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Build(st, store, peaks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.A3) != 1 {
		t.Fatalf("A3 rows = %d, want 1", len(rep.A3))
	}
	row := rep.A3[0]

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"entity", row.EntityName, "Alpha Inc"},
		{"country", row.CountryName, "United States of America"},
		{"country code", row.CountryCode, "2"},
		{"nature", row.NatureEntity, "Listed equity share"},
		{"acquired", row.AcquiredOn, "2024-03-15"},
		{"initial", row.InitialValue.INR.Amount.RatString(), "80000"},  // 1000 * 80
		{"peak", row.PeakValue.INR.Amount.RatString(), "120000"},       // 1500 * 80
		{"closing", row.ClosingValue.INR.Amount.RatString(), "120000"}, // 1500 * 80
		{"dividend", row.GrossDividend.INR.Amount.RatString(), "2000"}, // 25 * 80
		{"proceeds", row.SaleProceeds.INR.Amount.RatString(), "0"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	// Without entity metadata, the missing address/ZIP trips the review flag.
	if !row.NeedsReview || !strings.Contains(row.ReviewNote, "address/ZIP") {
		t.Errorf("expected review flag for missing address, got %q", row.ReviewNote)
	}
	// The initial-value audit trail should record the single lot conversion.
	if len(row.InitialValue.Audit) != 1 {
		t.Errorf("initial value audit lines = %d, want 1", len(row.InitialValue.Audit))
	}

	// Table A2 aggregates the account: closing balance = Σ A3 closing values.
	if len(rep.A2) != 1 {
		t.Fatalf("A2 rows = %d, want 1", len(rep.A2))
	}
	if got := rep.A2[0].ClosingBalance.INR.Amount.RatString(); got != "120000" {
		t.Errorf("A2 closing balance = %s, want 120000", got)
	}
}

func TestBuildEntitiesOverrideAndRSU(t *testing.T) {
	inst := model.Instrument{Symbol: "AAA", ISIN: "X1", Name: "Alpha Inc", AssetClass: "STK", ListingCtry: "US", Currency: model.USD}
	yearEnd := day(2024, 12, 31)
	st := &model.Statement{
		Year:          2024,
		OpenPositions: []model.Position{{Instrument: inst, Date: yearEnd, Quantity: big.NewRat(10, 1), MarkPrice: usd(150)}},
		// RSU lot: acquisition date is the vesting date, not the open date.
		Lots: []model.Lot{{Instrument: inst, OpenDate: day(2024, 1, 1), VestDate: day(2024, 5, 20), Quantity: big.NewRat(10, 1), CostBasis: usd(1000)}},
	}

	// Entity metadata fills address/ZIP/nature so the row needs no review.
	dir := t.TempDir()
	csv := "isin,symbol,entity_name,address,zip,country_code,nature\n" +
		"X1,AAA,Alpha Inc,\"123 Main St, City\",94000,2,Listed company\n"
	if err := os.WriteFile(filepath.Join(dir, "entities.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	ents, err := entities.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	store := fakeStore{big.NewRat(80, 1)}
	peaks, _ := peak.Compute(st, store, peak.ModeApprox, nil)
	rep, _ := Build(st, store, peaks, ents)
	row := rep.A3[0]

	if row.Address != "123 Main St, City" || row.ZIP != "94000" || row.NatureEntity != "Listed company" {
		t.Errorf("entity override not applied: %+v", row)
	}
	if row.AcquiredOn != "2024-05-20" {
		t.Errorf("acquired = %q, want RSU vesting date 2024-05-20", row.AcquiredOn)
	}
	if row.NeedsReview {
		t.Errorf("row should not need review once metadata is complete: %q", row.ReviewNote)
	}
}

func TestBuildExitedPositionHasNoClosing(t *testing.T) {
	inst := model.Instrument{Symbol: "BBB", ISIN: "X2", Name: "Beta", AssetClass: "ETF", ListingCtry: "US", Currency: model.USD}
	st := &model.Statement{
		Year: 2024,
		Trades: []model.Trade{
			{Instrument: inst, Date: day(2024, 2, 1), Side: model.Buy, Quantity: big.NewRat(5, 1), Price: usd(100)},
			{Instrument: inst, Date: day(2024, 9, 1), Side: model.Sell, Quantity: big.NewRat(5, 1), Price: usd(120), Proceeds: usd(600)},
		},
	}
	store := fakeStore{big.NewRat(80, 1)}
	peaks, _ := peak.Compute(st, store, peak.ModeApprox, nil)
	rep, _ := Build(st, store, peaks, nil)
	row := rep.A3[0]
	if row.ClosingValue.INR.Amount.Sign() != 0 {
		t.Errorf("closing value = %s, want 0 for an exited position", row.ClosingValue.INR.Amount.RatString())
	}
	if got := row.SaleProceeds.INR.Amount.RatString(); got != "48000" { // 600 * 80
		t.Errorf("proceeds = %s, want 48000", got)
	}
	if row.AcquiredOn != "2024-02-01" { // from the buy trade, no lot present
		t.Errorf("acquired = %q, want 2024-02-01", row.AcquiredOn)
	}
}

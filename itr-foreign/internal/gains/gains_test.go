package gains

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akagr/finance-tools/itr-foreign/internal/fx"
	"github.com/akagr/finance-tools/itr-foreign/internal/model"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// testStore publishes a flat rate on every month end in range, plus the two the
// individual tests care about.
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

func lot(open, close string, cost, proceeds, commission, pnl int64) model.RealizedLot {
	l := model.RealizedLot{
		Instrument: model.Instrument{Symbol: "AAA", ISIN: "US0000000001", ListingCtry: "US"},
		CloseDate:  day(close),
		Quantity:   big.NewRat(10, 1),
		Cost:       model.NewMoney(model.USD, big.NewRat(cost, 1)),
		Proceeds:   model.NewMoney(model.USD, big.NewRat(proceeds, 1)),
		Commission: model.NewMoney(model.USD, big.NewRat(commission, 1)),
	}
	if open != "" {
		l.OpenDate = day(open)
	}
	if pnl != 0 {
		l.RealizedPnL = model.NewMoney(model.USD, big.NewRat(pnl, 1))
	}
	return l
}

// Foreign shares are long-term only after MORE than 24 months — not 12, and not
// at exactly 24 months.
func TestHoldingPeriodBoundary(t *testing.T) {
	store := testStore(t, map[string]string{
		"2022-12-31": "83.00", "2023-12-31": "83.00", "2024-12-31": "85.00",
		"2025-07-31": "87.00", "2025-11-30": "88.00",
	})
	cases := []struct {
		name       string
		open, sell string
		want       Term
	}{
		{"a day short of 24 months", "2023-08-15", "2025-08-14", Short},
		{"exactly 24 months", "2023-08-15", "2025-08-15", Short},
		{"24 months and a day", "2023-08-14", "2025-08-15", Long},
		{"comfortably over 24 months", "2023-01-10", "2025-12-20", Long},
		{"under a year", "2025-01-10", "2025-08-15", Short},
	}
	for _, c := range cases {
		st := &model.Statement{RealizedLots: []model.RealizedLot{lot(c.open, c.sell, 1000, 1200, 0, 0)}}
		sum, err := Compute(st, store, 2025, PerLeg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := sum.Disposals[0].Term; got != c.want {
			t.Errorf("%s (%s → %s): term = %s, want %s", c.name, c.open, c.sell, got, c.want)
		}
	}
}

// The two FX methods must genuinely differ when the rupee has moved, since that
// is the whole reason the choice is exposed.
func TestPerLegVersusNetGain(t *testing.T) {
	store := testStore(t, map[string]string{
		"2022-12-31": "80.00", // cost leg (acquired Jan 2023)
		"2025-07-31": "90.00", // proceeds leg (sold Aug 2025)
	})
	st := &model.Statement{RealizedLots: []model.RealizedLot{
		lot("2023-01-15", "2025-08-20", 1000, 1200, 0, 0),
	}}

	perLeg, err := Compute(st, store, 2025, PerLeg)
	if err != nil {
		t.Fatal(err)
	}
	netGain, err := Compute(st, store, 2025, NetGain)
	if err != nil {
		t.Fatal(err)
	}

	// Per leg: 1200 × 90 − 1000 × 80 = 108000 − 80000 = 28000.
	if got, want := perLeg.Disposals[0].GainINR.FloatString(2), "28000.00"; got != want {
		t.Errorf("per-leg gain = %s, want %s", got, want)
	}
	// Net gain: (1200 − 1000) × 90 = 18000. A resident who takes this view
	// reports a materially smaller gain, which is exactly why it is flagged.
	if got, want := netGain.Disposals[0].GainINR.FloatString(2), "18000.00"; got != want {
		t.Errorf("net-gain gain = %s, want %s", got, want)
	}
	if perLeg.LTCG.Cmp(netGain.LTCG) <= 0 {
		t.Error("with a depreciating rupee the per-leg gain should exceed the net-gain figure")
	}
}

// The selling commission is expenditure in connection with the transfer and
// reduces the gain.
func TestClosingCommissionReducesGain(t *testing.T) {
	store := testStore(t, map[string]string{"2024-12-31": "80.00", "2025-07-31": "80.00"})
	st := &model.Statement{RealizedLots: []model.RealizedLot{
		lot("2025-01-10", "2025-08-20", 1000, 1200, -10, 0),
	}}
	sum, err := Compute(st, store, 2025, PerLeg)
	if err != nil {
		t.Fatal(err)
	}
	// (1200 − 10 − 1000) × 80 = 15200.
	if got, want := sum.Disposals[0].GainINR.FloatString(2), "15200.00"; got != want {
		t.Errorf("gain = %s, want %s", got, want)
	}
}

// Transfers before 23 Jul 2024 fall in the 20%-with-indexation regime, which
// this tool does not compute — so they must be separated and flagged.
func TestPreRateCutSeparatedAndFlagged(t *testing.T) {
	store := testStore(t, map[string]string{"2021-12-31": "75.00", "2024-05-31": "83.00"})
	st := &model.Statement{RealizedLots: []model.RealizedLot{
		lot("2022-01-10", "2024-06-15", 1000, 1500, 0, 0),
	}}
	sum, err := Compute(st, store, 2024, PerLeg)
	if err != nil {
		t.Fatal(err)
	}
	d := sum.Disposals[0]
	if !d.PreRateCut {
		t.Error("a June 2024 transfer should be flagged as pre-23-Jul-2024")
	}
	if d.Term != Long {
		t.Fatalf("term = %s, want %s", d.Term, Long)
	}
	if sum.LTCGPreCut.Sign() == 0 || sum.LTCG.Sign() != 0 {
		t.Errorf("pre-cut gain went to the wrong bucket: LTCG=%s preCut=%s",
			sum.LTCG.FloatString(2), sum.LTCGPreCut.FloatString(2))
	}
	if !sum.NeedsReview {
		t.Error("a pre-23-Jul-2024 long-term transfer must be flagged for manual indexation")
	}
}

// Without an acquisition date the holding period is unknowable, so the higher
// short-term treatment is assumed rather than guessing in the taxpayer's favour.
func TestMissingAcquisitionDateIsShortAndFlagged(t *testing.T) {
	store := testStore(t, map[string]string{"2025-07-31": "87.00"})
	st := &model.Statement{RealizedLots: []model.RealizedLot{
		lot("", "2025-08-20", 1000, 1200, 0, 0),
	}}
	sum, err := Compute(st, store, 2025, NetGain)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Disposals[0].Term; got != Short {
		t.Errorf("term = %s, want %s", got, Short)
	}
	if !sum.Disposals[0].NeedsReview {
		t.Error("a missing acquisition date must be flagged")
	}
}

// Our arithmetic is cross-checked against IBKR's own realized P&L; a mismatch
// (a short cover inverts the signs) must surface rather than pass silently.
func TestRealizedPnLMismatchIsFlagged(t *testing.T) {
	store := testStore(t, map[string]string{"2024-12-31": "85.00", "2025-07-31": "87.00"})
	st := &model.Statement{RealizedLots: []model.RealizedLot{
		lot("2025-01-10", "2025-08-20", 1000, 1200, 0, -200), // IBKR says a loss
	}}
	sum, err := Compute(st, store, 2025, PerLeg)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Disposals[0].NeedsReview {
		t.Fatal("a disagreement with IBKR's realized P&L must be flagged")
	}
	// And an agreeing lot must NOT be flagged, or the check is just noise.
	st = &model.Statement{RealizedLots: []model.RealizedLot{
		lot("2025-01-10", "2025-08-20", 1000, 1200, 0, 200),
	}}
	sum, err = Compute(st, store, 2025, PerLeg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Disposals[0].NeedsReview {
		t.Errorf("a matching lot should not be flagged: %s", sum.Disposals[0].ReviewNote)
	}
}

func TestParseMethod(t *testing.T) {
	for _, s := range []string{"", "per-leg", "PER-LEG"} {
		if m, err := ParseMethod(s); err != nil || m != PerLeg {
			t.Errorf("ParseMethod(%q) = %v, %v; want per-leg", s, m, err)
		}
	}
	if m, err := ParseMethod("net-gain"); err != nil || m != NetGain {
		t.Errorf("ParseMethod(net-gain) = %v, %v", m, err)
	}
	if _, err := ParseMethod("spot"); err == nil {
		t.Error("an unknown method should be rejected rather than silently defaulted")
	}
}

package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/akagr/finance-tools/networth/internal/analysis"
	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/money"
	"github.com/akagr/finance-tools/networth/internal/store"
)

func sample() Data {
	return Data{
		Settings: db.Settings{BaseCurrency: "INR", Grouping: money.GroupIndian},
		Summary: store.Summary{
			AsOf:            "2026-01-01",
			NetWorth:        money.FromMajor(14310000),
			Assets:          money.FromMajor(18640000),
			Liabilities:     money.FromMajor(-4330000),
			Known:           true,
			Since:           "2025-07-01",
			Change:          money.FromMajor(1440000),
			Contribution:    money.FromMajor(685000),
			MarketReturn:    money.FromMajor(755000),
			XIRR:            0.117,
			HasXIRR:         true,
			TWR:             analysis.TWRResult{Annualised: 0.104, Periods: 6},
			HasTWR:          true,
			ValuationPoints: 7,
		},
		Balances: []store.Balance{
			{Account: "Flat, Pune", Kind: "asset", Currency: "INR",
				BaseValue: money.FromMajor(10200000), Known: true, InNetworth: true,
				AnchorDate: "2026-01-01"},
		},
		Attribution: []store.AttributionRow{
			{Start: "2025-07-01", End: "2026-01-01",
				StartValue: money.FromMajor(12870000), EndValue: money.FromMajor(14310000),
				Change:       money.FromMajor(1440000),
				Contribution: money.FromMajor(685000),
				MarketReturn: money.FromMajor(755000)},
		},
	}
}

// Both return measures must appear together. Quoting either one alone is how a
// portfolio gets misjudged, so a report that omits one is a bug.
func TestTextShowsBothReturnMeasures(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, sample()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Money-weighted", "Time-weighted", "11.7%", "10.4%"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

func TestTextShowsTheContributionSplit(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, sample()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"You put in", "+₹6,85,000", "Markets gave", "+₹7,55,000"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

// A thin evidence base must be stated, not implied by a confident-looking
// percentage.
func TestCaveatsAreAlwaysPrinted(t *testing.T) {
	d := sample()
	d.Summary.ValuationPoints = 3
	d.Summary.UnknownAccounts = 2
	d.Summary.UnexplainedShare = 0.4

	var b bytes.Buffer
	if err := Text(&b, d); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Caveats") {
		t.Fatalf("no caveats section:\n%s", out)
	}
	for _, want := range []string{"could not be valued", "Only 3 valuations", "no contribution recorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("caveats are missing %q:\n%s", want, out)
		}
	}
}

func TestEmptyDatabaseProducesGuidanceNotAZero(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, Data{Settings: db.Settings{Grouping: money.GroupIndian}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Nothing recorded yet") {
		t.Errorf("an empty database should explain itself, got:\n%s", b.String())
	}
}

// Amounts leave as exact decimal strings. Emitting floats would undo the
// exactness the whole storage design exists to preserve.
func TestJSONWritesExactDecimalStrings(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, sample()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["net_worth"] != "14310000.00" {
		t.Errorf("net_worth = %v, want the string \"14310000.00\"", got["net_worth"])
	}
	if _, ok := got["xirr"].(float64); !ok {
		t.Error("xirr should be a number: it is a rate, not an exact amount")
	}
}

func TestMarkdownRendersTables(t *testing.T) {
	var b bytes.Buffer
	if err := Markdown(&b, sample()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"# Net worth", "| Markets gave |", "## By period"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown is missing %q:\n%s", want, out)
		}
	}
}

func TestCSVHasAStableHeader(t *testing.T) {
	var b bytes.Buffer
	if err := CSV(&b, sample()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	want := "start,end,start_value,end_value,change,contribution,market_return"
	if lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if len(lines) != 2 {
		t.Errorf("got %d lines, want a header and one period", len(lines))
	}
	if !strings.Contains(lines[1], "685000.00") {
		t.Errorf("row is missing the contribution: %q", lines[1])
	}
}

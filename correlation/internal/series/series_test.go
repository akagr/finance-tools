package series

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBasic(t *testing.T) {
	p := write(t, "p.csv", `date,symbol,close,currency
2024-01-02,VWRA,100.5,USD
2024-01-01,VWRA,100.0,USD
2024-01-01,^NSEI,22000,INR
2024-01-02,^NSEI,22100,INR
`)
	got, err := Load(p, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("assets = %d, want 2", len(got))
	}
	// First-seen order preserved.
	if got[0].Label != "VWRA" || got[1].Label != "^NSEI" {
		t.Fatalf("order = %s,%s", got[0].Label, got[1].Label)
	}
	// Points sorted ascending by date.
	if !got[0].Points[0].Date.Before(got[0].Points[1].Date) {
		t.Fatal("points not sorted ascending")
	}
	if got[1].Currency != "INR" {
		t.Fatalf("currency = %s, want INR", got[1].Currency)
	}
}

func TestLoadDefaultCurrencyAndSkips(t *testing.T) {
	p := write(t, "p.csv", `date,symbol,close
2024-01-01,AAA,10
bad,AAA,11
2024-01-02,AAA,-5
2024-01-03,AAA,12
`)
	got, err := Load(p, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Currency != "EUR" {
		t.Fatalf("got %+v", got)
	}
	// Bad date and non-positive close skipped -> 2 valid points.
	if len(got[0].Points) != 2 {
		t.Fatalf("points = %d, want 2", len(got[0].Points))
	}
}

func TestLoadDuplicateDateLastWins(t *testing.T) {
	p := write(t, "p.csv", `date,symbol,close
2024-01-01,AAA,10
2024-01-01,AAA,15
`)
	got, err := Load(p, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Points) != 1 || got[0].Points[0].Close != 15 {
		t.Fatalf("dedup failed: %+v", got[0].Points)
	}
}

func TestLoadMissingColumns(t *testing.T) {
	p := write(t, "p.csv", "date,close\n2024-01-01,10\n")
	if _, err := Load(p, "USD"); err == nil {
		t.Fatal("want error for missing symbol column")
	}
}

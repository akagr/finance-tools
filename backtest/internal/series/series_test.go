package series

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "prices.csv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSortsAndDedups(t *testing.T) {
	// Out-of-order dates and a duplicate (last wins).
	p := writeCSV(t, "date,symbol,close\n"+
		"2024-01-03,A,12\n"+
		"2024-01-01,A,10\n"+
		"2024-01-02,A,11\n"+
		"2024-01-02,A,99\n")
	all, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("series count = %d, want 1", len(all))
	}
	pts := all[0].Points
	if len(pts) != 3 {
		t.Fatalf("points = %d, want 3 (deduped)", len(pts))
	}
	for i := 1; i < len(pts); i++ {
		if !pts[i-1].Date.Before(pts[i].Date) {
			t.Errorf("not sorted ascending at %d", i)
		}
	}
	if pts[1].Close != 99 {
		t.Errorf("dedup last-wins failed: close = %v, want 99", pts[1].Close)
	}
}

func TestLoadMultipleSymbols(t *testing.T) {
	p := writeCSV(t, "date,symbol,close\n"+
		"2024-01-01,A,10\n"+
		"2024-01-01,B,20\n"+
		"2024-01-02,A,11\n")
	all, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("series count = %d, want 2", len(all))
	}
	// First-seen order preserved.
	if all[0].Label != "A" || all[1].Label != "B" {
		t.Errorf("labels = %q,%q, want A,B", all[0].Label, all[1].Label)
	}
}

func TestLoadRejectsMissingColumns(t *testing.T) {
	p := writeCSV(t, "date,ticker,price\n2024-01-01,A,10\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for missing required columns")
	}
}

func TestLoadSkipsBadRows(t *testing.T) {
	p := writeCSV(t, "date,symbol,close\n"+
		"not-a-date,A,10\n"+
		"2024-01-01,A,-5\n"+ // non-positive close skipped
		"2024-01-02,A,abc\n"+ // unparseable close skipped
		"2024-01-03,A,15\n")
	all, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Points) != 1 {
		t.Fatalf("expected 1 valid point, got %#v", all)
	}
	if all[0].Points[0].Close != 15 {
		t.Errorf("close = %v, want 15", all[0].Points[0].Close)
	}
}

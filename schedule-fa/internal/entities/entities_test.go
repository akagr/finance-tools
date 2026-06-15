package entities

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAndLookup(t *testing.T) {
	dir := t.TempDir()
	// Note: column order is intentionally shuffled and an extra column added, to
	// confirm parsing is by header name and ignores unknown columns. The address
	// is quoted because it contains a comma.
	p := writeFile(t, dir, "e.csv",
		"symbol,isin,nature,entity_name,address,zip,country_code,extra\n"+
			"AAPL,US0378331005,Company (listed),Apple Inc,\"One Apple Park Way, Cupertino, CA\",95014,1,ignored\n"+
			"VWRA,IE00BK5BQT80,Fund,Vanguard FTSE All-World,\"Dublin, Ireland\",D02R296,353,x\n")

	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Lookup by ISIN.
	e, ok := s.Lookup("US0378331005", "")
	if !ok {
		t.Fatal("AAPL not found by ISIN")
	}
	if e.Name != "Apple Inc" || e.ZIP != "95014" || e.CountryCode != "1" ||
		e.Address != "One Apple Park Way, Cupertino, CA" || e.Nature != "Company (listed)" {
		t.Errorf("unexpected entity: %+v", e)
	}

	// Lookup by symbol, case-insensitive.
	if _, ok := s.Lookup("", "vwra"); !ok {
		t.Error("VWRA not found by lowercase symbol")
	}
	if _, ok := s.Lookup("ie00bk5bqt80", ""); !ok {
		t.Error("VWRA not found by lowercase ISIN")
	}

	// ISIN takes precedence over symbol when both are given.
	e, _ = s.Lookup("US0378331005", "VWRA")
	if e.Symbol != "AAPL" {
		t.Errorf("ISIN should win: got %q, want AAPL", e.Symbol)
	}

	// Unknown instrument.
	if _, ok := s.Lookup("XX", "ZZZZ"); ok {
		t.Error("unknown instrument should not be found")
	}
}

func TestLoadDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.csv", "isin,symbol,entity_name\nUS1,AAA,Alpha\n")
	writeFile(t, dir, "b.csv", "isin,symbol,entity_name\nUS2,BBB,Beta\n")
	writeFile(t, dir, "notes.txt", "ignored, not a csv\n") // non-CSV is skipped

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	for _, sym := range []string{"AAA", "BBB"} {
		if _, ok := s.Lookup("", sym); !ok {
			t.Errorf("%s not loaded from directory", sym)
		}
	}
}

func TestLoadMissingPathIsEmptyNotError(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist.csv"))
	if err != nil {
		t.Fatalf("missing path should not error, got %v", err)
	}
	if _, ok := s.Lookup("US0378331005", "AAPL"); ok {
		t.Error("empty store should find nothing")
	}
}

func TestLoadHeaderOnly(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "h.csv", "isin,symbol,entity_name\n")
	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Lookup("US1", "AAA"); ok {
		t.Error("header-only file should yield no entities")
	}
}

func TestNilStoreLookup(t *testing.T) {
	var s *Store
	if _, ok := s.Lookup("US1", "AAA"); ok {
		t.Error("nil store lookup should return false, not panic")
	}
}

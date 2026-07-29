// Package entities provides issuer metadata that IBKR does not supply cleanly
// but Schedule FA Table A3 requires: address, ZIP, ITR country code, and the
// nature of the entity. Data is user-maintained CSV keyed by ISIN or symbol.
//
// CSV columns (header required; extra columns ignored):
//
//	isin,symbol,entity_name,address,zip,country_code,nature
//	US0378331005,AAPL,Apple Inc,"One Apple Park Way, Cupertino, CA",95014,2,Listed company
package entities

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Entity is one issuer's Schedule FA metadata.
type Entity struct {
	ISIN        string
	Symbol      string
	Name        string
	Address     string
	ZIP         string
	CountryCode string // ITR country code
	Nature      string
}

// Store resolves metadata by ISIN (preferred) or symbol.
type Store struct {
	byISIN   map[string]Entity
	bySymbol map[string]Entity
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{byISIN: map[string]Entity{}, bySymbol: map[string]Entity{}}
}

// Load reads a CSV file or every *.csv in a directory. A missing path yields an
// empty store with no error, so the metadata file is optional.
func Load(path string) (*Store, error) {
	s := NewStore()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		if files, err = filepath.Glob(filepath.Join(path, "*.csv")); err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}
	for _, f := range files {
		if err := s.loadFile(f); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("entities: %s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("entities: %s: %w", path, err)
		}
		e := Entity{
			ISIN:        get(rec, "isin"),
			Symbol:      get(rec, "symbol"),
			Name:        get(rec, "entity_name"),
			Address:     get(rec, "address"),
			ZIP:         get(rec, "zip"),
			CountryCode: get(rec, "country_code"),
			Nature:      get(rec, "nature"),
		}
		if e.ISIN != "" {
			s.byISIN[strings.ToUpper(e.ISIN)] = e
		}
		if e.Symbol != "" {
			s.bySymbol[strings.ToUpper(e.Symbol)] = e
		}
	}
	return nil
}

// Lookup resolves metadata by ISIN first, then symbol.
func (s *Store) Lookup(isin, symbol string) (Entity, bool) {
	if s == nil {
		return Entity{}, false
	}
	if isin != "" {
		if e, ok := s.byISIN[strings.ToUpper(isin)]; ok {
			return e, true
		}
	}
	if symbol != "" {
		if e, ok := s.bySymbol[strings.ToUpper(symbol)]; ok {
			return e, true
		}
	}
	return Entity{}, false
}

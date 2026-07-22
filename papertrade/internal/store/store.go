// Package store persists a paper-trading account to disk: the account state as a
// single JSON file, and every simulated fill appended to a JSON-lines log next to
// it, so there is a complete, auditable history of what the paper account did.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/account"
	"github.com/akagr/finance-tools/papertrade/internal/broker"
)

// LogEntry is one recorded fill with the context needed to audit it.
type LogEntry struct {
	Date        string      `json:"date"` // bar date the decision was made on
	Time        time.Time   `json:"time"` // when the step ran
	Fill        broker.Fill `json:"fill"`
	Quote       float64     `json:"quote"` // pre-slippage price
	TargetWt    float64     `json:"target_weight"`
	CashAfter   float64     `json:"cash_after"`
	SharesAfter float64     `json:"shares_after"`
	EquityAfter float64     `json:"equity_after"`
}

// Store reads and writes an account and its fills log under a directory.
type Store struct {
	Dir string
}

// New returns a Store rooted at dir.
func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) statePath() string { return filepath.Join(s.Dir, "account.json") }
func (s *Store) logPath() string   { return filepath.Join(s.Dir, "fills.jsonl") }

// Exists reports whether an account has been initialised in the directory.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.statePath())
	return err == nil
}

// Save writes the account state atomically (write-temp-then-rename).
func (s *Store) Save(a *account.Account) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath())
}

// Load reads the account state.
func (s *Store) Load() (*account.Account, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("store: no account at %s (run `papertrade init` first)", s.Dir)
		}
		return nil, err
	}
	var a account.Account
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("store: parsing %s: %w", s.statePath(), err)
	}
	return &a, nil
}

// AppendLog appends one fill entry to the JSON-lines log.
func (s *Store) AppendLog(e LogEntry) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadLog returns all recorded fill entries in order.
func (s *Store) ReadLog() ([]LogEntry, error) {
	f, err := os.Open(s.logPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []LogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("store: parsing log line: %w", err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

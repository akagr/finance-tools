package db

import (
	"os"
	"path/filepath"
)

// DefaultPath resolves where the database lives when the user does not say.
//
// The order is: an explicit --db flag (handled by the caller), then NETWORTH_DB,
// then a networth.db in the working directory if one is already there, and
// finally a per-user location. Preferring an existing file in the working
// directory means a database kept alongside other records is picked up without
// any flag, while a first run still lands somewhere sensible and private.
func DefaultPath() string {
	if p := os.Getenv("NETWORTH_DB"); p != "" {
		return expand(p)
	}
	if _, err := os.Stat("networth.db"); err == nil {
		abs, err := filepath.Abs("networth.db")
		if err == nil {
			return abs
		}
		return "networth.db"
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "networth.db"
	}
	return filepath.Join(dir, ".local", "share", "networth", "networth.db")
}

func expand(p string) string {
	if len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Resolve returns the database path to use given an explicit flag value.
func Resolve(flagValue string) string {
	if flagValue != "" {
		return expand(flagValue)
	}
	return DefaultPath()
}

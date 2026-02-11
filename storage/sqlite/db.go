package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	defaultDBDir  = ".petalflow"
	defaultDBFile = "petalflow.db"
)

// DefaultPath returns the default shared SQLite database path.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sqlite: resolve user home: %w", err)
	}
	return filepath.Join(home, defaultDBDir, defaultDBFile), nil
}

// Open opens (or creates) a SQLite database and applies baseline pragmas.
func Open(path string) (*sql.DB, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return nil, fmt.Errorf("sqlite: database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(clean), 0o750); err != nil {
		return nil, fmt.Errorf("sqlite: create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", clean)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open db: %w", err)
	}

	// Keep defaults conservative for local single-process and daemon use.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, stmt := range pragmas {
		if _, execErr := db.Exec(stmt); execErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite: apply pragma %q: %w", stmt, execErr)
		}
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping db: %w", err)
	}
	return db, nil
}

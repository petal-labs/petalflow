package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

// Migration describes one idempotent SQL migration step.
type Migration struct {
	Name string
	SQL  string
}

// ApplyMigrations runs pending migrations in-order.
func ApplyMigrations(db *sql.DB, migrations []Migration) error {
	if db == nil {
		return fmt.Errorf("sqlite: database is nil")
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
  name TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("sqlite: ensure schema_migrations table: %w", err)
	}

	for _, m := range migrations {
		if m.Name == "" {
			return fmt.Errorf("sqlite: migration name is required")
		}
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM schema_migrations WHERE name = ?`, m.Name).Scan(&exists); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("sqlite: check migration %q: %w", m.Name, err)
		}
		if exists == 1 {
			continue
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			return fmt.Errorf("sqlite: apply migration %q: %w", m.Name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			m.Name,
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("sqlite: record migration %q: %w", m.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migrations: %w", err)
	}
	return nil
}

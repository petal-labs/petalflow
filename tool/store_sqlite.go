package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteStoreSchema = `
CREATE TABLE IF NOT EXISTS tool_registrations (
	name TEXT PRIMARY KEY,
	payload BLOB NOT NULL,
	updated_at TEXT NOT NULL
);`

const (
	defaultSQLiteStoreDir = ".petalflow"
	defaultSQLiteStoreDB  = "petalflow.db"
)

// SQLiteStoreConfig configures the SQLite-backed tool store.
type SQLiteStoreConfig struct {
	DSN string
	// Scope controls secret key derivation; defaults to DSN.
	Scope string
}

// SQLiteStore persists tool registrations in SQLite.
type SQLiteStore struct {
	db    *sql.DB
	scope string
}

// DefaultSQLitePath returns the default SQLite path for CLI/daemon storage.
func DefaultSQLitePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("tool: resolve user home: %w", err)
	}
	return filepath.Join(home, defaultSQLiteStoreDir, defaultSQLiteStoreDB), nil
}

// NewDefaultSQLiteStore creates a SQLite store at ~/.petalflow/petalflow.db.
func NewDefaultSQLiteStore() (*SQLiteStore, error) {
	path, err := DefaultSQLitePath()
	if err != nil {
		return nil, err
	}
	return NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
}

// NewSQLiteStore opens (or creates) a SQLite-backed registration store.
func NewSQLiteStore(cfg SQLiteStoreConfig) (*SQLiteStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("tool: sqlite store dsn is required")
	}

	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("tool: sqlite store open: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool: sqlite store set WAL mode: %w", err)
	}

	if _, err := db.Exec(sqliteStoreSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool: sqlite store create schema: %w", err)
	}

	scope := cfg.Scope
	if strings.TrimSpace(scope) == "" {
		scope = cfg.DSN
	}

	return &SQLiteStore{
		db:    db,
		scope: scope,
	}, nil
}

// List returns all registrations in deterministic (name-sorted) order.
func (s *SQLiteStore) List(ctx context.Context) ([]ToolRegistration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, errors.New("tool: sqlite store is nil")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT payload
FROM tool_registrations
ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("tool: sqlite list registrations: %w", err)
	}
	defer rows.Close()

	var regs []ToolRegistration
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("tool: sqlite scan registration: %w", err)
		}
		reg, err := s.decodeRegistration(payload)
		if err != nil {
			return nil, err
		}
		regs = append(regs, reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tool: sqlite registration rows: %w", err)
	}

	return cloneRegistrations(regs), nil
}

// Get returns a registration by name.
func (s *SQLiteStore) Get(ctx context.Context, name string) (ToolRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolRegistration{}, false, err
	}
	if s == nil || s.db == nil {
		return ToolRegistration{}, false, errors.New("tool: sqlite store is nil")
	}

	row := s.db.QueryRowContext(ctx, `
SELECT payload
FROM tool_registrations
WHERE name = ?`, name)

	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolRegistration{}, false, nil
		}
		return ToolRegistration{}, false, fmt.Errorf("tool: sqlite get registration: %w", err)
	}

	reg, err := s.decodeRegistration(payload)
	if err != nil {
		return ToolRegistration{}, false, err
	}
	return cloneRegistration(reg), true, nil
}

// Upsert inserts or updates a registration by name.
func (s *SQLiteStore) Upsert(ctx context.Context, reg ToolRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("tool: sqlite store is nil")
	}
	if strings.TrimSpace(reg.Name) == "" {
		return errors.New("tool: registration name is required")
	}

	existing, found, err := s.Get(ctx, reg.Name)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if reg.Status == "" {
		reg.Status = StatusUnverified
	}
	if reg.RegisteredAt.IsZero() {
		if found && !existing.RegisteredAt.IsZero() {
			reg.RegisteredAt = existing.RegisteredAt
		} else {
			reg.RegisteredAt = now
		}
	}
	if reg.LastHealthCheck.IsZero() && found {
		reg.LastHealthCheck = existing.LastHealthCheck
	}

	payload, err := s.encodeRegistration(reg)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_registrations (name, payload, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
	payload = excluded.payload,
	updated_at = excluded.updated_at`,
		reg.Name,
		payload,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("tool: sqlite upsert registration: %w", err)
	}
	return nil
}

// Delete removes a registration by name. Deleting a missing name is a no-op.
func (s *SQLiteStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("tool: sqlite store is nil")
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM tool_registrations WHERE name = ?`, name); err != nil {
		return fmt.Errorf("tool: sqlite delete registration: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) encodeRegistration(reg ToolRegistration) ([]byte, error) {
	clone := cloneRegistration(reg)
	if err := s.encryptSensitiveRegistration(&clone); err != nil {
		return nil, err
	}
	data, err := json.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("tool: sqlite encode registration: %w", err)
	}
	return data, nil
}

func (s *SQLiteStore) decodeRegistration(payload []byte) (ToolRegistration, error) {
	var reg ToolRegistration
	if err := json.Unmarshal(payload, &reg); err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: sqlite decode registration: %w", err)
	}
	if err := s.decryptSensitiveRegistration(&reg); err != nil {
		return ToolRegistration{}, err
	}
	return reg, nil
}

func (s *SQLiteStore) encryptSensitiveRegistration(reg *ToolRegistration) error {
	if reg == nil || len(reg.Config) == 0 || len(reg.Manifest.Config) == 0 {
		return nil
	}

	codec, err := newSecretCodec(s.scope)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range reg.Manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := reg.Config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		encrypted, err := codec.Encrypt(value)
		if err != nil {
			return fmt.Errorf("tool: encrypt config %q for %s: %w", key, reg.Name, err)
		}
		reg.Config[key] = encrypted
	}
	return nil
}

func (s *SQLiteStore) decryptSensitiveRegistration(reg *ToolRegistration) error {
	if reg == nil || len(reg.Config) == 0 || len(reg.Manifest.Config) == 0 {
		return nil
	}

	codec, err := newSecretCodec(s.scope)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range reg.Manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := reg.Config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		plain, err := codec.Decrypt(value)
		if err != nil {
			return fmt.Errorf("tool: decrypt config %q for %s: %w", key, reg.Name, err)
		}
		reg.Config[key] = plain
	}
	return nil
}

var _ Store = (*SQLiteStore)(nil)

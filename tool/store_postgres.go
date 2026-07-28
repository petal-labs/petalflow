package tool

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/internal/sqldialect"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed postgres_schema.sql
var toolPostgresSchema string

// PostgresStoreConfig configures the PostgreSQL-backed tool store.
type PostgresStoreConfig struct {
	DSN string
	// Scope controls secret key derivation; defaults to DSN.
	Scope string
}

// PostgresStore persists tool registrations in PostgreSQL.
type PostgresStore struct {
	db    *sql.DB
	scope string
}

// NewPostgresStore opens (or creates) a PostgreSQL-backed registration store.
func NewPostgresStore(cfg PostgresStoreConfig) (*PostgresStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("tool: postgres store dsn is required")
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("tool: postgres store open: %w", err)
	}
	db.SetMaxOpenConns(10)

	if _, err := db.Exec(toolPostgresSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool: postgres store create schema: %w", err)
	}

	scope := cfg.Scope
	if strings.TrimSpace(scope) == "" {
		scope = cfg.DSN
	}

	return &PostgresStore{
		db:    db,
		scope: scope,
	}, nil
}

// List returns all registrations in deterministic (name-sorted) order.
func (s *PostgresStore) List(ctx context.Context) ([]ToolRegistration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, errors.New("tool: postgres store is nil")
	}

	rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(`
SELECT payload
FROM tool_registrations
ORDER BY name ASC`))
	if err != nil {
		return nil, fmt.Errorf("tool: postgres list registrations: %w", err)
	}
	defer rows.Close()

	var regs []ToolRegistration
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("tool: postgres scan registration: %w", err)
		}
		reg, err := decodeRegistration(s.scope, payload)
		if err != nil {
			return nil, err
		}
		regs = append(regs, reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tool: postgres registration rows: %w", err)
	}

	return cloneRegistrations(regs), nil
}

// Get returns a registration by name.
func (s *PostgresStore) Get(ctx context.Context, name string) (ToolRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolRegistration{}, false, err
	}
	if s == nil || s.db == nil {
		return ToolRegistration{}, false, errors.New("tool: postgres store is nil")
	}

	row := s.db.QueryRowContext(ctx, sqldialect.Rebind(`
SELECT payload
FROM tool_registrations
WHERE name = ?`), name)

	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolRegistration{}, false, nil
		}
		return ToolRegistration{}, false, fmt.Errorf("tool: postgres get registration: %w", err)
	}

	reg, err := decodeRegistration(s.scope, payload)
	if err != nil {
		return ToolRegistration{}, false, err
	}
	return cloneRegistration(reg), true, nil
}

// Upsert inserts or updates a registration by name.
func (s *PostgresStore) Upsert(ctx context.Context, reg ToolRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("tool: postgres store is nil")
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

	payload, err := encodeRegistration(s.scope, reg)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, sqldialect.Rebind(`
INSERT INTO tool_registrations (name, payload, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
	payload = excluded.payload,
	updated_at = excluded.updated_at`),
		reg.Name,
		payload,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("tool: postgres upsert registration: %w", err)
	}
	return nil
}

// Delete removes a registration by name. Deleting a missing name is a no-op.
func (s *PostgresStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("tool: postgres store is nil")
	}

	if _, err := s.db.ExecContext(ctx, sqldialect.Rebind(`DELETE FROM tool_registrations WHERE name = ?`), name); err != nil {
		return fmt.Errorf("tool: postgres delete registration: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

var _ Store = (*PostgresStore)(nil)

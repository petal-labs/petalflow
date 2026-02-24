package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const providerSQLiteSchema = `
CREATE TABLE IF NOT EXISTS providers (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	default_model TEXT,
	status TEXT NOT NULL DEFAULT 'disconnected',
	api_key_hash TEXT,
	api_key_enc TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_providers_type ON providers(type);
CREATE INDEX IF NOT EXISTS idx_providers_status ON providers(status);
`

// ProviderSQLiteStore persists provider records in SQLite.
type ProviderSQLiteStore struct {
	db      *sql.DB
	secrets *providerSecretCodec
}

// NewProviderSQLiteStore creates a new SQLite-backed provider store using an existing database connection.
func NewProviderSQLiteStore(db *sql.DB) (*ProviderSQLiteStore, error) {
	if db == nil {
		return nil, errors.New("provider sqlite store: db is nil")
	}

	if _, err := db.Exec(providerSQLiteSchema); err != nil {
		return nil, fmt.Errorf("provider sqlite store create schema: %w", err)
	}

	if err := migrateLegacyProviderSQLiteSchema(db); err != nil {
		return nil, err
	}

	scope, err := providerSQLiteScope(db)
	if err != nil {
		return nil, err
	}
	secrets, err := newProviderSecretCodec(scope)
	if err != nil {
		return nil, fmt.Errorf("provider sqlite store init secrets codec: %w", err)
	}

	return &ProviderSQLiteStore{db: db, secrets: secrets}, nil
}

// List returns all provider records ordered by creation time.
func (s *ProviderSQLiteStore) List(ctx context.Context) ([]ProviderRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, name, default_model, status, created_at, updated_at
FROM providers
ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("provider sqlite store list: %w", err)
	}
	defer rows.Close()

	var records []ProviderRecord
	for rows.Next() {
		rec, err := scanProviderRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provider sqlite store list rows: %w", err)
	}

	return records, nil
}

// Get retrieves a provider by ID.
func (s *ProviderSQLiteStore) Get(ctx context.Context, id string) (ProviderRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, type, name, default_model, status, created_at, updated_at
FROM providers
WHERE id = ?`, id)

	rec, err := scanProviderRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderRecord{}, false, nil
		}
		return ProviderRecord{}, false, err
	}
	return rec, true, nil
}

// Create adds a new provider record.
func (s *ProviderSQLiteStore) Create(ctx context.Context, rec ProviderRecord) error {
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	if rec.Status == "" {
		rec.Status = ProviderStatusDisconnected
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO providers (id, type, name, default_model, status, api_key_hash, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID,
		string(rec.Type),
		rec.Name,
		nullIfEmpty(rec.DefaultModel),
		string(rec.Status),
		nullIfEmpty(rec.APIKeyHash),
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isProviderSQLiteUniqueViolation(err) {
			return ErrProviderExists
		}
		return fmt.Errorf("provider sqlite store create: %w", err)
	}
	return nil
}

// Update modifies an existing provider record.
func (s *ProviderSQLiteStore) Update(ctx context.Context, rec ProviderRecord) error {
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE providers
SET type = ?, name = ?, default_model = ?, status = ?, updated_at = ?
WHERE id = ?`,
		string(rec.Type),
		rec.Name,
		nullIfEmpty(rec.DefaultModel),
		string(rec.Status),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		rec.ID,
	)
	if err != nil {
		return fmt.Errorf("provider sqlite store update: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("provider sqlite store update affected rows: %w", err)
	}
	if affected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// Delete removes a provider by ID.
func (s *ProviderSQLiteStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("provider sqlite store delete: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("provider sqlite store delete affected rows: %w", err)
	}
	if affected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// GetAPIKey retrieves the stored API key for a provider.
func (s *ProviderSQLiteStore) GetAPIKey(ctx context.Context, id string) (string, error) {
	var encrypted sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT api_key_enc FROM providers WHERE id = ?`, id).Scan(&encrypted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrProviderNotFound
		}
		return "", fmt.Errorf("provider sqlite store get api key: %w", err)
	}

	if strings.TrimSpace(encrypted.String) == "" {
		// Legacy rows may only contain api_key_hash; plaintext cannot be recovered.
		return "", nil
	}

	apiKey, err := s.secrets.Decrypt(encrypted.String)
	if err != nil {
		return "", fmt.Errorf("provider sqlite store decrypt api key: %w", err)
	}
	return apiKey, nil
}

// SetAPIKey stores an API key (encrypted) and hash for connectivity checks.
func (s *ProviderSQLiteStore) SetAPIKey(ctx context.Context, id string, apiKey string) error {
	hash := ""
	if strings.TrimSpace(apiKey) != "" {
		h := sha256.Sum256([]byte(apiKey))
		hash = hex.EncodeToString(h[:])
	}

	encrypted, err := s.secrets.Encrypt(apiKey)
	if err != nil {
		return fmt.Errorf("provider sqlite store encrypt api key: %w", err)
	}

	status := ProviderStatusDisconnected
	if strings.TrimSpace(apiKey) != "" {
		status = ProviderStatusConnected
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE providers
SET api_key_hash = ?, api_key_enc = ?, status = ?, updated_at = ?
WHERE id = ?`,
		nullIfEmpty(hash),
		nullIfEmpty(encrypted),
		string(status),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return fmt.Errorf("provider sqlite store set api key: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("provider sqlite store set api key affected rows: %w", err)
	}
	if affected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// Close is a no-op since we share the database connection.
func (s *ProviderSQLiteStore) Close() error {
	return nil
}

type providerScanner interface {
	Scan(dest ...any) error
}

func scanProviderRecord(scanner providerScanner) (ProviderRecord, error) {
	var (
		id           string
		provType     string
		name         string
		defaultModel sql.NullString
		status       string
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(&id, &provType, &name, &defaultModel, &status, &createdAt, &updatedAt); err != nil {
		return ProviderRecord{}, err
	}

	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ProviderRecord{}, fmt.Errorf("provider sqlite store parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ProviderRecord{}, fmt.Errorf("provider sqlite store parse updated_at: %w", err)
	}

	return ProviderRecord{
		ID:           id,
		Type:         ProviderType(provType),
		Name:         name,
		DefaultModel: defaultModel.String,
		Status:       ProviderStatus(status),
		CreatedAt:    created,
		UpdatedAt:    updated,
	}, nil
}

func isProviderSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: providers.id")
}

func migrateLegacyProviderSQLiteSchema(db *sql.DB) error {
	columns, err := sqliteTableColumns(db, "providers")
	if err != nil {
		return err
	}

	if !columns["api_key_enc"] {
		if _, err := db.Exec(`ALTER TABLE providers ADD COLUMN api_key_enc TEXT`); err != nil {
			return fmt.Errorf("provider sqlite store add providers.api_key_enc: %w", err)
		}
	}

	return nil
}

func providerSQLiteScope(db *sql.DB) (string, error) {
	var (
		seq  int
		name string
		path string
	)
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		return "", fmt.Errorf("provider sqlite store resolve scope: %w", err)
	}
	scope := strings.TrimSpace(path)
	if scope == "" {
		scope = "providers"
	}
	return scope, nil
}

var _ ProviderStore = (*ProviderSQLiteStore)(nil)

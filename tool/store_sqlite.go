package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

	if err := migrateLegacySQLiteSchema(db, scope); err != nil {
		_ = db.Close()
		return nil, err
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

func migrateLegacySQLiteSchema(db *sql.DB, scope string) error {
	if db == nil {
		return errors.New("tool: sqlite store db is nil")
	}

	columns, err := sqliteTableColumns(db, "tool_registrations")
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	if !columns["name"] {
		return errors.New("tool: sqlite schema missing tool_registrations.name column")
	}

	hasPayload := columns["payload"]
	if !hasPayload {
		if _, err := db.Exec(`ALTER TABLE tool_registrations ADD COLUMN payload BLOB`); err != nil {
			return fmt.Errorf("tool: sqlite add payload column: %w", err)
		}
	}
	if !columns["updated_at"] {
		if _, err := db.Exec(`ALTER TABLE tool_registrations ADD COLUMN updated_at TEXT`); err != nil {
			return fmt.Errorf("tool: sqlite add updated_at column: %w", err)
		}
	}

	if !hasPayload && columns["manifest_json"] {
		if err := backfillLegacyPayloadFromColumnSchema(db, scope, columns); err != nil {
			return err
		}
	}

	if err := backfillMissingPayloadDefaults(db, scope); err != nil {
		return err
	}
	return nil
}

func sqliteTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("tool: sqlite inspect schema for %s: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("tool: sqlite scan schema for %s: %w", table, err)
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tool: sqlite schema rows for %s: %w", table, err)
	}

	return columns, nil
}

func backfillLegacyPayloadFromColumnSchema(db *sql.DB, scope string, columns map[string]bool) error {
	store := &SQLiteStore{db: db, scope: scope}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	manifestExpr := sqliteLegacyColumnExpr(columns, "manifest_json", "''")
	originExpr := sqliteLegacyColumnExpr(columns, "origin", "''")
	configExpr := sqliteLegacyColumnExpr(columns, "config_json", "'{}'")
	statusExpr := sqliteLegacyColumnExpr(columns, "status", "''")
	registeredExpr := sqliteLegacyColumnExpr(columns, "registered_at", "''")
	lastCheckExpr := sqliteLegacyColumnExpr(columns, "last_health_check", "''")
	healthFailuresExpr := sqliteLegacyColumnExpr(columns, "health_failures", "0")
	overlayExpr := sqliteLegacyColumnExpr(columns, "overlay_path", "NULL")
	enabledExpr := sqliteLegacyColumnExpr(columns, "enabled", "1")

	query := fmt.Sprintf(`
SELECT name, %s, %s, %s, %s, %s, %s, %s, %s, %s
FROM tool_registrations`,
		manifestExpr,
		originExpr,
		configExpr,
		statusExpr,
		registeredExpr,
		lastCheckExpr,
		healthFailuresExpr,
		overlayExpr,
		enabledExpr,
	)

	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("tool: sqlite read legacy registrations: %w", err)
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("tool: sqlite begin legacy migration: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for rows.Next() {
		var (
			name            string
			manifestJSON    string
			origin          string
			configJSON      string
			status          string
			registeredAtRaw string
			lastCheckRaw    string
			healthFailures  int
			overlayPath     sql.NullString
			enabledRaw      any
		)
		if err := rows.Scan(
			&name,
			&manifestJSON,
			&origin,
			&configJSON,
			&status,
			&registeredAtRaw,
			&lastCheckRaw,
			&healthFailures,
			&overlayPath,
			&enabledRaw,
		); err != nil {
			return fmt.Errorf("tool: sqlite scan legacy registration: %w", err)
		}

		reg := decodeLegacySQLiteRegistration(
			name,
			manifestJSON,
			origin,
			configJSON,
			status,
			registeredAtRaw,
			lastCheckRaw,
			healthFailures,
			overlayPath,
			sqliteAnyToBool(enabledRaw, true),
		)

		payload, err := store.encodeRegistration(reg)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(
			`UPDATE tool_registrations SET payload = ?, updated_at = COALESCE(updated_at, ?) WHERE name = ?`,
			payload,
			now,
			reg.Name,
		); err != nil {
			return fmt.Errorf("tool: sqlite update migrated payload for %q: %w", reg.Name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tool: sqlite iterate legacy registrations: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("tool: sqlite commit legacy migration: %w", err)
	}
	return nil
}

func sqliteLegacyColumnExpr(columns map[string]bool, name string, fallback string) string {
	if columns[strings.ToLower(strings.TrimSpace(name))] {
		return name
	}
	return fallback
}

func decodeLegacySQLiteRegistration(
	name string,
	manifestJSON string,
	origin string,
	configJSON string,
	status string,
	registeredAtRaw string,
	lastCheckRaw string,
	healthFailures int,
	overlayPath sql.NullString,
	enabled bool,
) ToolRegistration {
	trimmedName := strings.TrimSpace(name)
	reg := ToolRegistration{
		Name:           trimmedName,
		Manifest:       NewManifest(trimmedName),
		Origin:         ToolOrigin(strings.TrimSpace(origin)),
		Status:         Status(strings.TrimSpace(status)),
		Config:         map[string]string{},
		HealthFailures: healthFailures,
		Enabled:        enabled,
	}

	if reg.Status == "" {
		reg.Status = StatusUnverified
	}

	if strings.TrimSpace(manifestJSON) != "" {
		var manifest ToolManifest
		if err := json.Unmarshal([]byte(manifestJSON), &manifest); err == nil {
			if strings.TrimSpace(manifest.Tool.Name) == "" {
				manifest.Tool.Name = trimmedName
			}
			if strings.TrimSpace(manifest.ManifestVersion) == "" {
				manifest.ManifestVersion = ManifestVersionV1
			}
			if strings.TrimSpace(manifest.Schema) == "" {
				manifest.Schema = SchemaToolV1
			}
			if manifest.Actions == nil {
				manifest.Actions = map[string]ActionSpec{}
			}
			reg.Manifest = manifest
		}
	}

	if strings.TrimSpace(configJSON) != "" {
		_ = json.Unmarshal([]byte(configJSON), &reg.Config)
		if reg.Config == nil {
			reg.Config = map[string]string{}
		}
	}

	if registeredAt, err := parseSQLiteTimeValue(registeredAtRaw); err == nil {
		reg.RegisteredAt = registeredAt
	}
	if lastCheck, err := parseSQLiteTimeValue(lastCheckRaw); err == nil {
		reg.LastHealthCheck = lastCheck
	}

	if overlayPath.Valid && strings.TrimSpace(overlayPath.String) != "" {
		reg.Overlay = &ToolOverlay{Path: overlayPath.String}
	}

	return reg
}

func backfillMissingPayloadDefaults(db *sql.DB, scope string) error {
	store := &SQLiteStore{db: db, scope: scope}
	rows, err := db.Query(`SELECT name FROM tool_registrations WHERE payload IS NULL`)
	if err != nil {
		return fmt.Errorf("tool: sqlite query registrations missing payload: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("tool: sqlite begin default payload backfill: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("tool: sqlite scan missing payload row: %w", err)
		}
		reg := ToolRegistration{
			Name:     name,
			Manifest: NewManifest(name),
			Status:   StatusUnverified,
			Enabled:  true,
		}
		payload, err := store.encodeRegistration(reg)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE tool_registrations SET payload = ?, updated_at = COALESCE(updated_at, ?) WHERE name = ?`,
			payload,
			now,
			name,
		); err != nil {
			return fmt.Errorf("tool: sqlite default payload backfill for %q: %w", name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tool: sqlite iterate missing payload rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("tool: sqlite commit default payload backfill: %w", err)
	}
	return nil
}

func parseSQLiteTimeValue(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func sqliteAnyToBool(value any, fallback bool) bool {
	switch v := value.(type) {
	case nil:
		return fallback
	case bool:
		return v
	case int64:
		return v != 0
	case int32:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case []byte:
		s := strings.TrimSpace(string(v))
		if s == "" {
			return fallback
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i != 0
		}
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
		return fallback
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return fallback
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i != 0
		}
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
		return fallback
	default:
		return fallback
	}
}

var _ Store = (*SQLiteStore)(nil)

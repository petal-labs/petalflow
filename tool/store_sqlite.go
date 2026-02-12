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

	storagesqlite "github.com/petal-labs/petalflow/storage/sqlite"
)

const toolSQLiteMigrationV1 = `
CREATE TABLE IF NOT EXISTS tool_registrations (
  name TEXT PRIMARY KEY,
  manifest_json TEXT NOT NULL,
  origin TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL,
  registered_at TEXT,
  last_health_check TEXT,
  health_failures INTEGER NOT NULL DEFAULT 0,
  overlay_path TEXT,
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_tool_registrations_status ON tool_registrations (status);
CREATE INDEX IF NOT EXISTS idx_tool_registrations_enabled ON tool_registrations (enabled);
`

var errEmptySQLiteStorePath = errors.New("tool: sqlite store path is empty")

type legacyFileStoreDocument struct {
	Version string             `json:"version"`
	Tools   []ToolRegistration `json:"tools"`
}

// SQLiteStore persists tool registrations in a shared SQLite database.
type SQLiteStore struct {
	path string
	db   *sql.DB
}

// NewSQLiteStore creates a SQLite-backed tool store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return nil, errEmptySQLiteStorePath
	}

	db, err := storagesqlite.Open(clean)
	if err != nil {
		return nil, err
	}
	if err := storagesqlite.ApplyMigrations(db, []storagesqlite.Migration{
		{
			Name: "tool_registrations_v1",
			SQL:  toolSQLiteMigrationV1,
		},
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &SQLiteStore{
		path: clean,
		db:   db,
	}
	if err := store.migrateLegacyDefaultFileStore(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewDefaultSQLiteStore creates a tool store at the shared default DB path.
func NewDefaultSQLiteStore() (*SQLiteStore, error) {
	path, err := DefaultSQLiteStorePath()
	if err != nil {
		return nil, err
	}
	return NewSQLiteStore(path)
}

// DefaultSQLiteStorePath returns the default SQLite DB path.
func DefaultSQLiteStorePath() (string, error) {
	return storagesqlite.DefaultPath()
}

// Path returns the backing DB path.
func (s *SQLiteStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close releases the SQLite handle.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// List returns all registrations in deterministic name order.
func (s *SQLiteStore) List(ctx context.Context) ([]ToolRegistration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, errors.New("tool: sqlite store is nil")
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT name, manifest_json, origin, config_json, status, registered_at, last_health_check, health_failures, overlay_path, enabled
FROM tool_registrations
ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("tool: list registrations: %w", err)
	}
	defer rows.Close()

	regs := make([]ToolRegistration, 0)
	for rows.Next() {
		reg, err := s.scanRegistration(rows)
		if err != nil {
			return nil, err
		}
		regs = append(regs, reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tool: list registrations rows: %w", err)
	}
	return regs, nil
}

// Get returns a registration by name.
func (s *SQLiteStore) Get(ctx context.Context, name string) (ToolRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolRegistration{}, false, err
	}
	if s == nil || s.db == nil {
		return ToolRegistration{}, false, errors.New("tool: sqlite store is nil")
	}

	var (
		dbName          string
		manifestJSON    string
		origin          string
		configJSON      string
		status          string
		registeredAtRaw string
		lastCheckRaw    string
		healthFailures  int
		overlayPath     sql.NullString
		enabledInt      int
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT name, manifest_json, origin, config_json, status, registered_at, last_health_check, health_failures, overlay_path, enabled
		 FROM tool_registrations WHERE name = ?`,
		strings.TrimSpace(name),
	).Scan(
		&dbName,
		&manifestJSON,
		&origin,
		&configJSON,
		&status,
		&registeredAtRaw,
		&lastCheckRaw,
		&healthFailures,
		&overlayPath,
		&enabledInt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolRegistration{}, false, nil
		}
		return ToolRegistration{}, false, fmt.Errorf("tool: get registration %q: %w", name, err)
	}

	reg, err := s.decodeRegistration(
		dbName,
		manifestJSON,
		origin,
		configJSON,
		status,
		registeredAtRaw,
		lastCheckRaw,
		healthFailures,
		overlayPath,
		enabledInt,
	)
	if err != nil {
		return ToolRegistration{}, false, err
	}
	return reg, true, nil
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

	now := time.Now().UTC()
	existing, found, err := s.Get(ctx, reg.Name)
	if err != nil {
		return err
	}

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

	stored := cloneRegistration(reg)
	if stored.Config == nil {
		stored.Config = map[string]string{}
	}
	if err := s.encryptSensitiveConfig(stored.Manifest, stored.Config); err != nil {
		return err
	}

	manifestJSON, err := json.Marshal(stored.Manifest)
	if err != nil {
		return fmt.Errorf("tool: encode manifest for %q: %w", stored.Name, err)
	}
	configJSON, err := json.Marshal(stored.Config)
	if err != nil {
		return fmt.Errorf("tool: encode config for %q: %w", stored.Name, err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_registrations
  (name, manifest_json, origin, config_json, status, registered_at, last_health_check, health_failures, overlay_path, enabled)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  manifest_json = excluded.manifest_json,
  origin = excluded.origin,
  config_json = excluded.config_json,
  status = excluded.status,
  registered_at = excluded.registered_at,
  last_health_check = excluded.last_health_check,
  health_failures = excluded.health_failures,
  overlay_path = excluded.overlay_path,
  enabled = excluded.enabled`,
		stored.Name,
		string(manifestJSON),
		string(stored.Origin),
		string(configJSON),
		string(stored.Status),
		formatSQLiteTime(stored.RegisteredAt),
		formatSQLiteTime(stored.LastHealthCheck),
		stored.HealthFailures,
		overlayPathValue(stored.Overlay),
		boolToSQLiteInt(stored.Enabled),
	)
	if err != nil {
		return fmt.Errorf("tool: upsert registration %q: %w", reg.Name, err)
	}
	return nil
}

// Delete removes a registration by name. Missing names are ignored.
func (s *SQLiteStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("tool: sqlite store is nil")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tool_registrations WHERE name = ?`, strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("tool: delete registration %q: %w", name, err)
	}
	return nil
}

func (s *SQLiteStore) scanRegistration(rows *sql.Rows) (ToolRegistration, error) {
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
		enabledInt      int
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
		&enabledInt,
	); err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: scan registration: %w", err)
	}
	return s.decodeRegistration(
		name,
		manifestJSON,
		origin,
		configJSON,
		status,
		registeredAtRaw,
		lastCheckRaw,
		healthFailures,
		overlayPath,
		enabledInt,
	)
}

func (s *SQLiteStore) decodeRegistration(
	name string,
	manifestJSON string,
	origin string,
	configJSON string,
	status string,
	registeredAtRaw string,
	lastCheckRaw string,
	healthFailures int,
	overlayPath sql.NullString,
	enabledInt int,
) (ToolRegistration, error) {
	var manifest ToolManifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: decode manifest for %q: %w", name, err)
	}

	config := map[string]string{}
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return ToolRegistration{}, fmt.Errorf("tool: decode config for %q: %w", name, err)
		}
	}
	if err := s.decryptSensitiveConfig(manifest, config); err != nil {
		return ToolRegistration{}, err
	}

	registeredAt, err := parseSQLiteTime(registeredAtRaw)
	if err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: parse registered_at for %q: %w", name, err)
	}
	lastHealthCheck, err := parseSQLiteTime(lastCheckRaw)
	if err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: parse last_health_check for %q: %w", name, err)
	}

	reg := ToolRegistration{
		Name:            name,
		Manifest:        manifest,
		Origin:          ToolOrigin(origin),
		Config:          config,
		Status:          Status(status),
		RegisteredAt:    registeredAt,
		LastHealthCheck: lastHealthCheck,
		HealthFailures:  healthFailures,
		Enabled:         enabledInt != 0,
	}
	if reg.Status == "" {
		reg.Status = StatusUnverified
	}
	if overlayPath.Valid && strings.TrimSpace(overlayPath.String) != "" {
		reg.Overlay = &ToolOverlay{Path: overlayPath.String}
	}
	return reg, nil
}

func (s *SQLiteStore) encryptSensitiveConfig(manifest ToolManifest, config map[string]string) error {
	if len(config) == 0 || len(manifest.Config) == 0 {
		return nil
	}
	codec, err := newSecretCodec(s.path)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		encrypted, err := codec.Encrypt(value)
		if err != nil {
			return fmt.Errorf("tool: encrypt config %q: %w", key, err)
		}
		config[key] = encrypted
	}
	return nil
}

func (s *SQLiteStore) decryptSensitiveConfig(manifest ToolManifest, config map[string]string) error {
	if len(config) == 0 || len(manifest.Config) == 0 {
		return nil
	}
	codec, err := newSecretCodec(s.path)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		plain, err := codec.Decrypt(value)
		if err != nil {
			return fmt.Errorf("tool: decrypt config %q: %w", key, err)
		}
		config[key] = plain
	}
	return nil
}

func formatSQLiteTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseSQLiteTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func overlayPathValue(overlay *ToolOverlay) any {
	if overlay == nil || strings.TrimSpace(overlay.Path) == "" {
		return nil
	}
	return overlay.Path
}

func (s *SQLiteStore) migrateLegacyDefaultFileStore(ctx context.Context) error {
	defaultPath, err := DefaultSQLiteStorePath()
	if err != nil {
		return err
	}
	if filepath.Clean(defaultPath) != filepath.Clean(s.path) {
		return nil
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tool_registrations`).Scan(&count); err != nil {
		return fmt.Errorf("tool: count sqlite registrations: %w", err)
	}
	if count > 0 {
		return nil
	}

	legacyPath, err := defaultLegacyFileStorePath()
	if err != nil {
		return err
	}
	if filepath.Clean(legacyPath) == filepath.Clean(s.path) {
		return nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("tool: stat legacy store %q: %w", legacyPath, err)
	}

	regs, err := loadLegacyFileRegistrations(legacyPath)
	if err != nil {
		return fmt.Errorf("tool: load legacy file store %q: %w", legacyPath, err)
	}
	for _, reg := range regs {
		if err := s.Upsert(ctx, reg); err != nil {
			return fmt.Errorf("tool: migrate registration %q: %w", reg.Name, err)
		}
	}
	return nil
}

func defaultLegacyFileStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("tool: resolve user home: %w", err)
	}
	return filepath.Join(home, ".petalflow", "tools.json"), nil
}

func loadLegacyFileRegistrations(path string) ([]ToolRegistration, error) {
	// #nosec G304 -- path is resolved from local default migration location.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return []ToolRegistration{}, nil
	}

	var doc legacyFileStoreDocument
	if err := json.Unmarshal(data, &doc); err == nil && doc.Tools != nil {
		if err := decryptLegacySensitiveRegistrations(path, doc.Tools); err != nil {
			return nil, err
		}
		sortRegistrations(doc.Tools)
		return cloneRegistrations(doc.Tools), nil
	}

	var regs []ToolRegistration
	if err := json.Unmarshal(data, &regs); err != nil {
		return nil, fmt.Errorf("tool: decode legacy registrations: %w", err)
	}
	if err := decryptLegacySensitiveRegistrations(path, regs); err != nil {
		return nil, err
	}
	sortRegistrations(regs)
	return cloneRegistrations(regs), nil
}

func decryptLegacySensitiveRegistrations(path string, regs []ToolRegistration) error {
	codec, err := newSecretCodec(path)
	if err != nil {
		return fmt.Errorf("tool: initialize legacy secret codec: %w", err)
	}
	for i := range regs {
		if len(regs[i].Config) == 0 || len(regs[i].Manifest.Config) == 0 {
			continue
		}
		for key, spec := range regs[i].Manifest.Config {
			if !spec.Sensitive {
				continue
			}
			value := regs[i].Config[key]
			if strings.TrimSpace(value) == "" {
				continue
			}
			plain, err := codec.Decrypt(value)
			if err != nil {
				return fmt.Errorf("tool: decrypt legacy config %q for %s: %w", key, regs[i].Name, err)
			}
			regs[i].Config[key] = plain
		}
	}
	return nil
}

var _ Store = (*SQLiteStore)(nil)

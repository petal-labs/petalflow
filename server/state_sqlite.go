package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/hydrate"
	storagesqlite "github.com/petal-labs/petalflow/storage/sqlite"
)

const stateSQLiteMigrationV1 = `
CREATE TABLE IF NOT EXISTS server_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  auth_user_json TEXT,
  settings_json TEXT NOT NULL DEFAULT '{}',
  providers_json TEXT NOT NULL DEFAULT '{}',
  provider_meta_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO server_state
  (id, auth_user_json, settings_json, providers_json, provider_meta_json, updated_at)
VALUES
  (1, NULL, '{}', '{}', '{}', '');
`

// SQLiteStateStore persists server auth/settings/providers in SQLite.
type SQLiteStateStore struct {
	path string
	db   *sql.DB
}

// NewSQLiteStateStore creates a state store at the shared DB path.
func NewSQLiteStateStore(path string) (*SQLiteStateStore, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return nil, fmt.Errorf("server: sqlite state store path is required")
	}

	db, err := storagesqlite.Open(clean)
	if err != nil {
		return nil, err
	}
	if err := storagesqlite.ApplyMigrations(db, []storagesqlite.Migration{
		{
			Name: "server_state_v1",
			SQL:  stateSQLiteMigrationV1,
		},
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &SQLiteStateStore{
		path: clean,
		db:   db,
	}
	if err := store.migrateLegacyDefaultFileState(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStateStore) migrateLegacyDefaultFileState() error {
	defaultDBPath, err := storagesqlite.DefaultPath()
	if err != nil {
		return err
	}
	if filepath.Clean(defaultDBPath) != filepath.Clean(s.path) {
		return nil
	}

	var (
		authUserJSON     sql.NullString
		settingsJSON     string
		providersJSON    string
		providerMetaJSON string
	)
	err = s.db.QueryRow(
		`SELECT auth_user_json, settings_json, providers_json, provider_meta_json
		 FROM server_state
		 WHERE id = 1`,
	).Scan(&authUserJSON, &settingsJSON, &providersJSON, &providerMetaJSON)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("server: inspect sqlite state for migration: %w", err)
	}
	if authUserJSON.Valid && strings.TrimSpace(authUserJSON.String) != "" {
		return nil
	}
	if strings.TrimSpace(settingsJSON) != "" && strings.TrimSpace(settingsJSON) != "{}" {
		return nil
	}
	if strings.TrimSpace(providersJSON) != "" && strings.TrimSpace(providersJSON) != "{}" {
		return nil
	}
	if strings.TrimSpace(providerMetaJSON) != "" && strings.TrimSpace(providerMetaJSON) != "{}" {
		return nil
	}

	legacyPath, err := DefaultStateStorePath()
	if err != nil {
		return err
	}
	if filepath.Clean(legacyPath) == filepath.Clean(s.path) {
		return nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		if err == os.ErrNotExist || os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("server: stat legacy state store %q: %w", legacyPath, err)
	}

	legacyStore := NewFileStateStore(legacyPath)
	state, err := legacyStore.Load()
	if err != nil {
		return fmt.Errorf("server: load legacy state store %q: %w", legacyPath, err)
	}
	if state.AuthUser == nil && state.Settings == defaultAppSettings() && len(state.Providers) == 0 && len(state.ProviderMeta) == 0 {
		return nil
	}
	if err := s.Save(state); err != nil {
		return fmt.Errorf("server: migrate legacy state store %q: %w", legacyPath, err)
	}
	return nil
}

// NewDefaultSQLiteStateStore creates a state store using the default DB path.
func NewDefaultSQLiteStateStore() (*SQLiteStateStore, error) {
	path, err := storagesqlite.DefaultPath()
	if err != nil {
		return nil, err
	}
	return NewSQLiteStateStore(path)
}

// Path returns the backing DB path.
func (s *SQLiteStateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close releases the SQLite handle.
func (s *SQLiteStateStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Load returns persisted state.
func (s *SQLiteStateStore) Load() (serverState, error) {
	var (
		authUserJSON     sql.NullString
		settingsJSON     string
		providersJSON    string
		providerMetaJSON string
	)
	err := s.db.QueryRow(
		`SELECT auth_user_json, settings_json, providers_json, provider_meta_json
		 FROM server_state
		 WHERE id = 1`,
	).Scan(&authUserJSON, &settingsJSON, &providersJSON, &providerMetaJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return serverState{}, nil
		}
		return serverState{}, fmt.Errorf("server: load sqlite state: %w", err)
	}

	state := serverState{
		Settings:     defaultAppSettings(),
		Providers:    hydrate.ProviderMap{},
		ProviderMeta: map[string]providerMetadata{},
	}

	if authUserJSON.Valid && strings.TrimSpace(authUserJSON.String) != "" {
		var user authAccount
		if err := json.Unmarshal([]byte(authUserJSON.String), &user); err != nil {
			return serverState{}, fmt.Errorf("server: decode sqlite auth user: %w", err)
		}
		state.AuthUser = &user
	}

	if strings.TrimSpace(settingsJSON) != "" {
		if err := json.Unmarshal([]byte(settingsJSON), &state.Settings); err != nil {
			return serverState{}, fmt.Errorf("server: decode sqlite settings: %w", err)
		}
	}
	if strings.TrimSpace(providersJSON) != "" {
		if err := json.Unmarshal([]byte(providersJSON), &state.Providers); err != nil {
			return serverState{}, fmt.Errorf("server: decode sqlite providers: %w", err)
		}
	}
	if strings.TrimSpace(providerMetaJSON) != "" {
		if err := json.Unmarshal([]byte(providerMetaJSON), &state.ProviderMeta); err != nil {
			return serverState{}, fmt.Errorf("server: decode sqlite provider metadata: %w", err)
		}
	}

	state.AuthUser = cloneAuthAccount(state.AuthUser)
	state.Settings = normalizeAppSettings(state.Settings)
	state.Providers = cloneProviderMap(state.Providers)
	state.ProviderMeta = cloneProviderMetaMap(state.ProviderMeta)
	return state, nil
}

// Save writes state atomically.
func (s *SQLiteStateStore) Save(state serverState) error {
	state.AuthUser = cloneAuthAccount(state.AuthUser)
	state.Settings = normalizeAppSettings(state.Settings)
	state.Providers = cloneProviderMap(state.Providers)
	state.ProviderMeta = cloneProviderMetaMap(state.ProviderMeta)

	var authUserJSON any
	if state.AuthUser != nil {
		data, err := json.Marshal(state.AuthUser)
		if err != nil {
			return fmt.Errorf("server: encode sqlite auth user: %w", err)
		}
		authUserJSON = string(data)
	}

	settingsJSON, err := json.Marshal(state.Settings)
	if err != nil {
		return fmt.Errorf("server: encode sqlite settings: %w", err)
	}
	providersJSON, err := json.Marshal(state.Providers)
	if err != nil {
		return fmt.Errorf("server: encode sqlite providers: %w", err)
	}
	providerMetaJSON, err := json.Marshal(state.ProviderMeta)
	if err != nil {
		return fmt.Errorf("server: encode sqlite provider metadata: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO server_state
		   (id, auth_user_json, settings_json, providers_json, provider_meta_json, updated_at)
		 VALUES
		   (1, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   auth_user_json = excluded.auth_user_json,
		   settings_json = excluded.settings_json,
		   providers_json = excluded.providers_json,
		   provider_meta_json = excluded.provider_meta_json,
		   updated_at = excluded.updated_at`,
		authUserJSON,
		string(settingsJSON),
		string(providersJSON),
		string(providerMetaJSON),
		formatSQLiteTime(time.Now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("server: save sqlite state: %w", err)
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

var _ ServerStateStore = (*SQLiteStateStore)(nil)

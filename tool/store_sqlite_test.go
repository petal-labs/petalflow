package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSQLiteToolStore(t *testing.T) *SQLiteStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tools.db")
	store, err := NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteStoreUpsertGetDeleteRoundTrip(t *testing.T) {
	store := newSQLiteToolStore(t)
	ctx := context.Background()

	reg := ToolRegistration{
		Name:     "s3_fetch",
		Origin:   OriginMCP,
		Manifest: NewManifest("s3_fetch"),
		Status:   StatusReady,
		Config: map[string]string{
			"region": "us-west-2",
		},
		RegisteredAt:    time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC),
		LastHealthCheck: time.Date(2026, 2, 9, 1, 0, 0, 0, time.UTC),
		Overlay: &ToolOverlay{
			Path: "./tools/s3.overlay.yaml",
		},
	}
	reg.Manifest.Transport = NewMCPTransport(MCPTransport{
		Mode:    MCPModeStdio,
		Command: "npx",
	})

	if err := store.Upsert(ctx, reg); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, ok, err := store.Get(ctx, "s3_fetch")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.Name != "s3_fetch" {
		t.Fatalf("Name = %q, want s3_fetch", got.Name)
	}
	if got.Config["region"] != "us-west-2" {
		t.Fatalf("Config[region] = %q, want us-west-2", got.Config["region"])
	}
	if got.Origin != OriginMCP {
		t.Fatalf("Origin = %q, want %q", got.Origin, OriginMCP)
	}
	if got.Overlay == nil || got.Overlay.Path == "" {
		t.Fatal("Overlay should be present")
	}

	if err := store.Delete(ctx, "s3_fetch"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, ok, err = store.Get(ctx, "s3_fetch")
	if err != nil {
		t.Fatalf("Get() after delete error = %v", err)
	}
	if ok {
		t.Fatal("Get() after delete ok = true, want false")
	}
}

func TestSQLiteStoreListOrder(t *testing.T) {
	store := newSQLiteToolStore(t)
	ctx := context.Background()

	regA := ToolRegistration{
		Name:     "alpha",
		Origin:   OriginHTTP,
		Manifest: NewManifest("alpha"),
		Status:   StatusUnverified,
	}
	regA.Manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9001"})

	regB := ToolRegistration{
		Name:     "beta",
		Origin:   OriginStdio,
		Manifest: NewManifest("beta"),
		Status:   StatusReady,
	}
	regB.Manifest.Transport = NewStdioTransport(StdioTransport{Command: "./beta"})

	if err := store.Upsert(ctx, regB); err != nil {
		t.Fatalf("Upsert(beta) error = %v", err)
	}
	if err := store.Upsert(ctx, regA); err != nil {
		t.Fatalf("Upsert(alpha) error = %v", err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(list))
	}
	if list[0].Name != "alpha" || list[1].Name != "beta" {
		t.Fatalf("List order = [%s, %s], want [alpha, beta]", list[0].Name, list[1].Name)
	}
}

func TestSQLiteStoreEncryptsSensitiveConfigAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.db")
	store, err := NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ctx := context.Background()

	manifest := NewManifest("secure_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9901"})
	manifest.Actions["run"] = ActionSpec{
		Outputs: map[string]FieldSpec{
			"ok": {Type: TypeBoolean},
		},
	}
	manifest.Config = map[string]FieldSpec{
		"api_key": {Type: TypeString, Sensitive: true},
	}

	reg := ToolRegistration{
		Name:     "secure_tool",
		Origin:   OriginHTTP,
		Manifest: manifest,
		Status:   StatusReady,
		Enabled:  true,
		Config: map[string]string{
			"api_key": "super-secret-value",
		},
	}

	if err := store.Upsert(ctx, reg); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var payload string
	if err := db.QueryRow(`SELECT payload FROM tool_registrations WHERE name = ?`, "secure_tool").Scan(&payload); err != nil {
		t.Fatalf("query payload error = %v", err)
	}
	if strings.Contains(payload, "super-secret-value") {
		t.Fatalf("sqlite payload leaked plaintext secret: %s", payload)
	}
	if !strings.Contains(payload, encryptedValuePrefix) {
		t.Fatalf("sqlite payload missing encrypted value prefix %q", encryptedValuePrefix)
	}

	got, ok, err := store.Get(ctx, "secure_tool")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.Config["api_key"] != "super-secret-value" {
		t.Fatalf("decrypted api_key = %q, want super-secret-value", got.Config["api_key"])
	}
}

func TestSQLiteStorePersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tools.db")

	store1, err := NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store1): %v", err)
	}

	reg := ToolRegistration{
		Name:     "persisted_tool",
		Manifest: NewManifest("persisted_tool"),
		Origin:   OriginNative,
		Status:   StatusReady,
	}
	reg.Manifest.Transport = NewNativeTransport()
	reg.Manifest.Actions["run"] = ActionSpec{}

	if err := store1.Upsert(ctx, reg); err != nil {
		t.Fatalf("store1.Upsert: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close: %v", err)
	}

	store2, err := NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store2): %v", err)
	}
	t.Cleanup(func() {
		_ = store2.Close()
	})

	got, ok, err := store2.Get(ctx, "persisted_tool")
	if err != nil {
		t.Fatalf("store2.Get: %v", err)
	}
	if !ok {
		t.Fatal("store2.Get: expected persisted registration")
	}
	if got.Name != "persisted_tool" {
		t.Fatalf("store2.Get.Name = %q, want %q", got.Name, "persisted_tool")
	}
}

func TestSQLiteStoreMigratesLegacyColumnSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	legacySchema := `
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
);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema error = %v", err)
	}

	manifest := NewManifest("legacy_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9911"})
	manifest.Actions["run"] = ActionSpec{
		Outputs: map[string]FieldSpec{
			"ok": {Type: TypeBoolean},
		},
	}
	manifest.Config = map[string]FieldSpec{
		"api_key": {Type: TypeString, Sensitive: true},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest error = %v", err)
	}

	configJSON := `{"api_key":"legacy-secret"}`
	if _, err := db.Exec(
		`INSERT INTO tool_registrations
		  (name, manifest_json, origin, config_json, status, registered_at, last_health_check, health_failures, overlay_path, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy_tool",
		string(manifestJSON),
		string(OriginHTTP),
		configJSON,
		string(StatusReady),
		"2026-02-17T00:00:00Z",
		"2026-02-17T01:00:00Z",
		1,
		"./legacy.overlay.yaml",
		1,
	); err != nil {
		t.Fatalf("insert legacy row error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewSQLiteStore(SQLiteStoreConfig{
		DSN:   path,
		Scope: path,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	got, ok, err := store.Get(context.Background(), "legacy_tool")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.Origin != OriginHTTP {
		t.Fatalf("Origin = %q, want %q", got.Origin, OriginHTTP)
	}
	if got.Status != StatusReady {
		t.Fatalf("Status = %q, want %q", got.Status, StatusReady)
	}
	if got.Config["api_key"] != "legacy-secret" {
		t.Fatalf("Config[api_key] = %q, want legacy-secret", got.Config["api_key"])
	}
	if got.Overlay == nil || got.Overlay.Path != "./legacy.overlay.yaml" {
		t.Fatalf("Overlay = %#v, want ./legacy.overlay.yaml", got.Overlay)
	}

	var (
		payload   string
		updatedAt string
	)
	if err := store.db.QueryRow(
		`SELECT payload, updated_at FROM tool_registrations WHERE name = ?`,
		"legacy_tool",
	).Scan(&payload, &updatedAt); err != nil {
		t.Fatalf("query migrated payload error = %v", err)
	}
	if strings.TrimSpace(payload) == "" {
		t.Fatal("payload should be populated after migration")
	}
	if strings.Contains(payload, "legacy-secret") {
		t.Fatalf("migrated payload leaked plaintext secret: %s", payload)
	}
	if !strings.Contains(payload, encryptedValuePrefix) {
		t.Fatalf("migrated payload missing encrypted value prefix %q", encryptedValuePrefix)
	}
	if strings.TrimSpace(updatedAt) == "" {
		t.Fatal("updated_at should be populated after migration")
	}
}

package tool

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "petalflow.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteStoreListEmptyWhenMissing(t *testing.T) {
	store := newSQLiteTestStore(t)

	regs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(regs) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(regs))
	}
}

func TestSQLiteStoreUpsertGetDeleteRoundTrip(t *testing.T) {
	store := newSQLiteTestStore(t)
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

func TestSQLiteStoreDeterministicOrder(t *testing.T) {
	store := newSQLiteTestStore(t)
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

func TestSQLiteStoreEmptyPathError(t *testing.T) {
	_, err := NewSQLiteStore("")
	if err == nil {
		t.Fatal("NewSQLiteStore() error = nil, want non-nil for empty path")
	}
}

func TestSQLiteStoreEncryptsSensitiveConfigAtRest(t *testing.T) {
	store := newSQLiteTestStore(t)
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

	var rawConfig string
	if err := store.db.QueryRowContext(ctx, `SELECT config_json FROM tool_registrations WHERE name = ?`, "secure_tool").Scan(&rawConfig); err != nil {
		t.Fatalf("query config_json error = %v", err)
	}
	if strings.Contains(rawConfig, "super-secret-value") {
		t.Fatalf("sqlite row leaked plaintext secret: %s", rawConfig)
	}
	if !strings.Contains(rawConfig, encryptedValuePrefix) {
		t.Fatalf("sqlite row missing encrypted value prefix %q", encryptedValuePrefix)
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

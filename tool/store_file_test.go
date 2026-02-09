package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreListEmptyWhenMissing(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "tools.json"))

	regs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(regs) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(regs))
	}
}

func TestFileStoreUpsertGetDeleteRoundTrip(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "tools.json"))
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

func TestFileStoreDeterministicOrderAndVersionedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	store := NewFileStore(path)
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

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var doc fileStoreDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if doc.Version != fileStoreVersionV1 {
		t.Fatalf("version = %q, want %q", doc.Version, fileStoreVersionV1)
	}
	if len(doc.Tools) != 2 {
		t.Fatalf("len(doc.Tools) = %d, want 2", len(doc.Tools))
	}
}

func TestFileStoreEmptyPathError(t *testing.T) {
	store := NewFileStore("")
	if err := store.Upsert(context.Background(), ToolRegistration{Name: "x", Manifest: NewManifest("x")}); err == nil {
		t.Fatal("Upsert() error = nil, want non-nil for empty path")
	}
}

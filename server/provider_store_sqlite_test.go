package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderSQLiteStore_APIKeyPersistsAcrossReopen_EncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "providers.sqlite")

	store1, err := NewSQLiteStore(SQLiteStoreConfig{DSN: dbPath})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store1) error = %v", err)
	}
	providerStore1, err := NewProviderSQLiteStore(store1.DB())
	if err != nil {
		t.Fatalf("NewProviderSQLiteStore(store1) error = %v", err)
	}

	rec := ProviderRecord{
		ID:     "prov-1",
		Type:   ProviderTypeAnthropic,
		Name:   "Anthropic",
		Status: ProviderStatusDisconnected,
	}
	if err := providerStore1.Create(ctx, rec); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := providerStore1.SetAPIKey(ctx, rec.ID, "sk-ant-persisted"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var encrypted string
	if err := store1.DB().QueryRowContext(ctx, `SELECT api_key_enc FROM providers WHERE id = ?`, rec.ID).Scan(&encrypted); err != nil {
		t.Fatalf("query api_key_enc error = %v", err)
	}
	if strings.TrimSpace(encrypted) == "" {
		t.Fatal("api_key_enc should be non-empty")
	}
	if strings.Contains(encrypted, "sk-ant-persisted") {
		t.Fatalf("api_key_enc leaked plaintext API key: %q", encrypted)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close() error = %v", err)
	}

	store2, err := NewSQLiteStore(SQLiteStoreConfig{DSN: dbPath})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store2) error = %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })

	providerStore2, err := NewProviderSQLiteStore(store2.DB())
	if err != nil {
		t.Fatalf("NewProviderSQLiteStore(store2) error = %v", err)
	}

	apiKey, err := providerStore2.GetAPIKey(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if apiKey != "sk-ant-persisted" {
		t.Fatalf("GetAPIKey() = %q, want %q", apiKey, "sk-ant-persisted")
	}
}

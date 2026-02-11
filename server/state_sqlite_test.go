package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/hydrate"
)

func newSQLiteStateStore(t *testing.T) *SQLiteStateStore {
	t.Helper()
	store, err := NewSQLiteStateStore(filepath.Join(t.TempDir(), "petalflow.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStateStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteStateStoreLoadSave(t *testing.T) {
	store := newSQLiteStateStore(t)

	state := serverState{
		AuthUser: &authAccount{
			Username: "admin",
			Password: "secret",
		},
		Settings: AppSettings{
			OnboardingComplete: true,
			OnboardingStep:     2,
			Preferences: UserPreferences{
				Theme:               "light",
				DefaultWorkflowMode: "graph",
			},
		},
		Providers: hydrate.ProviderMap{
			"openai": {
				APIKey:  "sk-openai",
				BaseURL: "https://api.openai.com/v1",
			},
		},
		ProviderMeta: map[string]providerMetadata{
			"openai": {
				DefaultModel: "gpt-4o-mini",
				Verified:     true,
				LatencyMS:    123,
			},
		},
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AuthUser == nil {
		t.Fatal("AuthUser = nil, want non-nil")
	}
	if got.AuthUser.Username != "admin" || got.AuthUser.Password != "secret" {
		t.Fatalf("AuthUser = %+v, want admin/secret", got.AuthUser)
	}
	if !got.Settings.OnboardingComplete || got.Settings.OnboardingStep != 2 {
		t.Fatalf("Settings onboarding = %+v, want complete/step2", got.Settings)
	}
	if got.Settings.Preferences.Theme != "light" {
		t.Fatalf("Theme = %q, want light", got.Settings.Preferences.Theme)
	}
	if got.Providers["openai"].APIKey != "sk-openai" {
		t.Fatalf("providers[openai].api_key = %q, want sk-openai", got.Providers["openai"].APIKey)
	}
	if got.ProviderMeta["openai"].DefaultModel != "gpt-4o-mini" {
		t.Fatalf("provider_meta[openai].default_model = %q, want gpt-4o-mini", got.ProviderMeta["openai"].DefaultModel)
	}
}

func TestSQLiteStateStoreMigratesLegacyFileState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath, err := DefaultStateStorePath()
	if err != nil {
		t.Fatalf("DefaultStateStorePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyStore := NewFileStateStore(legacyPath)
	if err := legacyStore.Save(serverState{
		AuthUser: &authAccount{
			Username: "legacy-admin",
			Password: "legacy-secret",
		},
		Settings: AppSettings{
			OnboardingComplete: true,
			OnboardingStep:     3,
		},
	}); err != nil {
		t.Fatalf("legacy Save() error = %v", err)
	}

	store, err := NewDefaultSQLiteStateStore()
	if err != nil {
		t.Fatalf("NewDefaultSQLiteStateStore() error = %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AuthUser == nil {
		t.Fatal("AuthUser = nil, want migrated user")
	}
	if got.AuthUser.Username != "legacy-admin" {
		t.Fatalf("username = %q, want legacy-admin", got.AuthUser.Username)
	}
	if !got.Settings.OnboardingComplete || got.Settings.OnboardingStep != 3 {
		t.Fatalf("settings = %+v, want onboarding complete step 3", got.Settings)
	}
}

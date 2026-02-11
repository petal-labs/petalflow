package server

import (
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

package llmprovider

import (
	"reflect"
	"strings"
	"testing"

	"github.com/petal-labs/iris/providers"
	"github.com/petal-labs/petalflow/hydrate"
)

func TestNewClient_WiresBaseURLForKnownProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		providerName string
		cfg          hydrate.ProviderConfig
		wantType     string
	}{
		{
			name:         "openai",
			providerName: "openai",
			cfg: hydrate.ProviderConfig{
				APIKey:  "test-openai-key",
				BaseURL: "https://openai.example/v1",
			},
			wantType: "*openai.OpenAI",
		},
		{
			name:         "anthropic",
			providerName: "anthropic",
			cfg: hydrate.ProviderConfig{
				APIKey:  "test-anthropic-key",
				BaseURL: "https://anthropic.example",
			},
			wantType: "*anthropic.Anthropic",
		},
		{
			name:         "ollama",
			providerName: "ollama",
			cfg: hydrate.ProviderConfig{
				APIKey:  "test-ollama-key",
				BaseURL: "https://ollama.example",
			},
			wantType: "*ollama.Ollama",
		},
		{
			name:         "provider names are case-insensitive",
			providerName: "OpenAI",
			cfg: hydrate.ProviderConfig{
				APIKey:  "test-openai-key",
				BaseURL: "https://openai.example/v1",
			},
			wantType: "*openai.OpenAI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := NewClient(tt.providerName, tt.cfg)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			adapter, ok := client.(*irisAdapter)
			if !ok {
				t.Fatalf("expected *irisAdapter, got %T", client)
			}

			gotType := reflect.TypeOf(adapter.provider).String()
			if gotType != tt.wantType {
				t.Fatalf("provider type = %q, want %q", gotType, tt.wantType)
			}

			if gotBaseURL := readProviderConfigStringField(t, adapter.provider, "BaseURL"); gotBaseURL != tt.cfg.BaseURL {
				t.Fatalf("BaseURL = %q, want %q", gotBaseURL, tt.cfg.BaseURL)
			}
		})
	}
}

func TestNewClient_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := NewClient("definitely-not-a-provider", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "unknown provider")
	}
}

func readProviderConfigStringField(t *testing.T, p providers.Provider, field string) string {
	t.Helper()

	pv := reflect.ValueOf(p)
	if pv.Kind() != reflect.Ptr || pv.IsNil() {
		t.Fatalf("provider must be a non-nil pointer, got %T", p)
	}

	cfg := pv.Elem().FieldByName("config")
	if !cfg.IsValid() {
		t.Fatalf("provider %T has no config field", p)
	}

	v := cfg.FieldByName(field)
	if !v.IsValid() {
		t.Fatalf("provider %T config has no %q field", p, field)
	}
	if v.Kind() != reflect.String {
		t.Fatalf("provider %T config field %q has kind %s, want string", p, field, v.Kind())
	}

	return v.String()
}

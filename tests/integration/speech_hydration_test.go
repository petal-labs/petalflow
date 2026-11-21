//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

// TestSpeechNodes_DefaultHydrationCredentialResolution proves the acceptance
// criteria for hydration + credential resolution: speech nodes hydrate through
// the default factories (no explicit wiring), credentials resolve through the
// standard config-file flow, and unsupported providers fail hydration with a
// key-free error.
func TestSpeechNodes_DefaultHydrationCredentialResolution(t *testing.T) {
	// Credential resolution through the existing config-file flow.
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"providers": {"elevenlabs-mock": {"api_key": "ek-mock-secret-do-not-log"}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("PETALFLOW_CONFIG", configPath)

	providers, err := hydrate.ResolveProviders(nil)
	if err != nil {
		t.Fatalf("resolving providers: %v", err)
	}
	if providers["elevenlabs-mock"].APIKey != "ek-mock-secret-do-not-log" {
		t.Fatalf("credential not resolved: %+v", providers)
	}

	definition := `{
		"id": "speech-default-hydration",
		"entry": "tts",
		"nodes": [
			{
				"id": "tts",
				"type": "speech_synthesize",
				"config": {"provider": "elevenlabs-mock", "voice_id": "voice-1", "input_var": "text", "output_var": "audio_id"}
			},
			{
				"id": "stt",
				"type": "speech_transcribe",
				"config": {"provider": "elevenlabs-mock", "input_var": "audio", "audio_format": "mp3", "output_var": "transcript"}
			}
		],
		"edges": [
			{"source": "tts", "target": "stt"}
		]
	}`

	var def graph.GraphDefinition
	if err := json.Unmarshal([]byte(definition), &def); err != nil {
		t.Fatalf("parsing definition: %v", err)
	}
	if diags := def.ValidateWithRegistry(registry.Global()); graph.HasErrors(diags) {
		t.Fatalf("invalid definition: %v", graph.Errors(diags))
	}

	// No WithSpeechRecognizerFactory / WithSpeechSynthesizerFactory: the
	// llmprovider-registered defaults must resolve both nodes.
	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})
	g, err := hydrate.HydrateGraph(&def, providers, factory)
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().
		WithVar("text", "Resolve credentials from config")
	env.SetVar("audio", []byte("fake-audio"))
	env.Input = nil

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	if len(result.GetArtifactsByType("audio")) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(result.GetArtifactsByType("audio")))
	}
	transcript, ok := result.GetVar("transcript")
	if !ok || transcript == "" {
		t.Fatalf("expected transcript var, got %v", transcript)
	}
}

// TestSpeechNodes_UnsupportedCapabilityFailsHydration proves the unsupported
// capability validation: a provider without speech capabilities fails at
// hydration with a diagnosable error that never echoes the API key.
func TestSpeechNodes_UnsupportedCapabilityFailsHydration(t *testing.T) {
	const apiKey = "ek-mock-secret-do-not-log"
	providers := hydrate.ProviderMap{
		"chat-only-mock": {APIKey: apiKey},
	}

	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})

	var def graph.GraphDefinition
	definition := `{
		"id": "speech-unsupported",
		"nodes": [
			{
				"id": "stt",
				"type": "speech_transcribe",
				"config": {"provider": "chat-only-mock", "input_var": "audio", "audio_format": "mp3"}
			}
		]
	}`
	if err := json.Unmarshal([]byte(definition), &def); err != nil {
		t.Fatalf("parsing definition: %v", err)
	}

	_, err := hydrate.HydrateGraph(&def, providers, factory)
	if err == nil {
		t.Fatal("expected hydration to fail for a provider without speech capabilities")
	}
	if !strings.Contains(err.Error(), "does not support speech recognition") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("hydration error leaks API key: %q", err.Error())
	}
}

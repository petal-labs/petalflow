//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

// mockIntegrationSynthesizer, streamingMockSynthesizer,
// failingIntegrationSynthesizer, and blockingIntegrationSynthesizer live in
// speech_mocks_test.go alongside the recognizer fakes.

func TestSpeechSynthesizeNode_MockedRuntimeRun(t *testing.T) {
	node := nodes.NewSpeechSynthesizeNode("tts", mockIntegrationSynthesizer{}, nodes.SpeechSynthesizeNodeConfig{
		Model:       "eleven_flash_v2_5",
		VoiceID:     "voice-1",
		InputVar:    "text",
		AudioFormat: "mp3",
		OutputVar:   "audio_id",
		Timeout:     10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-mock-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "Deterministic synthesis")

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	// The output var references the artifact ID; audio bytes live on the
	// artifact, never in vars.
	audioID, ok := result.GetVar("audio_id")
	if !ok {
		t.Fatal("expected 'audio_id' var in envelope")
	}
	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if audioID != artifacts[0].ID {
		t.Errorf("audio_id = %v, want artifact ID %q", audioID, artifacts[0].ID)
	}
	if len(artifacts[0].Bytes) == 0 {
		t.Fatal("expected non-empty audio bytes on artifact")
	}
	if artifacts[0].Meta["voice_id"] != "voice-1" || artifacts[0].Meta["streamed"] != false {
		t.Errorf("artifact meta = %+v", artifacts[0].Meta)
	}
}

func TestSpeechSynthesizeNode_MockedHydratedGraph(t *testing.T) {
	// End-to-end: load a graph IR definition containing a speech_synthesize
	// node, validate it against the registry, hydrate it with a deterministic
	// synthesizer factory, and execute it.
	definition := `{
		"id": "tts-hydrated",
		"nodes": [
			{
				"id": "tts",
				"type": "speech_synthesize",
				"config": {
					"provider": "elevenlabs",
					"voice_id": "voice-1",
					"input_var": "text",
					"stream": true
				}
			}
		]
	}`

	var def graph.GraphDefinition
	if err := json.Unmarshal([]byte(definition), &def); err != nil {
		t.Fatalf("parsing definition: %v", err)
	}
	if diags := def.ValidateWithRegistry(registry.Global()); graph.HasErrors(diags) {
		t.Fatalf("invalid definition: %v", graph.Errors(diags))
	}

	factory := hydrate.NewLiveNodeFactory(
		hydrate.ProviderMap{"elevenlabs": {APIKey: "mock-key"}},
		func(string, hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		hydrate.WithSpeechSynthesizerFactory(func(string, hydrate.ProviderConfig) (nodes.SpeechSynthesizer, error) {
			return mockIntegrationSynthesizer{}, nil
		}),
	)

	g, err := hydrate.HydrateGraph(&def, hydrate.ProviderMap{"elevenlabs": {APIKey: "mock-key"}}, factory)
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "Hydrated synthesis")
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	if len(result.GetArtifactsByType("audio")) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(result.GetArtifactsByType("audio")))
	}
}

func TestSpeechSynthesizeNode_MockedStreamedRun(t *testing.T) {
	node := nodes.NewSpeechSynthesizeNode("tts", streamingMockSynthesizer{}, nodes.SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
		Timeout:  10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-stream-mock-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "Streamed synthesis")
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if string(artifacts[0].Bytes) != "chunk-achunk-b" {
		t.Errorf("artifact bytes = %q", artifacts[0].Bytes)
	}
	if artifacts[0].Meta["streamed"] != true {
		t.Errorf("artifact meta = %+v", artifacts[0].Meta)
	}
}

func TestSpeechSynthesizeNode_MockedFailurePropagates(t *testing.T) {
	node := nodes.NewSpeechSynthesizeNode("tts", failingIntegrationSynthesizer{}, nodes.SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Timeout:  10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-failure-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "Hello")
	rt := runtime.NewRuntime()
	_, err = rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected run to fail when synthesis fails")
	}
	if !strings.Contains(err.Error(), "synthesis unavailable") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_MockedCancellationStopsRun(t *testing.T) {
	node := nodes.NewSpeechSynthesizeNode("tts", blockingIntegrationSynthesizer{}, nodes.SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Timeout:  10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-cancel-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "Hello")
	rt := runtime.NewRuntime()
	_, err = rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected run to fail on cancellation")
	}
}

// Registry conformance: the built-in type exposes typed outputs with the
// artifact reference (never raw audio in vars).
func TestSpeechSynthesizeNode_RegistryPorts(t *testing.T) {
	def, ok := registry.Global().Get("speech_synthesize")
	if !ok {
		t.Fatal("speech_synthesize not registered")
	}
	if len(def.Ports.Inputs) != 1 || def.Ports.Inputs[0].Name != "input" {
		t.Errorf("inputs = %+v", def.Ports.Inputs)
	}
	var names []string
	for _, out := range def.Ports.Outputs {
		names = append(names, out.Name)
	}
	if strings.Join(names, ",") != "audio_id,audio_artifact,stats" {
		t.Errorf("outputs = %v", names)
	}
}

// The Iris synthesis contract shape (compile-time parity with iriscore).
var _ iriscore.SpeechSynthesizer = mockIntegrationSynthesizer{}

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

// mockIntegrationRecognizer is a deterministic fake speech recognizer used by
// the mocked speech_transcribe integration tests. It never touches the network.
type mockIntegrationRecognizer struct{}

func (mockIntegrationRecognizer) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{
		ID:       "mock-transcript-1",
		Model:    "scribe_v1",
		Text:     "PetalFlow transcribed this line deterministically.",
		Language: "en",
		Duration: 2 * time.Second,
		IsFinal:  true,
	}, nil
}

// failingIntegrationRecognizer always errors, mirroring a provider outage.
type failingIntegrationRecognizer struct{}

func (failingIntegrationRecognizer) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return nil, errMockTranscriptionUnavailable
}

var errMockTranscriptionUnavailable = errorString("transcription unavailable")

type errorString string

func (e errorString) Error() string { return string(e) }

// blockingIntegrationRecognizer blocks until the context is cancelled, then
// returns the context error, mirroring an in-flight provider call.
type blockingIntegrationRecognizer struct{}

func (blockingIntegrationRecognizer) Transcribe(ctx context.Context, _ *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSpeechTranscribeNode_MockedRuntimeRun(t *testing.T) {
	recognizer := mockIntegrationRecognizer{}
	node := nodes.NewSpeechTranscribeNode("transcribe", recognizer, nodes.SpeechTranscribeNodeConfig{
		Model:       "scribe_v1",
		InputVar:    "audio",
		AudioFormat: "mp3",
		Language:    "en",
		OutputVar:   "transcript",
		Timeout:     10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("speech-mock-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", []byte("deterministic-fake-audio"))

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	// Committed transcript output as text.
	text, ok := result.GetVar("transcript")
	if !ok {
		t.Fatal("expected 'transcript' var in envelope")
	}
	if transcript, ok := text.(string); !ok || transcript == "" {
		t.Fatalf("expected non-empty string transcript, got %T=%v", text, text)
	}

	// Committed transcript output as artifact.
	artifacts := result.GetArtifactsByType("transcript")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 transcript artifact, got %d", len(artifacts))
	}
	if artifacts[0].ID != "mock-transcript-1" {
		t.Errorf("artifact ID = %q, want provider response ID", artifacts[0].ID)
	}
	if !strings.Contains(artifacts[0].Text, "deterministically") {
		t.Errorf("artifact text = %q", artifacts[0].Text)
	}
	if artifacts[0].Meta["is_final"] != true {
		t.Errorf("artifact meta is_final = %v, want true", artifacts[0].Meta["is_final"])
	}
}

func TestSpeechTranscribeNode_MockedHydratedGraph(t *testing.T) {
	// End-to-end: load a graph IR definition containing a speech_transcribe
	// node, validate it against the registry, hydrate it with a deterministic
	// recognizer factory, and execute it.
	definition := `{
		"id": "speech-hydrated",
		"nodes": [
			{
				"id": "transcribe",
				"type": "speech_transcribe",
				"config": {
					"provider": "elevenlabs",
					"audio_format": "mp3",
					"input_var": "audio",
					"output_var": "transcript"
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
		hydrate.WithSpeechRecognizerFactory(func(string, hydrate.ProviderConfig) (nodes.SpeechRecognizer, error) {
			return mockIntegrationRecognizer{}, nil
		}),
	)

	g, err := hydrate.HydrateGraph(&def, hydrate.ProviderMap{"elevenlabs": {APIKey: "mock-key"}}, factory)
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", []byte("hydrated-fake-audio"))
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	if _, ok := result.GetVar("transcript"); !ok {
		t.Fatal("expected 'transcript' var in envelope")
	}
	if len(result.GetArtifactsByType("transcript")) != 1 {
		t.Fatalf("expected 1 transcript artifact, got %d", len(result.GetArtifactsByType("transcript")))
	}
}

func TestSpeechTranscribeNode_MockedFailurePropagates(t *testing.T) {
	node := nodes.NewSpeechTranscribeNode("transcribe", failingIntegrationRecognizer{}, nodes.SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
		Timeout:     10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("speech-failure-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", []byte("fake-audio"))
	rt := runtime.NewRuntime()
	_, err = rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected run to fail when transcription fails")
	}
	if !strings.Contains(err.Error(), "transcription unavailable") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_MockedCancellationStopsRun(t *testing.T) {
	node := nodes.NewSpeechTranscribeNode("transcribe", blockingIntegrationRecognizer{}, nodes.SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
		Timeout:     10 * time.Second,
	})

	g, err := graph.NewGraphBuilder("speech-cancel-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", []byte("fake-audio"))
	rt := runtime.NewRuntime()
	_, err = rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected run to fail on cancellation")
	}
}

// Registry conformance: the built-in type exposes typed audio input and the
// transcript/artifact/stats output ports.
func TestSpeechTranscribeNode_RegistryPorts(t *testing.T) {
	def, ok := registry.Global().Get("speech_transcribe")
	if !ok {
		t.Fatal("speech_transcribe not registered")
	}
	if len(def.Ports.Inputs) != 1 || def.Ports.Inputs[0].Name != "audio" {
		t.Errorf("inputs = %+v", def.Ports.Inputs)
	}
	var names []string
	for _, out := range def.Ports.Outputs {
		names = append(names, out.Name)
	}
	if strings.Join(names, ",") != "transcript,transcript_artifact,stats" {
		t.Errorf("outputs = %v", names)
	}
}

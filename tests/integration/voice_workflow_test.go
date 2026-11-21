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

	iriscore "github.com/petal-labs/iris/v2/core"
	irisproviders "github.com/petal-labs/iris/v2/providers"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

// e2eVoiceDemoProvider mirrors the simulated provider shipped with
// examples/09_voice_workflow: deterministic in-process chat + speech +
// voice-session capabilities, no network.
type e2eVoiceDemoProvider struct{}

func (e2eVoiceDemoProvider) ID() string { return "voice-demo" }

func (e2eVoiceDemoProvider) Models() []iriscore.ModelInfo {
	return []iriscore.ModelInfo{{
		ID: "voice-demo-model",
		Capabilities: []iriscore.Feature{
			iriscore.FeatureChat,
			iriscore.FeatureSpeechRecognition,
			iriscore.FeatureSpeechSynthesis,
			iriscore.FeatureVoiceSession,
		},
	}}
}

func (e2eVoiceDemoProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat ||
		f == iriscore.FeatureSpeechRecognition ||
		f == iriscore.FeatureSpeechSynthesis ||
		f == iriscore.FeatureVoiceSession
}

func (e2eVoiceDemoProvider) Chat(_ context.Context, req *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return &iriscore.ChatResponse{
		ID:     "chat-e2e-1",
		Model:  req.Model,
		Output: "PetalFlow heard you: voice workflows run end to end.",
	}, nil
}

func (e2eVoiceDemoProvider) StreamChat(_ context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	ch := make(chan iriscore.ChatChunk, 1)
	ch <- iriscore.ChatChunk{Delta: "PetalFlow heard you: voice workflows run end to end."}
	close(ch)
	errCh := make(chan error)
	close(errCh)
	finalCh := make(chan *iriscore.ChatResponse, 1)
	finalCh <- &iriscore.ChatResponse{ID: "chat-e2e-stream-1", Model: req.Model}
	close(finalCh)
	return &iriscore.ChatStream{Ch: ch, Err: errCh, Final: finalCh}, nil
}

func (e2eVoiceDemoProvider) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{
		ID:       "transcript-e2e-1",
		Model:    "voice-demo-model",
		Text:     "How do voice workflows work in PetalFlow?",
		Language: "en",
		Duration: 2 * time.Second,
		IsFinal:  true,
	}, nil
}

func (e2eVoiceDemoProvider) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{
		ID:      "audio-e2e-1",
		Model:   "voice-demo-model",
		VoiceID: "demo-voice",
		Audio: iriscore.AudioData{
			Data:   []byte("simulated-audio-output"),
			Format: iriscore.AudioFormatMP3_44100_128,
		},
		Usage: iriscore.SpeechUsage{OutputBytes: int64(len("simulated-audio-output"))},
	}, nil
}

func (e2eVoiceDemoProvider) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return nil, iriscore.ErrNotSupported
}

func (e2eVoiceDemoProvider) StartVoiceSession(context.Context, iriscore.VoiceSessionConfig) (iriscore.VoiceSession, error) {
	return nil, iriscore.ErrNotSupported
}

func init() {
	irisproviders.Register("voice-demo", func(string) iriscore.Provider { return e2eVoiceDemoProvider{} })
}

// loadVoiceExampleGraph reads the graph definition shipped with
// examples/09_voice_workflow and validates it against the registry.
func loadVoiceExampleGraph(t *testing.T) *graph.GraphDefinition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "09_voice_workflow", "voice_workflow.graph.json"))
	if err != nil {
		t.Fatalf("reading example graph definition: %v", err)
	}
	var def graph.GraphDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		t.Fatalf("parsing definition: %v", err)
	}
	if diags := def.ValidateWithRegistry(registry.Global()); graph.HasErrors(diags) {
		t.Fatalf("invalid definition: %v", graph.Errors(diags))
	}
	return &def
}

// TestVoiceWorkflowExampleEndToEnd runs the example's DAG (audio ->
// transcript -> agent -> speech) through the default live-node factory: the
// llmprovider-registered default speech factories hydrate the speech nodes,
// and the runtime executes the full pipeline deterministically.
func TestVoiceWorkflowExampleEndToEnd(t *testing.T) {
	def := loadVoiceExampleGraph(t)

	providers := hydrate.ProviderMap{"voice-demo": {
		APIKey: "ek-mock-key",
		Capabilities: map[string][]string{
			"voice-demo-model": {"chat", "streaming"},
		},
	}}
	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})

	g, err := hydrate.HydrateGraph(def, providers, factory)
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", []byte("simulated-user-audio-payload"))
	result, err := runtime.NewRuntime().Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	if transcript := result.GetVarString("transcript"); transcript != "How do voice workflows work in PetalFlow?" {
		t.Errorf("transcript = %q", transcript)
	}
	if reply := result.GetVarString("reply"); reply != "PetalFlow heard you: voice workflows run end to end." {
		t.Errorf("reply = %q", reply)
	}
	if audioID, ok := result.GetVar("audio_id"); !ok || audioID == "" {
		t.Errorf("audio_id = %v", audioID)
	}
	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if string(artifacts[0].Bytes) != "simulated-audio-output" {
		t.Errorf("audio artifact bytes = %q", artifacts[0].Bytes)
	}
	// Raw audio never lands in committed envelope vars (the "audio" input
	// var belongs to the caller; committed output vars hold text/IDs).
	for name, value := range result.Vars {
		if name == "audio" {
			continue
		}
		if _, isBytes := value.([]byte); isBytes {
			t.Errorf("var %q carries raw bytes into the envelope", name)
		}
	}
}

// TestVoiceWorkflowExampleCancellation documents the cancellation behavior:
// an expired context fails the run and commits no downstream outputs.
func TestVoiceWorkflowExampleCancellation(t *testing.T) {
	def := loadVoiceExampleGraph(t)

	providers := hydrate.ProviderMap{"voice-demo": {
		APIKey: "ek-mock-key",
		Capabilities: map[string][]string{
			"voice-demo-model": {"chat", "streaming"},
		},
	}}
	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})
	g, err := hydrate.HydrateGraph(def, providers, factory)
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	result, err := runtime.NewRuntime().Run(ctx, g, core.NewEnvelope().WithVar("audio", []byte("audio")), runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected the cancelled run to fail")
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error = %v, want a cancellation error", err)
	}
	if result != nil {
		if transcript := result.GetVarString("transcript"); transcript != "" {
			t.Errorf("cancelled run committed transcript %q", transcript)
		}
	}
}

package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	iriscore "github.com/petal-labs/iris/v2/core"
	irisproviders "github.com/petal-labs/iris/v2/providers"
	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/registry"
)

// cliSpeechFakeProvider implements both speech capabilities for CLI hydration
// tests; construction requires no network.
type cliSpeechFakeProvider struct{}

func (cliSpeechFakeProvider) ID() string                   { return "elevenlabs-mock" }
func (cliSpeechFakeProvider) Models() []iriscore.ModelInfo { return nil }
func (cliSpeechFakeProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat ||
		f == iriscore.FeatureSpeechRecognition ||
		f == iriscore.FeatureSpeechSynthesis
}
func (cliSpeechFakeProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (cliSpeechFakeProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}
func (cliSpeechFakeProvider) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{Text: "cli transcript", IsFinal: true}, nil
}
func (cliSpeechFakeProvider) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{Audio: iriscore.AudioData{Data: []byte("cli audio")}}, nil
}
func (cliSpeechFakeProvider) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return nil, nil
}

// cliChatOnlyFakeProvider implements only chat, mirroring a provider without
// speech capabilities.
type cliChatOnlyFakeProvider struct{}

func (cliChatOnlyFakeProvider) ID() string                   { return "chat-only-mock" }
func (cliChatOnlyFakeProvider) Models() []iriscore.ModelInfo { return nil }
func (cliChatOnlyFakeProvider) Supports(iriscore.Feature) bool {
	return true
}
func (cliChatOnlyFakeProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (cliChatOnlyFakeProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

func init() {
	irisproviders.Register("elevenlabs-mock", func(string) iriscore.Provider { return cliSpeechFakeProvider{} })
	irisproviders.Register("chat-only-mock", func(string) iriscore.Provider { return cliChatOnlyFakeProvider{} })
}

func speechTestDefinition(t *testing.T, definition string) *graph.GraphDefinition {
	t.Helper()
	var def graph.GraphDefinition
	if err := json.Unmarshal([]byte(definition), &def); err != nil {
		t.Fatalf("parsing definition: %v", err)
	}
	if diags := def.ValidateWithRegistry(registry.Global()); graph.HasErrors(diags) {
		t.Fatalf("invalid definition: %v", graph.Errors(diags))
	}
	return &def
}

// TestHydrateRunGraph_SpeechNodesDefaultFactories verifies the CLI hydration
// path resolves speech nodes through the llmprovider-registered default
// factories and the standard provider credential resolution.
func TestHydrateRunGraph_SpeechNodesDefaultFactories(t *testing.T) {
	definition := `{
		"id": "cli-speech",
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
		]
	}`
	def := speechTestDefinition(t, definition)

	providers := hydrate.ProviderMap{"elevenlabs-mock": {APIKey: "ek-mock-key"}}
	execGraph, err := hydrateRunGraph(&cobra.Command{Use: "test"}, def, providers, nil)
	if err != nil {
		t.Fatalf("hydrating run graph: %v", err)
	}

	// Both speech nodes must be live executable nodes, not placeholders.
	speechNodes := 0
	for _, node := range execGraph.Nodes() {
		switch node.Kind() {
		case core.NodeKindSpeechTranscribe, core.NodeKindSpeechSynthesize:
			speechNodes++
			if _, ok := node.(*nodes.SpeechTranscribeNode); !ok {
				if _, ok := node.(*nodes.SpeechSynthesizeNode); !ok {
					t.Fatalf("speech node %q hydrated as %T placeholder", node.ID(), node)
				}
			}
		}
	}
	if speechNodes != 2 {
		t.Fatalf("expected 2 speech nodes, got %d", speechNodes)
	}
}

// TestHydrateRunGraph_SpeechUnsupportedCapability verifies the CLI surfaces a
// key-free capability error when the node's provider cannot do speech.
func TestHydrateRunGraph_SpeechUnsupportedCapability(t *testing.T) {
	const apiKey = "ek-mock-secret-do-not-log"
	definition := `{
		"id": "cli-speech-unsupported",
		"nodes": [
			{
				"id": "tts",
				"type": "speech_synthesize",
				"config": {"provider": "chat-only-mock", "voice_id": "voice-1", "input_var": "text"}
			}
		]
	}`
	def := speechTestDefinition(t, definition)

	providers := hydrate.ProviderMap{"chat-only-mock": {APIKey: apiKey}}
	_, err := hydrateRunGraph(&cobra.Command{Use: "test"}, def, providers, nil)
	if err == nil {
		t.Fatal("expected hydration to fail for provider without speech capabilities")
	}
	if !strings.Contains(err.Error(), "does not support speech synthesis") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks API key: %q", err.Error())
	}
}

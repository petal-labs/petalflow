package hydrate

import (
	"context"
	"strings"
	"testing"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
)

// stubSpeechRecognizer implements nodes.SpeechRecognizer for default-factory
// tests.
type stubSpeechRecognizer struct{}

func (stubSpeechRecognizer) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return nil, nil
}

// stubSpeechSynthesizer implements nodes.SpeechSynthesizer for default-factory
// tests.
type stubSpeechSynthesizer struct{}

func (stubSpeechSynthesizer) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return nil, nil
}

func (stubSpeechSynthesizer) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return nil, nil
}

// clearDefaultSpeechFactories resets the registered defaults (test-only).
func clearDefaultSpeechFactories() {
	defaultSpeechFactoriesMu.Lock()
	defer defaultSpeechFactoriesMu.Unlock()
	defaultSpeechRecognizerFactory = nil
	defaultSpeechSynthesizerFactory = nil
}

func TestRegisterDefaultSpeechFactories(t *testing.T) {
	recognizerCalls, synthesizerCalls := 0, 0
	RegisterDefaultSpeechFactories(
		func(string, ProviderConfig) (nodes.SpeechRecognizer, error) {
			recognizerCalls++
			return stubSpeechRecognizer{}, nil
		},
		func(string, ProviderConfig) (nodes.SpeechSynthesizer, error) {
			synthesizerCalls++
			return stubSpeechSynthesizer{}, nil
		},
	)
	defer clearDefaultSpeechFactories()

	gotRecognizer, gotSynthesizer := DefaultSpeechFactories()
	if gotRecognizer == nil || gotSynthesizer == nil {
		t.Fatalf("registered defaults not visible: recognizer=%v synthesizer=%v", gotRecognizer, gotSynthesizer)
	}

	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(
		ProviderMap{"elevenlabs": {APIKey: "ek-test"}},
		factory,
	)

	// speech_transcribe hydrates through the default recognizer factory.
	transcribe, err := nodeFactory(graph.NodeDef{
		ID:   "stt-1",
		Type: "speech_transcribe",
		Config: map[string]any{
			"provider":     "elevenlabs",
			"audio_format": "mp3",
		},
	})
	if err != nil {
		t.Fatalf("speech_transcribe hydration: %v", err)
	}
	if transcribe.Kind() != core.NodeKindSpeechTranscribe {
		t.Errorf("kind = %q", transcribe.Kind())
	}
	if recognizerCalls != 1 {
		t.Errorf("recognizer calls = %d, want 1", recognizerCalls)
	}

	// speech_synthesize hydrates through the default synthesizer factory.
	synthesize, err := nodeFactory(graph.NodeDef{
		ID:   "tts-1",
		Type: "speech_synthesize",
		Config: map[string]any{
			"provider": "elevenlabs",
			"voice_id": "voice-1",
		},
	})
	if err != nil {
		t.Fatalf("speech_synthesize hydration: %v", err)
	}
	if synthesize.Kind() != core.NodeKindSpeechSynthesize {
		t.Errorf("kind = %q", synthesize.Kind())
	}
	if synthesizerCalls != 1 {
		t.Errorf("synthesizer calls = %d, want 1", synthesizerCalls)
	}
}

func TestRegisterDefaultSpeechFactories_NilKeepsPrevious(t *testing.T) {
	RegisterDefaultSpeechFactories(
		func(string, ProviderConfig) (nodes.SpeechRecognizer, error) { return stubSpeechRecognizer{}, nil },
		func(string, ProviderConfig) (nodes.SpeechSynthesizer, error) { return stubSpeechSynthesizer{}, nil },
	)
	defer clearDefaultSpeechFactories()

	// Passing nil for the recognizer keeps the previously registered value.
	RegisterDefaultSpeechFactories(nil, func(string, ProviderConfig) (nodes.SpeechSynthesizer, error) {
		return stubSpeechSynthesizer{}, nil
	})
	gotRecognizer, gotSynthesizer := DefaultSpeechFactories()
	if gotRecognizer == nil || gotSynthesizer == nil {
		t.Fatal("nil registration must not clear previously registered factories")
	}
}

func TestRegisterDefaultSpeechFactories_ClearRestore(t *testing.T) {
	RegisterDefaultSpeechFactories(
		func(string, ProviderConfig) (nodes.SpeechRecognizer, error) { return stubSpeechRecognizer{}, nil },
		func(string, ProviderConfig) (nodes.SpeechSynthesizer, error) { return stubSpeechSynthesizer{}, nil },
	)
	clearDefaultSpeechFactories()

	recognizer, synthesizer := DefaultSpeechFactories()
	if recognizer != nil || synthesizer != nil {
		t.Fatal("clearDefaultSpeechFactories must clear both factories")
	}
}

func TestNewLiveNodeFactory_SpeechNodesWithoutDefaultsFail(t *testing.T) {
	// Defaults must be absent in this package's tests: llmprovider (which
	// registers them) is not linked here. Guard against future accidental
	// registration so the failure-path assertion stays meaningful.
	if recognizer, synthesizer := DefaultSpeechFactories(); recognizer != nil || synthesizer != nil {
		t.Skipf("llmprovider defaults registered in this binary; skipping absence check")
	}

	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{"elevenlabs": {APIKey: "ek-test"}}, factory)

	_, err := nodeFactory(graph.NodeDef{
		ID:     "stt-1",
		Type:   "speech_transcribe",
		Config: map[string]any{"provider": "elevenlabs", "audio_format": "mp3"},
	})
	if err == nil {
		t.Fatal("expected error without registered defaults or explicit factory")
	}
	if !strings.Contains(err.Error(), "register llmprovider defaults or use WithSpeechRecognizerFactory") {
		t.Errorf("error = %v", err)
	}

	_, err = nodeFactory(graph.NodeDef{
		ID:     "tts-1",
		Type:   "speech_synthesize",
		Config: map[string]any{"provider": "elevenlabs", "voice_id": "voice-1"},
	})
	if err == nil {
		t.Fatal("expected error without registered defaults or explicit factory")
	}
	if !strings.Contains(err.Error(), "register llmprovider defaults or use WithSpeechSynthesizerFactory") {
		t.Errorf("error = %v", err)
	}
}

func TestNewLiveNodeFactory_ExplicitFactoryOverridesDefaults(t *testing.T) {
	RegisterDefaultSpeechFactories(
		func(string, ProviderConfig) (nodes.SpeechRecognizer, error) {
			return stubSpeechRecognizer{}, nil
		},
		nil,
	)
	defer clearDefaultSpeechFactories()

	recognizerCalls := 0
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(
		ProviderMap{"elevenlabs": {APIKey: "ek-test"}},
		factory,
		WithSpeechRecognizerFactory(func(string, ProviderConfig) (nodes.SpeechRecognizer, error) {
			recognizerCalls++
			return stubSpeechRecognizer{}, nil
		}),
	)

	if _, err := nodeFactory(graph.NodeDef{
		ID:     "stt-1",
		Type:   "speech_transcribe",
		Config: map[string]any{"provider": "elevenlabs", "audio_format": "mp3"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognizerCalls != 1 {
		t.Errorf("explicit factory calls = %d, want 1 (defaults must not be used)", recognizerCalls)
	}
}

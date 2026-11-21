package llmprovider

import (
	"context"
	"strings"
	"testing"

	iriscore "github.com/petal-labs/iris/v2/core"
	irisproviders "github.com/petal-labs/iris/v2/providers"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
)

// speechProvider is a fake Iris provider that advertises speech recognition.
type speechProvider struct{}

func (speechProvider) ID() string                   { return "speech-fake" }
func (speechProvider) Models() []iriscore.ModelInfo { return nil }
func (speechProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat || f == iriscore.FeatureSpeechRecognition
}
func (speechProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (speechProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}
func (speechProvider) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{Text: "hi", IsFinal: true}, nil
}

// ttsProvider is a fake Iris provider that advertises speech synthesis.
type ttsProvider struct{}

func (ttsProvider) ID() string                   { return "tts-fake" }
func (ttsProvider) Models() []iriscore.ModelInfo { return nil }
func (ttsProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat || f == iriscore.FeatureSpeechSynthesis
}
func (ttsProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (ttsProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}
func (ttsProvider) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{Audio: iriscore.AudioData{Data: []byte("audio")}}, nil
}
func (ttsProvider) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return nil, nil
}

// chatOnlyProvider is a fake Iris provider without speech recognition.
type chatOnlyProvider struct{}

func (chatOnlyProvider) ID() string                     { return "chat-fake" }
func (chatOnlyProvider) Models() []iriscore.ModelInfo   { return nil }
func (chatOnlyProvider) Supports(iriscore.Feature) bool { return true }
func (chatOnlyProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (chatOnlyProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

func init() {
	// Register fake providers in the Iris provider registry so
	// NewSpeechRecognizer / NewSpeechSynthesizer can resolve them by name.
	irisproviders.Register("speech-fake", func(string) iriscore.Provider { return speechProvider{} })
	irisproviders.Register("chat-only", func(string) iriscore.Provider { return chatOnlyProvider{} })
	irisproviders.Register("tts-fake", func(string) iriscore.Provider { return ttsProvider{} })
}

func TestNewSpeechRecognizer_Discovered(t *testing.T) {
	recognizer, err := NewSpeechRecognizer("speech-fake", hydrate.ProviderConfig{APIKey: "sk-speech-fake"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, err := recognizer.Transcribe(context.Background(), &iriscore.TranscriptionRequest{
		Audio: &iriscore.AudioData{
			Data:   []byte("audio"),
			Format: iriscore.AudioFormat{Encoding: iriscore.AudioEncodingMP3},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hi" || !resp.IsFinal {
		t.Errorf("response = %+v", resp)
	}
}

func TestNewSpeechRecognizer_CapabilityMissing(t *testing.T) {
	_, err := NewSpeechRecognizer("chat-only", hydrate.ProviderConfig{APIKey: "sk-chat-only"})
	if err == nil {
		t.Fatal("expected error for provider without speech recognition")
	}
	if !strings.Contains(err.Error(), "does not support speech recognition") {
		t.Errorf("error = %v", err)
	}
}

func TestNewSpeechRecognizer_MissingCredential(t *testing.T) {
	_, err := NewSpeechRecognizer("speech-fake", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	for _, want := range []string{
		"requires an API key for speech",
		"PETALFLOW_PROVIDER_SPEECH-FAKE_API_KEY",
		"providers.speech-fake.api_key",
		"--provider-key speech-fake=<key>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestNewSpeechRecognizer_ErrorDoesNotLeakCredential asserts that
// construction failures never echo the resolved API key.
func TestNewSpeechRecognizer_ErrorDoesNotLeakCredential(t *testing.T) {
	const apiKey = "sk-secret-elevenlabs-key-123456"
	_, err := NewSpeechRecognizer("chat-only", hydrate.ProviderConfig{APIKey: apiKey})
	if err == nil {
		t.Fatal("expected capability error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks API key: %q", err.Error())
	}
}

func TestNewSpeechRecognizer_UnknownProvider(t *testing.T) {
	_, err := NewSpeechRecognizer("does-not-exist", hydrate.ProviderConfig{APIKey: "sk-x"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewSpeechSynthesizer_Discovered(t *testing.T) {
	synthesizer, err := NewSpeechSynthesizer("tts-fake", hydrate.ProviderConfig{APIKey: "sk-tts-fake"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, err := synthesizer.Synthesize(context.Background(), &iriscore.SpeechRequest{
		VoiceID: "voice-1",
		Text:    "hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Audio.Data) != "audio" {
		t.Errorf("response = %+v", resp)
	}
}

func TestNewSpeechSynthesizer_CapabilityMissing(t *testing.T) {
	_, err := NewSpeechSynthesizer("chat-only", hydrate.ProviderConfig{APIKey: "sk-chat-only"})
	if err == nil {
		t.Fatal("expected error for provider without speech synthesis")
	}
	if !strings.Contains(err.Error(), "does not support speech synthesis") {
		t.Errorf("error = %v", err)
	}
}

func TestNewSpeechSynthesizer_MissingCredential(t *testing.T) {
	_, err := NewSpeechSynthesizer("tts-fake", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "requires an API key for speech") {
		t.Errorf("error = %v", err)
	}
}

// TestNewSpeechSynthesizer_ErrorDoesNotLeakCredential asserts that
// construction failures never echo the resolved API key.
func TestNewSpeechSynthesizer_ErrorDoesNotLeakCredential(t *testing.T) {
	const apiKey = "sk-secret-elevenlabs-key-123456"
	_, err := NewSpeechSynthesizer("chat-only", hydrate.ProviderConfig{APIKey: apiKey})
	if err == nil {
		t.Fatal("expected capability error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks API key: %q", err.Error())
	}
}

func TestNewSpeechSynthesizer_UnknownProvider(t *testing.T) {
	_, err := NewSpeechSynthesizer("does-not-exist", hydrate.ProviderConfig{APIKey: "sk-x"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestDefaultSpeechFactoriesRegistered(t *testing.T) {
	// llmprovider's init installs itself as the hydration default so speech
	// nodes resolve credentials through the standard ProviderMap flow.
	recognizerFactory, synthesizerFactory := hydrate.DefaultSpeechFactories()
	if recognizerFactory == nil || synthesizerFactory == nil {
		t.Fatalf("default speech factories not registered: recognizer=%v synthesizer=%v", recognizerFactory, synthesizerFactory)
	}

	recognizer, err := recognizerFactory("speech-fake", hydrate.ProviderConfig{APIKey: "sk-speech-fake"})
	if err != nil {
		t.Fatalf("recognizer factory: %v", err)
	}
	if _, ok := recognizer.(nodes.SpeechRecognizer); !ok {
		t.Fatalf("recognizer factory returned %T", recognizer)
	}

	synthesizer, err := synthesizerFactory("tts-fake", hydrate.ProviderConfig{APIKey: "sk-tts-fake"})
	if err != nil {
		t.Fatalf("synthesizer factory: %v", err)
	}
	if _, ok := synthesizer.(nodes.SpeechSynthesizer); !ok {
		t.Fatalf("synthesizer factory returned %T", synthesizer)
	}
}

// Compile-time contract checks.
var (
	_ iriscore.Provider          = speechProvider{}
	_ nodes.SpeechRecognizer     = speechProvider{}
	_ iriscore.SpeechRecognizer  = speechProvider{}
	_ iriscore.Provider          = ttsProvider{}
	_ nodes.SpeechSynthesizer    = ttsProvider{}
	_ iriscore.SpeechSynthesizer = ttsProvider{}
)

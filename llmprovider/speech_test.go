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
	recognizer, err := NewSpeechRecognizer("speech-fake", hydrate.ProviderConfig{})
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
	// Create via the iris adapter path: chatOnlyProvider must fail discovery.
	_, err := NewSpeechRecognizer("chat-only", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for provider without speech recognition")
	}
	if !strings.Contains(err.Error(), "does not support speech recognition") {
		t.Errorf("error = %v", err)
	}
}

func TestNewSpeechRecognizer_UnknownProvider(t *testing.T) {
	_, err := NewSpeechRecognizer("does-not-exist", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewSpeechSynthesizer_Discovered(t *testing.T) {
	synthesizer, err := NewSpeechSynthesizer("tts-fake", hydrate.ProviderConfig{})
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
	_, err := NewSpeechSynthesizer("chat-only", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for provider without speech synthesis")
	}
	if !strings.Contains(err.Error(), "does not support speech synthesis") {
		t.Errorf("error = %v", err)
	}
}

func TestNewSpeechSynthesizer_UnknownProvider(t *testing.T) {
	_, err := NewSpeechSynthesizer("does-not-exist", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
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

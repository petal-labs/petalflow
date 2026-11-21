//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	elevenlabsprovider "github.com/petal-labs/iris/v2/providers/elevenlabs"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// newElevenLabsSynthesizer returns a nodes.SpeechSynthesizer backed by the
// ElevenLabs provider via Iris capability discovery.
func newElevenLabsSynthesizer(t *testing.T) nodes.SpeechSynthesizer {
	t.Helper()
	provider := elevenlabsprovider.New(os.Getenv("ELEVENLABS_API_KEY"))
	synthesizer, ok := iriscore.AsSpeechSynthesizer(provider)
	if !ok {
		t.Fatal("elevenlabs provider does not implement SpeechSynthesizer")
	}
	return synthesizer
}

func TestSpeechSynthesizeNode_ElevenLabsLive(t *testing.T) {
	skipIfNoElevenLabsKey(t)

	node := nodes.NewSpeechSynthesizeNode("tts", newElevenLabsSynthesizer(t), nodes.SpeechSynthesizeNodeConfig{
		Model:       "eleven_flash_v2_5",
		VoiceID:     os.Getenv("ELEVENLABS_VOICE_ID"),
		InputVar:    "text",
		AudioFormat: "mp3",
		SampleRate:  22050,
		BitRate:     32,
		OutputVar:   "audio_id",
		Timeout:     60 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-live-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "PetalFlow live speech synthesis check.")
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if len(artifacts[0].Bytes) == 0 {
		t.Fatal("expected non-empty audio bytes")
	}
	t.Logf("live audio: %d bytes (%s)", len(artifacts[0].Bytes), artifacts[0].MimeType)
}

func TestSpeechSynthesizeNode_ElevenLabsLiveStream(t *testing.T) {
	skipIfNoElevenLabsKey(t)

	node := nodes.NewSpeechSynthesizeNode("tts", newElevenLabsSynthesizer(t), nodes.SpeechSynthesizeNodeConfig{
		Model:       "eleven_flash_v2_5",
		VoiceID:     os.Getenv("ELEVENLABS_VOICE_ID"),
		InputVar:    "text",
		AudioFormat: "mp3",
		SampleRate:  22050,
		BitRate:     32,
		Stream:      true,
		OutputVar:   "audio_id",
		Timeout:     60 * time.Second,
	})

	g, err := graph.NewGraphBuilder("tts-live-stream-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("text", "PetalFlow live streaming speech synthesis check.")
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if len(artifacts[0].Bytes) == 0 {
		t.Fatal("expected non-empty streamed audio bytes")
	}
	if artifacts[0].Meta["streamed"] != true {
		t.Fatalf("expected streamed=true, meta = %+v", artifacts[0].Meta)
	}
	t.Logf("live streamed audio: %d bytes (%s)", len(artifacts[0].Bytes), artifacts[0].MimeType)
}

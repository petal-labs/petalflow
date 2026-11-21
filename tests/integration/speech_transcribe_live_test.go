//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"math"
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

// skipIfNoElevenLabsKey skips or fatals if ELEVENLABS_API_KEY is not set.
func skipIfNoElevenLabsKey(t *testing.T) {
	t.Helper()
	if os.Getenv("ELEVENLABS_API_KEY") == "" {
		skipOrFailOnMissingKey(t, "ELEVENLABS_API_KEY")
	}
}

// newElevenLabsRecognizer returns a nodes.SpeechRecognizer backed by the
// ElevenLabs provider via Iris capability discovery.
func newElevenLabsRecognizer(t *testing.T) nodes.SpeechRecognizer {
	t.Helper()
	provider := elevenlabsprovider.New(os.Getenv("ELEVENLABS_API_KEY"))
	recognizer, ok := iriscore.AsSpeechRecognizer(provider)
	if !ok {
		t.Fatal("elevenlabs provider does not implement SpeechRecognizer")
	}
	return recognizer
}

// oneSecondSilentWAV returns a deterministic 16-bit mono 16 kHz WAV file of
// gentle noise, so the live test depends only on the transcription API.
func oneSecondSilentWAV() []byte {
	const sampleRate = 16000
	samples := make([]int16, sampleRate)
	for i := range samples {
		amplitude := int16(math.Sin(float64(i)*0.01) * 64)
		samples[i] = amplitude
	}

	data := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(s))
	}

	header := []byte("RIFF")
	size := uint32(36 + len(data))
	header = binary.LittleEndian.AppendUint32(header, size)
	header = append(header, "WAVEfmt "...)
	header = binary.LittleEndian.AppendUint32(header, 16)
	header = binary.LittleEndian.AppendUint16(header, 1) // PCM
	header = binary.LittleEndian.AppendUint16(header, 1) // mono
	header = binary.LittleEndian.AppendUint32(header, sampleRate)
	header = binary.LittleEndian.AppendUint32(header, sampleRate*2)
	header = binary.LittleEndian.AppendUint16(header, 2)
	header = binary.LittleEndian.AppendUint16(header, 16)
	header = append(header, "data"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(len(data)))
	return append(header, data...)
}

func TestSpeechTranscribeNode_ElevenLabsLive(t *testing.T) {
	skipIfNoElevenLabsKey(t)

	node := nodes.NewSpeechTranscribeNode("transcribe", newElevenLabsRecognizer(t), nodes.SpeechTranscribeNodeConfig{
		Model:       "scribe_v1",
		InputVar:    "audio",
		AudioFormat: "wav",
		Filename:    "one-second.wav",
		Language:    "en",
		OutputVar:   "transcript",
		Timeout:     60 * time.Second,
	})

	g, err := graph.NewGraphBuilder("speech-live-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", oneSecondSilentWAV())
	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	artifacts := result.GetArtifactsByType("transcript")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 transcript artifact, got %d", len(artifacts))
	}
	if artifacts[0].Meta["is_final"] != true {
		t.Fatalf("expected committed transcript (is_final=true), meta = %+v", artifacts[0].Meta)
	}
	t.Logf("live transcript: %q (language=%v)", artifacts[0].Text, artifacts[0].Meta["language"])
}

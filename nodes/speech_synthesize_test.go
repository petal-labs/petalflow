package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// mockSpeechSynthesizer captures requests and returns canned responses.
type mockSpeechSynthesizer struct {
	response *iriscore.SpeechResponse
	stream   *iriscore.SpeechStream
	err      error
	requests []*iriscore.SpeechRequest
}

func (m *mockSpeechSynthesizer) Synthesize(ctx context.Context, req *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockSpeechSynthesizer) StreamSynthesize(ctx context.Context, req *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	if m.stream != nil {
		return m.stream, nil
	}
	return bufferedSpeechStream(m.response), nil
}

// bufferedSpeechStream turns a complete response into a finished SpeechStream
// whose channels are already closed.
func bufferedSpeechStream(resp *iriscore.SpeechResponse) *iriscore.SpeechStream {
	var chunks []iriscore.AudioChunk
	if resp != nil && len(resp.Audio.Data) > 0 {
		chunks = []iriscore.AudioChunk{{
			Data:   resp.Audio.Data,
			Format: resp.Audio.Format,
		}}
	}
	return finishedSpeechStream(chunks, resp, nil)
}

// finishedSpeechStream builds a SpeechStream with all channels resolved.
func finishedSpeechStream(chunks []iriscore.AudioChunk, final *iriscore.SpeechResponse, streamErr error) *iriscore.SpeechStream {
	ch := make(chan iriscore.AudioChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	errCh := make(chan error, 1)
	if streamErr != nil {
		errCh <- streamErr
	}
	close(errCh)
	finalCh := make(chan *iriscore.SpeechResponse, 1)
	if final != nil {
		finalCh <- final
	}
	close(finalCh)
	return &iriscore.SpeechStream{Ch: ch, Err: errCh, Final: finalCh}
}

// blockingSpeechSynthesizer blocks forever until its context is cancelled,
// simulating an in-flight provider call that never completes.
type blockingSpeechSynthesizer struct{}

func (blockingSpeechSynthesizer) Synthesize(ctx context.Context, _ *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingSpeechSynthesizer) StreamSynthesize(ctx context.Context, _ *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func validSpeechResponse() *iriscore.SpeechResponse {
	return &iriscore.SpeechResponse{
		ID:      "audio-1",
		Model:   "eleven_flash_v2_5",
		VoiceID: "voice-1",
		Audio: iriscore.AudioData{
			Data:   []byte("fake-audio-bytes"),
			Format: iriscore.AudioFormatMP3_44100_128,
		},
		Usage: iriscore.SpeechUsage{
			InputCharacters: len("Hello"),
			AudioDuration:   2 * time.Second,
			OutputBytes:     int64(len("fake-audio-bytes")),
		},
	}
}

func TestNewSpeechSynthesizeNode(t *testing.T) {
	node := NewSpeechSynthesizeNode("test-tts", &mockSpeechSynthesizer{}, SpeechSynthesizeNodeConfig{})
	if node.ID() != "test-tts" {
		t.Errorf("expected ID 'test-tts', got %q", node.ID())
	}
	if node.Kind() != core.NodeKindSpeechSynthesize {
		t.Errorf("expected kind %q, got %q", core.NodeKindSpeechSynthesize, node.Kind())
	}
}

func TestNewSpeechSynthesizeNode_Defaults(t *testing.T) {
	node := NewSpeechSynthesizeNode("my-node", &mockSpeechSynthesizer{}, SpeechSynthesizeNodeConfig{})
	config := node.Config()
	if config.OutputVar != "my-node_audio" {
		t.Errorf("expected default output var 'my-node_audio', got %q", config.OutputVar)
	}
	if config.Timeout != 120*time.Second {
		t.Errorf("expected default timeout 120s, got %v", config.Timeout)
	}
	if config.MaxBytes != defaultSpeechMaxAudioBytes {
		t.Errorf("expected default max_bytes %d, got %d", defaultSpeechMaxAudioBytes, config.MaxBytes)
	}
}

func TestSpeechSynthesizeNode_Run_Sync(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		Model:       "eleven_flash_v2_5",
		VoiceID:     "voice-1",
		InputVar:    "text",
		Language:    "en",
		OutputVar:   "audio_id",
		AudioFormat: "mp3",
		SampleRate:  44100,
		BitRate:     128,
	})

	env := core.NewEnvelope()
	env.Trace.RunID = "run-1"
	env.SetVar("text", "Hello")

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(synthesizer.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(synthesizer.requests))
	}
	req := synthesizer.requests[0]
	if req.VoiceID != "voice-1" || req.Text != "Hello" || req.Language != "en" {
		t.Errorf("request = %+v", req)
	}
	if req.Format.Encoding != iriscore.AudioEncodingMP3 || req.Format.SampleRate != 44100 || req.Format.BitRate != 128 {
		t.Errorf("format = %+v", req.Format)
	}
	if req.Metadata["node_id"] != "test" || req.Metadata["run_id"] != "run-1" {
		t.Errorf("metadata = %v", req.Metadata)
	}

	// The output var holds the artifact ID, never audio bytes.
	audioID, ok := result.GetVar("audio_id")
	if !ok || audioID != "audio-1" {
		t.Errorf("audio_id = %v, want provider response ID", audioID)
	}

	// Bounded audio artifact committed on the envelope.
	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	artifact := artifacts[0]
	if artifact.ID != "audio-1" || string(artifact.Bytes) != "fake-audio-bytes" {
		t.Errorf("artifact = %+v", artifact)
	}
	if artifact.MimeType != "audio/mpeg" {
		t.Errorf("artifact mime = %q, want audio/mpeg", artifact.MimeType)
	}
	if artifact.Meta["voice_id"] != "voice-1" || artifact.Meta["streamed"] != false {
		t.Errorf("artifact meta = %+v", artifact.Meta)
	}

	statsRaw, ok := result.GetVar("audio_id_stats")
	if !ok {
		t.Fatal("expected audio_id_stats to be stored")
	}
	stats := statsRaw.(SpeechSynthesizeStats)
	if stats.AudioBytes != len("fake-audio-bytes") || stats.Streamed {
		t.Errorf("stats = %+v", stats)
	}
}

func TestSpeechSynthesizeNode_Run_EnvelopeInputFallback(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID: "voice-1",
	})

	env := core.NewEnvelope()
	env.Input = "Envelope text"

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if synthesizer.requests[0].Text != "Envelope text" {
		t.Errorf("text = %q", synthesizer.requests[0].Text)
	}
}

func TestSpeechSynthesizeNode_Run_BytesInput(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", []byte("bytes text"))

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if synthesizer.requests[0].Text != "bytes text" {
		t.Errorf("text = %q", synthesizer.requests[0].Text)
	}
}

func TestSpeechSynthesizeNode_Run_MissingInputVar(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for missing input var")
	}
	if !strings.Contains(err.Error(), `input variable "text" not found`) {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_UnsupportedInputType(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", 42)
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unsupported input type")
	}
	if !strings.Contains(err.Error(), "unsupported input text type int") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_EmptyTextRejected(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "   ")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for empty text")
	}
	if len(synthesizer.requests) != 0 {
		t.Error("synthesizer must not be called on empty text")
	}
}

func TestSpeechSynthesizeNode_Run_MissingVoiceID(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for missing voice_id")
	}
	if !strings.Contains(err.Error(), "invalid speech request") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_NoSynthesizer(t *testing.T) {
	node := NewSpeechSynthesizeNode("test", nil, SpeechSynthesizeNodeConfig{})
	_, err := node.Run(context.Background(), core.NewEnvelope().WithVar("t", "x"))
	if err == nil {
		t.Fatal("expected error for missing synthesizer")
	}
	if !strings.Contains(err.Error(), "no synthesizer configured") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_SynthesizerError(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{
		response: validSpeechResponse(),
		err:      errors.New("provider unavailable"),
	}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	result, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected synthesizer error to propagate")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("error = %v", err)
	}
	if result != nil {
		if _, ok := result.GetVar("test_audio"); ok {
			t.Error("output var must not be committed on failure")
		}
		if len(result.GetArtifactsByType("audio")) != 0 {
			t.Error("artifact must not be committed on failure")
		}
	}
}

func TestSpeechSynthesizeNode_Run_ContextCancellation(t *testing.T) {
	node := NewSpeechSynthesizeNode("test", blockingSpeechSynthesizer{}, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Timeout:  50 * time.Millisecond,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline error, got %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_SynthesizerErrorWhileCancelled(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{err: errors.New("network blip")}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context cancellation, got %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_EmptyAudioRejected(t *testing.T) {
	resp := validSpeechResponse()
	resp.Audio.Data = nil
	synthesizer := &mockSpeechSynthesizer{response: resp}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected empty audio to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid audio response") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_AudioExceedsMaxBytes(t *testing.T) {
	resp := validSpeechResponse()
	synthesizer := &mockSpeechSynthesizer{response: resp}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		MaxBytes: 8,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected oversized audio to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds max_bytes") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_NilResponseRejected(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected nil response to be rejected")
	}
}

func TestSpeechSynthesizeNode_Run_OmitArtifacts(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{response: validSpeechResponse()}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:       "voice-1",
		InputVar:      "text",
		OmitArtifacts: true,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GetArtifactsByType("audio")) != 0 {
		t.Error("artifact must be omitted when configured")
	}
	audioID, ok := result.GetVar("test_audio")
	if !ok || audioID != "audio-1" {
		t.Errorf("audio var = %v, ok = %v", audioID, ok)
	}
}

func TestSpeechSynthesizeNode_Run_Stream(t *testing.T) {
	chunks := []iriscore.AudioChunk{
		{Data: []byte("chunk-1"), Format: iriscore.AudioFormatMP3_44100_128},
		{Data: []byte("chunk-2")},
	}
	final := &iriscore.SpeechResponse{
		ID:      "audio-9",
		Model:   "eleven_flash_v2_5",
		VoiceID: "voice-1",
		Usage: iriscore.SpeechUsage{
			InputCharacters: 5,
			AudioDuration:   2 * time.Second,
		},
	}
	synthesizer := &mockSpeechSynthesizer{
		stream: finishedSpeechStream(chunks, final, nil),
	}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)

	env := core.NewEnvelope()
	env.Trace.RunID = "run-1"
	env.SetVar("text", "Hello")

	result, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := synthesizer.requests[0]
	if req.Text != "Hello" || req.VoiceID != "voice-1" {
		t.Errorf("request = %+v", req)
	}

	// One delta event per chunk.
	deltas := 0
	for _, event := range events {
		if event.Kind == runtime.EventNodeOutputDelta {
			deltas++
		}
	}
	if deltas != 2 {
		t.Errorf("delta events = %d, want 2", deltas)
	}

	// Assembled audio artifact.
	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if string(artifacts[0].Bytes) != "chunk-1chunk-2" {
		t.Errorf("artifact bytes = %q", artifacts[0].Bytes)
	}
	if artifacts[0].ID != "audio-9" {
		t.Errorf("artifact ID = %q, want final response ID", artifacts[0].ID)
	}
	if artifacts[0].MimeType != "audio/mpeg" {
		t.Errorf("artifact mime = %q, want format inferred from first chunk", artifacts[0].MimeType)
	}
	if artifacts[0].Meta["streamed"] != true {
		t.Errorf("artifact meta = %+v", artifacts[0].Meta)
	}

	// Final event with metadata only.
	finals := 0
	for _, event := range events {
		if event.Kind == runtime.EventNodeOutputFinal {
			finals++
			if _, present := event.Payload["audio_bytes"]; !present {
				t.Errorf("final event missing audio_bytes: %+v", event.Payload)
			}
			if _, present := event.Payload["text"]; present {
				t.Errorf("final event carries text payload: %+v", event.Payload)
			}
		}
	}
	if finals != 1 {
		t.Errorf("final events = %d, want 1", finals)
	}
}

func TestSpeechSynthesizeNode_Run_StreamWithoutFinal(t *testing.T) {
	chunks := []iriscore.AudioChunk{
		{Data: []byte("chunk-1"), Format: iriscore.AudioFormatPCM16000},
	}
	synthesizer := &mockSpeechSynthesizer{
		stream: finishedSpeechStream(chunks, nil, nil),
	}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	artifacts := result.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 audio artifact, got %d", len(artifacts))
	}
	if string(artifacts[0].Bytes) != "chunk-1" {
		t.Errorf("artifact bytes = %q", artifacts[0].Bytes)
	}
	if artifacts[0].MimeType != "audio/pcm" {
		t.Errorf("artifact mime = %q, want format from chunk", artifacts[0].MimeType)
	}
	// ID falls back to a generated stable-in-run identifier.
	if !strings.HasPrefix(artifacts[0].ID, "test_") {
		t.Errorf("artifact ID = %q, want generated prefix", artifacts[0].ID)
	}
}

func TestSpeechSynthesizeNode_Run_StreamError(t *testing.T) {
	chunks := []iriscore.AudioChunk{
		{Data: []byte("chunk-1")},
	}
	synthesizer := &mockSpeechSynthesizer{
		stream: finishedSpeechStream(chunks, nil, errors.New("stream broke")),
	}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected stream error to propagate")
	}
	if !strings.Contains(err.Error(), "stream broke") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_StreamCancellation(t *testing.T) {
	// A stream whose chunk channel never closes until the context is done.
	synthesizer := &mockSpeechSynthesizer{}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
		Timeout:  50 * time.Millisecond,
	})

	synthesizer.stream = blockingSpeechStream()

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline error, got %v", err)
	}
}

// blockingSpeechStream returns a stream that never resolves on its own.
func blockingSpeechStream() *iriscore.SpeechStream {
	ch := make(chan iriscore.AudioChunk)
	errCh := make(chan error)
	finalCh := make(chan *iriscore.SpeechResponse)
	return &iriscore.SpeechStream{Ch: ch, Err: errCh, Final: finalCh}
}

func TestSpeechSynthesizeNode_Run_StreamExceedsMaxBytes(t *testing.T) {
	chunks := []iriscore.AudioChunk{
		{Data: []byte("0123456789")},
		{Data: []byte("0123456789")},
	}
	synthesizer := &mockSpeechSynthesizer{
		stream: finishedSpeechStream(chunks, nil, nil),
	}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
		MaxBytes: 15,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected streamed audio to exceed max_bytes")
	}
	if !strings.Contains(err.Error(), "exceeds max_bytes") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechSynthesizeNode_Run_StreamSetupErrorWhileCancelled(t *testing.T) {
	synthesizer := &mockSpeechSynthesizer{err: errors.New("setup failed")}
	node := NewSpeechSynthesizeNode("test", synthesizer, SpeechSynthesizeNodeConfig{
		VoiceID:  "voice-1",
		InputVar: "text",
		Stream:   true,
	})

	env := core.NewEnvelope()
	env.SetVar("text", "Hello")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context cancellation, got %v", err)
	}
}

func TestParseSpeechSynthesizeConfig(t *testing.T) {
	seed := int64(42)
	cfg, err := ParseSpeechSynthesizeConfig(map[string]any{
		"model":          "eleven_flash_v2_5",
		"voice_id":       "voice-1",
		"input_var":      "text",
		"audio_format":   "mp3",
		"sample_rate":    float64(44100),
		"bit_rate":       float64(128),
		"language":       "en",
		"seed":           float64(42),
		"stream":         true,
		"max_bytes":      float64(1024),
		"output_var":     "out",
		"omit_artifacts": true,
		"timeout":        "45s",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "eleven_flash_v2_5" || cfg.VoiceID != "voice-1" || cfg.InputVar != "text" {
		t.Errorf("string fields = %+v", cfg)
	}
	if cfg.AudioFormat != "mp3" || cfg.SampleRate != 44100 || cfg.BitRate != 128 {
		t.Errorf("format fields = %+v", cfg)
	}
	if cfg.Seed == nil || *cfg.Seed != seed {
		t.Errorf("seed = %v", cfg.Seed)
	}
	if !cfg.Stream || !cfg.OmitArtifacts {
		t.Errorf("bool fields = %+v", cfg)
	}
	if cfg.MaxBytes != 1024 || cfg.Timeout != 45*time.Second {
		t.Errorf("bounds = %d/%v", cfg.MaxBytes, cfg.Timeout)
	}
}

func TestParseSpeechSynthesizeConfig_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "missing voice_id",
			config: map[string]any{},
			want:   "voice_id is required",
		},
		{
			name:   "invalid audio format",
			config: map[string]any{"voice_id": "v1", "audio_format": "midi"},
			want:   "unsupported audio_format",
		},
		{
			name:   "negative sample rate",
			config: map[string]any{"voice_id": "v1", "sample_rate": float64(-1)},
			want:   "must be non-negative",
		},
		{
			name:   "negative bit rate",
			config: map[string]any{"voice_id": "v1", "bit_rate": float64(-1)},
			want:   "must be non-negative",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSpeechSynthesizeConfig(tc.config)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// Compile-time check that a concrete mock satisfies the typed synthesizer port.
var _ SpeechSynthesizer = (*mockSpeechSynthesizer)(nil)

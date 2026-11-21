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

// mockSpeechRecognizer captures requests and returns a canned response.
type mockSpeechRecognizer struct {
	response *iriscore.TranscriptionResponse
	err      error
	requests []*iriscore.TranscriptionRequest
}

func (m *mockSpeechRecognizer) Transcribe(ctx context.Context, req *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// blockingSpeechRecognizer blocks until the context is cancelled, then
// returns the context error (simulating an in-flight provider call).
type blockingSpeechRecognizer struct{}

func (blockingSpeechRecognizer) Transcribe(ctx context.Context, _ *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func validTranscriptionResponse() *iriscore.TranscriptionResponse {
	return &iriscore.TranscriptionResponse{
		ID:       "transcript-1",
		Model:    "scribe_v1",
		Text:     "Hello from the meeting",
		Language: "en",
		Duration: 3 * time.Second,
		IsFinal:  true,
		Usage: iriscore.TranscriptionUsage{
			AudioDuration: 3 * time.Second,
			InputBytes:    1024,
		},
	}
}

func TestNewSpeechTranscribeNode(t *testing.T) {
	node := NewSpeechTranscribeNode("test-stt", &mockSpeechRecognizer{}, SpeechTranscribeNodeConfig{})

	if node.ID() != "test-stt" {
		t.Errorf("expected ID 'test-stt', got %q", node.ID())
	}
	if node.Kind() != core.NodeKindSpeechTranscribe {
		t.Errorf("expected kind %q, got %q", core.NodeKindSpeechTranscribe, node.Kind())
	}
}

func TestNewSpeechTranscribeNode_Defaults(t *testing.T) {
	node := NewSpeechTranscribeNode("my-node", &mockSpeechRecognizer{}, SpeechTranscribeNodeConfig{})
	config := node.Config()
	if config.OutputVar != "my-node_transcript" {
		t.Errorf("expected default output var 'my-node_transcript', got %q", config.OutputVar)
	}
	if config.Timeout != 120*time.Second {
		t.Errorf("expected default timeout 120s, got %v", config.Timeout)
	}
}

func TestSpeechTranscribeNode_Run_TypedAudioData(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		Model:       "scribe_v1",
		InputVar:    "audio",
		Language:    "en",
		Diarize:     true,
		NumSpeakers: func() *int { v := 2; return &v }(),
		Timestamps:  "word",
		OutputVar:   "transcript",
	})

	env := core.NewEnvelope()
	env.Trace.RunID = "run-1"
	env.SetVar("audio", &iriscore.AudioData{
		Data:     []byte("fake-audio"),
		Format:   iriscore.AudioFormat{Encoding: iriscore.AudioEncodingMP3},
		Filename: "meeting.mp3",
	})

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recognizer.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(recognizer.requests))
	}
	req := recognizer.requests[0]
	if req.Model != "scribe_v1" {
		t.Errorf("model = %q, want %q", req.Model, "scribe_v1")
	}
	if req.Language != "en" {
		t.Errorf("language = %q, want %q", req.Language, "en")
	}
	if !req.Diarize {
		t.Error("expected diarize=true")
	}
	if req.NumSpeakers == nil || *req.NumSpeakers != 2 {
		t.Errorf("num_speakers = %v, want 2", req.NumSpeakers)
	}
	if req.Timestamps != iriscore.TimestampGranularityWord {
		t.Errorf("timestamps = %q, want %q", req.Timestamps, iriscore.TimestampGranularityWord)
	}
	if req.Audio == nil || string(req.Audio.Data) != "fake-audio" {
		t.Errorf("audio payload = %+v, want inline bytes", req.Audio)
	}
	if req.Metadata["node_id"] != "test" || req.Metadata["run_id"] != "run-1" {
		t.Errorf("metadata = %v, want node/run identity", req.Metadata)
	}

	text, ok := result.GetVar("transcript")
	if !ok || text != "Hello from the meeting" {
		t.Errorf("transcript = %v, want %q", text, "Hello from the meeting")
	}

	statsRaw, ok := result.GetVar("transcript_stats")
	if !ok {
		t.Fatal("expected transcript_stats to be stored")
	}
	stats := statsRaw.(SpeechTranscribeStats)
	if stats.Words != 0 || stats.IsFinal != true || stats.InputBytes != len("fake-audio") {
		t.Errorf("stats = %+v", stats)
	}

	artifacts := result.GetArtifactsByType("transcript")
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 transcript artifact, got %d", len(artifacts))
	}
	artifact := artifacts[0]
	if artifact.ID != "transcript-1" {
		t.Errorf("artifact ID = %q, want provider response ID", artifact.ID)
	}
	if artifact.Text != "Hello from the meeting" {
		t.Errorf("artifact text = %q", artifact.Text)
	}
	if artifact.MimeType != "text/plain" {
		t.Errorf("artifact mime = %q, want text/plain", artifact.MimeType)
	}
	if artifact.Meta["language"] != "en" || artifact.Meta["is_final"] != true {
		t.Errorf("artifact meta = %+v", artifact.Meta)
	}
}

func TestSpeechTranscribeNode_Run_BytesInputWithFormatHint(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "wav",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("raw-wav-bytes"))

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := recognizer.requests[0]
	if req.Audio.Format.Encoding != iriscore.AudioEncodingWAV {
		t.Errorf("encoding = %q, want wav", req.Audio.Format.Encoding)
	}

	_, ok := result.GetVar("test_transcript")
	if !ok {
		t.Error("expected default output var to be stored")
	}
}

func TestSpeechTranscribeNode_Run_Base64StringInput(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	encoded := "ZmFrZS1hdWRpbw==" // base64("fake-audio")
	env := core.NewEnvelope()
	env.SetVar("audio", encoded)

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := recognizer.requests[0].Audio.GetBytes()
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(data) != "fake-audio" {
		t.Errorf("decoded audio = %q, want %q", data, "fake-audio")
	}
}

func TestSpeechTranscribeNode_Run_ArtifactInput(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar: "audio",
	})

	env := core.NewEnvelope()
	env.AppendArtifact(core.Artifact{
		ID:       "audio-1",
		Type:     "file",
		MimeType: "audio/mpeg",
		Bytes:    []byte("artifact-audio"),
	})
	env.SetVar("audio", env.Artifacts[0])

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := recognizer.requests[0]
	if string(req.Audio.Data) != "artifact-audio" {
		t.Errorf("audio bytes = %q", req.Audio.Data)
	}
	if req.Audio.Format.Encoding != iriscore.AudioEncodingMP3 {
		t.Errorf("encoding = %q, want mp3 inferred from mime", req.Audio.Format.Encoding)
	}
}

func TestSpeechTranscribeNode_Run_EnvelopeInputFallback(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{})

	env := core.NewEnvelope()
	env.Input = &iriscore.AudioData{
		Data:   []byte("input-audio"),
		Format: iriscore.AudioFormat{Encoding: iriscore.AudioEncodingMP3},
	}

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(recognizer.requests[0].Audio.Data) != "input-audio" {
		t.Errorf("audio bytes = %q", recognizer.requests[0].Audio.Data)
	}
}

func TestSpeechTranscribeNode_Run_MissingInputVar(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar: "audio",
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for missing audio input var")
	}
	if !strings.Contains(err.Error(), `audio input variable "audio" not found`) {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_Run_UnsupportedInputType(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar: "audio",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", 42)
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unsupported input type")
	}
	if !strings.Contains(err.Error(), "unsupported audio input type int") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_Run_MissingEncoding(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar: "audio",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unresolvable audio encoding")
	}
	if !strings.Contains(err.Error(), "audio encoding missing") {
		t.Errorf("error = %v", err)
	}
	if len(recognizer.requests) != 0 {
		t.Error("recognizer must not be called without an encoding")
	}
}

func TestSpeechTranscribeNode_Run_ArtifactWithoutInlineBytes(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar: "audio",
	})

	env := core.NewEnvelope()
	offloaded := core.Artifact{ID: "audio-1", Type: "file", MimeType: "audio/mpeg", URI: "artifact://sha256/abc"}
	env.SetVar("audio", offloaded)

	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for artifact without inline bytes")
	}
	if !strings.Contains(err.Error(), "no inline bytes") {
		t.Errorf("error = %v", err)
	}
	if len(recognizer.requests) != 0 {
		t.Error("recognizer must not be called on invalid input")
	}
}

func TestSpeechTranscribeNode_Run_NoRecognizer(t *testing.T) {
	node := NewSpeechTranscribeNode("test", nil, SpeechTranscribeNodeConfig{})
	_, err := node.Run(context.Background(), core.NewEnvelope())
	if err == nil {
		t.Fatal("expected error for missing recognizer")
	}
	if !strings.Contains(err.Error(), "no recognizer configured") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_Run_RecognizerError(t *testing.T) {
	recognizer := &mockSpeechRecognizer{
		response: validTranscriptionResponse(),
		err:      errors.New("provider unavailable"),
	}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	result, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected recognizer error to propagate")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("error = %v", err)
	}
	if result != nil {
		if _, ok := result.GetVar("test_transcript"); ok {
			t.Error("transcript must not be committed on failure")
		}
		if len(result.GetArtifactsByType("transcript")) != 0 {
			t.Error("artifact must not be committed on failure")
		}
	}
}

func TestSpeechTranscribeNode_Run_ContextCancellation(t *testing.T) {
	node := NewSpeechTranscribeNode("test", blockingSpeechRecognizer{}, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
		Timeout:     50 * time.Millisecond,
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline error, got %v", err)
	}
}

func TestSpeechTranscribeNode_Run_RecognizerErrorWhileCancelled(t *testing.T) {
	recognizer := &mockSpeechRecognizer{err: errors.New("network blip")}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context cancellation, got %v", err)
	}
}

func TestSpeechTranscribeNode_Run_PartialTranscriptRejected(t *testing.T) {
	resp := validTranscriptionResponse()
	resp.IsFinal = false
	recognizer := &mockSpeechRecognizer{response: resp}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected partial transcript to be rejected")
	}
	if !strings.Contains(err.Error(), "not committed") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_Run_InvalidResponseRejected(t *testing.T) {
	resp := validTranscriptionResponse()
	resp.Duration = -time.Second
	recognizer := &mockSpeechRecognizer{response: resp}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected invalid response to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid transcription response") {
		t.Errorf("error = %v", err)
	}
}

func TestSpeechTranscribeNode_Run_NilResponseRejected(t *testing.T) {
	recognizer := &mockSpeechRecognizer{}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected nil response to be rejected")
	}
}

func TestSpeechTranscribeNode_Run_OmitArtifacts(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:      "audio",
		AudioFormat:   "mp3",
		OmitArtifacts: true,
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GetArtifactsByType("transcript")) != 0 {
		t.Error("artifact must be omitted when configured")
	}
	text, ok := result.GetVar("test_transcript")
	if !ok || text != "Hello from the meeting" {
		t.Errorf("transcript var = %v, ok = %v", text, ok)
	}
}

func TestSpeechTranscribeNode_Run_EmptyOutputVarFallsBackToID(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		OutputVar: "",
	})
	if node.Config().OutputVar != "test_transcript" {
		t.Errorf("expected default output var, got %q", node.Config().OutputVar)
	}
}

func TestSpeechTranscribeNode_Run_RequestValidationError(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
		NumSpeakers: func() *int { v := 0; return &v }(),
	})

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected request validation error")
	}
	if !strings.Contains(err.Error(), "invalid transcription request") {
		t.Errorf("error = %v", err)
	}
	if len(recognizer.requests) != 0 {
		t.Error("recognizer must not be called when request validation fails")
	}
}

func TestSpeechTranscribeNode_Run_OutputFinalEvent(t *testing.T) {
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
		Model:       "scribe_v1",
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)

	env := core.NewEnvelope()
	env.Trace.RunID = "run-1"
	env.SetVar("audio", []byte("fake-audio"))

	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	event := events[0]
	if event.Kind != runtime.EventNodeOutputFinal {
		t.Errorf("kind = %v, want %v", event.Kind, runtime.EventNodeOutputFinal)
	}
	if event.NodeID != "test" || event.NodeKind != core.NodeKindSpeechTranscribe {
		t.Errorf("node info = %q/%q", event.NodeID, event.NodeKind)
	}
	// Default policy: no transcript content, only a shape descriptor.
	if _, present := event.Payload["text"]; present {
		t.Errorf("text payload present by default, want metadata only: %+v", event.Payload)
	}
	if event.Payload["text_bytes"] != len("Hello from the meeting") {
		t.Errorf("text_bytes = %v", event.Payload["text_bytes"])
	}
	if event.Payload["model"] != "scribe_v1" || event.Payload["is_final"] != true {
		t.Errorf("metadata payload = %+v", event.Payload)
	}
}

func TestSpeechTranscribeNode_Run_OutputFinalEventContentCapture(t *testing.T) {
	secret := "hello world"
	recognizer := &mockSpeechRecognizer{response: validTranscriptionResponse()}
	node := NewSpeechTranscribeNode("test", recognizer, SpeechTranscribeNodeConfig{
		InputVar:    "audio",
		AudioFormat: "mp3",
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	capture := runtime.ContentCapture{Enabled: true, MaxBytes: 8}
	ctx := runtime.ContextWithContentCapture(
		runtime.ContextWithEmitter(context.Background(), emitter), capture)

	env := core.NewEnvelope()
	env.SetVar("audio", []byte("fake-audio"))

	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	text, _ := events[0].Payload["text"].(string)
	if text == "" {
		t.Fatalf("captured text missing: %+v", events[0].Payload)
	}
	if strings.Contains(text, secret) {
		t.Errorf("captured text is not bounded: %q", text)
	}
}

func TestParseSpeechTranscribeConfig(t *testing.T) {
	numSpeakers := 3
	temperature := 0.5
	cfg, err := ParseSpeechTranscribeConfig(map[string]any{
		"model":            "scribe_v2",
		"input_var":        "audio",
		"audio_format":     "flac",
		"language":         "en",
		"keyterms":         []any{"PetalFlow", "Iris"},
		"diarize":          true,
		"num_speakers":     float64(numSpeakers),
		"timestamps":       "segment",
		"tag_audio_events": true,
		"temperature":      temperature,
		"max_bytes":        float64(1024),
		"output_var":       "out",
		"omit_artifacts":   true,
		"timeout":          "45s",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "scribe_v2" || cfg.InputVar != "audio" || cfg.AudioFormat != "flac" {
		t.Errorf("string fields = %+v", cfg)
	}
	if !cfg.Diarize || !cfg.TagAudioEvents || !cfg.OmitArtifacts {
		t.Errorf("bool fields = %+v", cfg)
	}
	if cfg.NumSpeakers == nil || *cfg.NumSpeakers != 3 {
		t.Errorf("num_speakers = %v", cfg.NumSpeakers)
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.5 {
		t.Errorf("temperature = %v", cfg.Temperature)
	}
	if cfg.MaxBytes != 1024 {
		t.Errorf("max_bytes = %d", cfg.MaxBytes)
	}
	if cfg.Timeout != 45*time.Second {
		t.Errorf("timeout = %v", cfg.Timeout)
	}
	if len(cfg.Keyterms) != 2 || cfg.Keyterms[0] != "PetalFlow" {
		t.Errorf("keyterms = %v", cfg.Keyterms)
	}
}

func TestParseSpeechTranscribeConfig_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "invalid audio format",
			config: map[string]any{"audio_format": "midi"},
			want:   "unsupported audio_format",
		},
		{
			name:   "invalid timestamps",
			config: map[string]any{"timestamps": "millisecond"},
			want:   "unsupported timestamps granularity",
		},
		{
			name:   "non-positive num_speakers",
			config: map[string]any{"num_speakers": float64(0)},
			want:   "num_speakers must be > 0",
		},
		{
			name:   "negative temperature",
			config: map[string]any{"temperature": -0.5},
			want:   "temperature must be >= 0",
		},
		{
			name:   "negative max_bytes",
			config: map[string]any{"max_bytes": float64(-1)},
			want:   "max_bytes must be non-negative",
		},
		{
			name:   "empty keyterm",
			config: map[string]any{"keyterms": []any{"ok", "  "}},
			want:   "keyterms[1] must not be empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSpeechTranscribeConfig(tc.config)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestAudioEncodingFromMIME(t *testing.T) {
	cases := map[string]iriscore.AudioEncoding{
		"audio/mpeg":                iriscore.AudioEncodingMP3,
		"audio/mp3":                 iriscore.AudioEncodingMP3,
		"audio/wav":                 iriscore.AudioEncodingWAV,
		"audio/x-wav":               iriscore.AudioEncodingWAV,
		"audio/pcm":                 iriscore.AudioEncodingPCM,
		"audio/flac":                iriscore.AudioEncodingFLAC,
		"audio/unknown":             "",
		"":                          "",
		"audio/mpeg; charset=bogus": iriscore.AudioEncodingMP3,
	}
	for mime, want := range cases {
		if got := audioEncodingFromMIME(mime); got != want {
			t.Errorf("audioEncodingFromMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

// Compile-time check that a concrete mock satisfies the typed recognizer port.
var _ SpeechRecognizer = (*mockSpeechRecognizer)(nil)

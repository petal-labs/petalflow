//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"
	irisproviders "github.com/petal-labs/iris/v2/providers"
)

// Deterministic speech mocks shared by the speech_transcribe and
// speech_synthesize integration tests. None of these touch the network.

// speechAndTTSFakeProvider implements both Iris speech capabilities so the
// hydration default-factory e2e can resolve speech nodes by provider name.
type speechAndTTSFakeProvider struct{}

func (speechAndTTSFakeProvider) ID() string { return "elevenlabs-mock" }

func (speechAndTTSFakeProvider) Models() []iriscore.ModelInfo { return nil }

func (speechAndTTSFakeProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat ||
		f == iriscore.FeatureSpeechRecognition ||
		f == iriscore.FeatureSpeechSynthesis
}

func (speechAndTTSFakeProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}

func (speechAndTTSFakeProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

func (speechAndTTSFakeProvider) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{
		ID:       "mock-transcript-1",
		Model:    "scribe_v1",
		Text:     "PetalFlow transcribed this line deterministically.",
		Language: "en",
		Duration: 2 * time.Second,
		IsFinal:  true,
	}, nil
}

func (speechAndTTSFakeProvider) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{
		ID:      "mock-audio-1",
		Model:   "eleven_flash_v2_5",
		VoiceID: "voice-1",
		Audio: iriscore.AudioData{
			Data:   []byte("deterministic-fake-audio"),
			Format: iriscore.AudioFormatMP3_44100_128,
		},
		Usage: iriscore.SpeechUsage{
			InputCharacters: 7,
			AudioDuration:   2 * time.Second,
			OutputBytes:     int64(len("deterministic-fake-audio")),
		},
	}, nil
}

func (speechAndTTSFakeProvider) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return finishedSynthesisStream([]iriscore.AudioChunk{
		{Data: []byte("deterministic-fake-audio"), Format: iriscore.AudioFormatMP3_44100_128},
	}, nil, nil), nil
}

// chatOnlyFakeProvider implements only chat, mirroring a provider without
// speech capabilities.
type chatOnlyFakeProvider struct{}

func (chatOnlyFakeProvider) ID() string                   { return "chat-only-mock" }
func (chatOnlyFakeProvider) Models() []iriscore.ModelInfo { return nil }
func (chatOnlyFakeProvider) Supports(iriscore.Feature) bool {
	return true
}
func (chatOnlyFakeProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (chatOnlyFakeProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

func init() {
	// Register the fake providers in the Iris provider registry so hydration
	// default factories can resolve them by name without network access.
	irisproviders.Register("elevenlabs-mock", func(string) iriscore.Provider { return speechAndTTSFakeProvider{} })
	irisproviders.Register("chat-only-mock", func(string) iriscore.Provider { return chatOnlyFakeProvider{} })
	irisproviders.Register("voice-fake-mock", func(string) iriscore.Provider { return voiceProviderFakeProvider{} })
}

// mockIntegrationSynthesizer returns a complete canned audio response.
type mockIntegrationSynthesizer struct{}

func (mockIntegrationSynthesizer) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{
		ID:      "mock-audio-1",
		Model:   "eleven_flash_v2_5",
		VoiceID: "voice-1",
		Audio: iriscore.AudioData{
			Data:   []byte("deterministic-fake-audio"),
			Format: iriscore.AudioFormatMP3_44100_128,
		},
		Usage: iriscore.SpeechUsage{
			InputCharacters: 7,
			AudioDuration:   2 * time.Second,
			OutputBytes:     int64(len("deterministic-fake-audio")),
		},
	}, nil
}

func (mockIntegrationSynthesizer) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return finishedSynthesisStream([]iriscore.AudioChunk{
		{Data: []byte("deterministic-fake-audio"), Format: iriscore.AudioFormatMP3_44100_128},
	}, nil, nil), nil
}

// streamingMockSynthesizer returns a resolved multi-chunk stream.
type streamingMockSynthesizer struct{}

func (streamingMockSynthesizer) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{Audio: iriscore.AudioData{Data: []byte("chunk-achunk-b")}}, nil
}

func (streamingMockSynthesizer) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return finishedSynthesisStream([]iriscore.AudioChunk{
		{Data: []byte("chunk-a"), Format: iriscore.AudioFormatMP3_44100_128},
		{Data: []byte("chunk-b")},
	}, &iriscore.SpeechResponse{
		ID:      "mock-audio-stream-1",
		Model:   "eleven_flash_v2_5",
		VoiceID: "voice-1",
		Usage:   iriscore.SpeechUsage{InputCharacters: 5},
	}, nil), nil
}

// failingIntegrationSynthesizer always errors, mirroring a provider outage.
type failingIntegrationSynthesizer struct{}

func (failingIntegrationSynthesizer) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return nil, errMockSynthesisUnavailable
}

func (failingIntegrationSynthesizer) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	return nil, errMockSynthesisUnavailable
}

var errMockSynthesisUnavailable = errorString("synthesis unavailable")

// blockingIntegrationSynthesizer blocks until its context is cancelled,
// mirroring an in-flight provider call that never completes.
type blockingIntegrationSynthesizer struct{}

func (blockingIntegrationSynthesizer) Synthesize(ctx context.Context, _ *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingIntegrationSynthesizer) StreamSynthesize(ctx context.Context, _ *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// finishedSynthesisStream builds a SpeechStream with all channels resolved.
func finishedSynthesisStream(chunks []iriscore.AudioChunk, final *iriscore.SpeechResponse, streamErr error) *iriscore.SpeechStream {
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

// voiceSessionFakeSession is a deterministic in-process iriscore.VoiceSession
// for the voice integration tests; events flow through a bounded emitter.
type voiceSessionFakeSession struct {
	id          string
	emitter     *iriscore.VoiceEventEmitter
	resultsMu   sync.Mutex
	toolResults []iriscore.ToolResult
}

func (s *voiceSessionFakeSession) ID() string { return s.id }
func (s *voiceSessionFakeSession) State() iriscore.VoiceSessionState {
	return iriscore.VoiceSessionStateActive
}
func (s *voiceSessionFakeSession) SendAudio(context.Context, iriscore.AudioChunk) error { return nil }
func (s *voiceSessionFakeSession) SendText(context.Context, string) error               { return nil }
func (s *voiceSessionFakeSession) SubmitToolResult(_ context.Context, result iriscore.ToolResult) error {
	s.resultsMu.Lock()
	defer s.resultsMu.Unlock()
	s.toolResults = append(s.toolResults, result)
	return nil
}

// submittedResults returns the recorded tool results.
func (s *voiceSessionFakeSession) submittedResults() []iriscore.ToolResult {
	s.resultsMu.Lock()
	defer s.resultsMu.Unlock()
	return append([]iriscore.ToolResult(nil), s.toolResults...)
}

func (s *voiceSessionFakeSession) Interrupt(context.Context) error { return nil }
func (s *voiceSessionFakeSession) Events() <-chan iriscore.VoiceEvent {
	return s.emitter.Events()
}
func (s *voiceSessionFakeSession) Err() <-chan error {
	return s.emitter.Err()
}
func (s *voiceSessionFakeSession) Close() error {
	s.emitter.Close()
	return nil
}

// fakeVoiceMock hands out deterministic fake voice sessions and lets tests
// drive events through the most recently started session.
var fakeVoiceMock = &voiceSessionFakeHarness{}

type voiceSessionFakeHarness struct {
	mu       sync.Mutex
	sessions []*voiceSessionFakeSession
	counter  int
}

func (h *voiceSessionFakeHarness) StartVoiceSession(_ context.Context, cfg iriscore.VoiceSessionConfig) (iriscore.VoiceSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counter++
	id := cfg.SessionID
	if id == "" {
		id = fmt.Sprintf("fake-voice-session-%d", h.counter)
	}
	sess := &voiceSessionFakeSession{
		id:      id,
		emitter: iriscore.NewVoiceEventEmitter(id, 0),
	}
	h.sessions = append(h.sessions, sess)
	return sess, nil
}

// latest emits evt through the most recently started fake session.
func (h *voiceSessionFakeHarness) latest() *voiceSessionFakeSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sessions) == 0 {
		return nil
	}
	return h.sessions[len(h.sessions)-1]
}

// emitTurnCompleted delivers the committed agent text and turn_completed
// envelope for turnID.
func (h *voiceSessionFakeHarness) emitTurnCompleted(turnID, text string) {
	sess := h.latest()
	if sess == nil {
		return
	}
	_ = sess.emitter.Emit(context.Background(), iriscore.VoiceEvent{
		Type:    iriscore.VoiceEventAgentTextCommitted,
		TurnID:  turnID,
		Text:    text,
		IsFinal: true,
	})
	_ = sess.emitter.Emit(context.Background(), iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventTurnCompleted,
		TurnID: turnID,
		Text:   text,
	})
}

// voiceProviderFakeProvider advertises full-duplex voice sessions.
type voiceProviderFakeProvider struct{}

func (voiceProviderFakeProvider) ID() string                   { return "voice-fake-mock" }
func (voiceProviderFakeProvider) Models() []iriscore.ModelInfo { return nil }
func (voiceProviderFakeProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat || f == iriscore.FeatureVoiceSession
}
func (voiceProviderFakeProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (voiceProviderFakeProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}
func (voiceProviderFakeProvider) StartVoiceSession(ctx context.Context, cfg iriscore.VoiceSessionConfig) (iriscore.VoiceSession, error) {
	return fakeVoiceMock.StartVoiceSession(ctx, cfg)
}

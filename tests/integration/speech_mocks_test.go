//go:build integration

package integration

import (
	"context"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"
)

// Deterministic speech mocks shared by the speech_transcribe and
// speech_synthesize integration tests. None of these touch the network.

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

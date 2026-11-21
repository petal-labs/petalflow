// Package voice provides PetalFlow's session-oriented voice execution
// boundary: typed session handles for full-duplex real-time voice
// conversations, layered over the Iris voice session API.
//
// Voice sessions are deliberately separate from one-shot DAG nodes: they
// model a long-lived, stateful, interruptible conversation (with session
// handles, typed event envelopes, and turn IDs) rather than a single
// envelope-in/envelope-out transformation. Sessions never enter the graph
// IR; they run alongside or outside workflow execution.
//
// The package reuses the Iris event and turn machinery (iriscore.VoiceEvent,
// iriscore.VoiceTurnTracker, iriscore.VoiceSessionConfig) verbatim — no
// provider speech logic is duplicated. See docs/voice-sessions.md for the
// lifecycle, event-envelope, turn-ID, and cancellation specification.
package voice

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	iriscore "github.com/petal-labs/iris/v2/core"
)

// Session is PetalFlow's typed handle for one live full-duplex voice session.
//
// The handle owns the session lifecycle separate from one-shot DAG nodes:
//
//   - Lifecycle: started via a SessionStarter, transitioned by the provider
//     (Connecting → Active → Listening/Thinking/Speaking → Closed), terminated
//     explicitly with Close (idempotent, safe for concurrent calls).
//   - Turn IDs: every user utterance, assistant response, audio burst, tool
//     exchange, and interruption is correlated by a monotonic turn ID; the
//     handle coordinates turns through an Iris VoiceTurnTracker.
//   - Interrupt/cancel propagation: Interrupt cancels the active turn (turn
//     context + Iris session), marks it cancelled so stale audio is
//     suppressed, and lets barge-in supersede it. Cancelling the parent
//     context terminates the session asynchronously.
//   - Event envelopes: Events() forwards the bounded, typed
//     iriscore.VoiceEvent stream (sequence numbers, session/turn correlation)
//     closed promptly on termination.
//
// Session is safe for concurrent use.
type Session struct {
	// session is the underlying Iris voice session handle.
	session iriscore.VoiceSession

	// turns coordinates turn IDs, per-turn cancellation, and stale-audio
	// filtering after interruptions.
	turns *iriscore.VoiceTurnTracker

	// sessionCtx is the session-scoped parent context; Close cancels it.
	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	closeOnce sync.Once
	closed    atomic.Bool
}

// NewSession wraps an Iris voice session handle with PetalFlow turn and
// lifecycle management. The returned Session takes ownership of session.
// parent is the session-scoped context; cancelling it terminates the session
// (the same as Close).
func NewSession(parent context.Context, session iriscore.VoiceSession) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	if parent != nil {
		ctx, cancel = context.WithCancel(parent)
	}
	return &Session{
		session:       session,
		turns:         iriscore.NewVoiceTurnTracker("turn"),
		sessionCtx:    ctx,
		sessionCancel: cancel,
	}
}

// ID returns the underlying session identifier.
func (s *Session) ID() string {
	return s.session.ID()
}

// State returns the current session lifecycle state.
func (s *Session) State() iriscore.VoiceSessionState {
	return s.session.State()
}

// SendAudio validates and transmits an incremental user audio chunk.
// The chunk is validated against the configured MaxAudioChunkBytes limit by
// the provider; on a closed session the error wraps iriscore.ErrVoiceSessionClosed.
func (s *Session) SendAudio(ctx context.Context, chunk iriscore.AudioChunk) error {
	if err := s.sessionCtx.Err(); err != nil {
		return fmt.Errorf("voice session %s closed: %w", s.ID(), iriscore.ErrVoiceSessionClosed)
	}
	return s.session.SendAudio(s.scoped(ctx), chunk)
}

// SendText injects a user text utterance into the voice session.
func (s *Session) SendText(ctx context.Context, text string) error {
	if err := s.sessionCtx.Err(); err != nil {
		return fmt.Errorf("voice session %s closed: %w", s.ID(), iriscore.ErrVoiceSessionClosed)
	}
	return s.session.SendText(s.scoped(ctx), text)
}

// SubmitToolResult delivers the result for a prior VoiceEventToolCall.
func (s *Session) SubmitToolResult(ctx context.Context, result iriscore.ToolResult) error {
	if err := s.sessionCtx.Err(); err != nil {
		return fmt.Errorf("voice session %s closed: %w", s.ID(), iriscore.ErrVoiceSessionClosed)
	}
	return s.session.SubmitToolResult(s.scoped(ctx), result)
}

// scoped keeps per-call deadlines while preserving session-scoped cancellation.
func (s *Session) scoped(ctx context.Context) context.Context {
	if ctx == nil {
		return s.sessionCtx
	}
	return ctx
}

// Events returns the bounded receive-only stream of typed
// iriscore.VoiceEvent envelopes. Closed when the session terminates.
func (s *Session) Events() <-chan iriscore.VoiceEvent {
	return s.session.Events()
}

// Err returns a receive-only channel emitting at most one terminal error
// before closing.
func (s *Session) Err() <-chan error {
	return s.session.Err()
}

// StartTurn opens a new conversational turn: it returns the monotonic turn ID
// and a per-turn context that is cancelled when the turn is completed,
// interrupted, superseded by a newer turn, or when the session closes. Every
// downstream action for the turn (assistant generation, playback) should be
// bound to the returned turn context.
func (s *Session) StartTurn() (string, context.Context) {
	return s.turns.StartTurnWithContext(s.sessionCtx)
}

// CompleteTurn marks the turn finished and releases its cancellation
// context. Returns false when turnID is not the active turn.
func (s *Session) CompleteTurn(turnID string) bool {
	return s.turns.CompleteTurn(turnID)
}

// Interrupt cancels the currently active assistant turn (client cancel):
// the turn's context is cancelled, the Iris session is asked to interrupt
// in-flight generation, and the turn is marked cancelled so stale audio
// chunks are suppressed by ShouldPlayAudio. Returns the interruption payload
// and true, or zero/false when no turn is active.
func (s *Session) Interrupt() (iriscore.VoiceInterruption, bool) {
	interruption, ok := s.turns.Interrupt(iriscore.VoiceInterruptionClientCancel)
	if ok {
		_ = s.session.Interrupt(s.sessionCtx)
	}
	return interruption, ok
}

// ObserveEvent folds a session event into consumer-side turn state so turn
// tracking stays in sync with provider-driven events (barge-in, turn
// completion, session end).
func (s *Session) ObserveEvent(evt iriscore.VoiceEvent) {
	s.turns.ObserveEvent(evt)
}

// ShouldPlayAudio reports whether an audio-output event belongs to the
// active, non-interrupted turn. Playback consumers must drop audio events
// for which this returns false.
func (s *Session) ShouldPlayAudio(evt iriscore.VoiceEvent) bool {
	return s.turns.ShouldPlayAudio(evt)
}

// ActiveTurnID returns the currently active non-interrupted turn ID.
func (s *Session) ActiveTurnID() string {
	return s.turns.ActiveTurnID()
}

// Close terminates the session: the active turn (if any) is cancelled, the
// session-scoped context is cancelled, and the underlying Iris session is
// closed. Close is idempotent and safe for concurrent calls.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.sessionCancel()
		_, _ = s.turns.Interrupt(iriscore.VoiceInterruptionSessionClosed)
		err = s.session.Close()
	})
	return err
}

// Closed reports whether the handle has been closed.
func (s *Session) Closed() bool {
	return s.closed.Load()
}

// SessionStarter opens PetalFlow voice session handles from provider
// credentials resolved through hydration. Implementations discover the Iris
// voice_session capability on the provider and fail at construction for
// providers that do not support full-duplex voice sessions.
type SessionStarter interface {
	StartVoiceSession(ctx context.Context, cfg iriscore.VoiceSessionConfig) (*Session, error)
}

// StarterFunc adapts a function into a SessionStarter.
type StarterFunc func(ctx context.Context, cfg iriscore.VoiceSessionConfig) (*Session, error)

// StartVoiceSession implements SessionStarter.
func (f StarterFunc) StartVoiceSession(ctx context.Context, cfg iriscore.VoiceSessionConfig) (*Session, error) {
	return f(ctx, cfg)
}

// Ensure the adapter satisfies the typed starter port.
var _ SessionStarter = StarterFunc(nil)

package voice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"
)

// fakeVoiceSession is a deterministic iriscore.VoiceSession used to drive the
// PetalFlow handle from tests. It never performs I/O.
type fakeVoiceSession struct {
	mu            sync.Mutex
	id            string
	state         iriscore.VoiceSessionState
	sendTexts     []string
	audioChunks   []iriscore.AudioChunk
	toolResults   []iriscore.ToolResult
	interrupts    int
	closes        int
	events        chan iriscore.VoiceEvent
	errCh         chan error
	closedEmitter *iriscore.VoiceEventEmitter
}

func newFakeVoiceSession(id string) *fakeVoiceSession {
	s := &fakeVoiceSession{
		id:     id,
		state:  iriscore.VoiceSessionStateActive,
		events: make(chan iriscore.VoiceEvent, 64),
		errCh:  make(chan error, 1),
	}
	return s
}

func (f *fakeVoiceSession) ID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.id
}

func (f *fakeVoiceSession) State() iriscore.VoiceSessionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeVoiceSession) SendAudio(_ context.Context, chunk iriscore.AudioChunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audioChunks = append(f.audioChunks, chunk)
	return nil
}

func (f *fakeVoiceSession) SendText(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTexts = append(f.sendTexts, text)
	return nil
}

func (f *fakeVoiceSession) SubmitToolResult(_ context.Context, result iriscore.ToolResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolResults = append(f.toolResults, result)
	return nil
}

func (f *fakeVoiceSession) Interrupt(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupts++
	return nil
}

func (f *fakeVoiceSession) Events() <-chan iriscore.VoiceEvent {
	return f.events
}

func (f *fakeVoiceSession) Err() <-chan error {
	return f.errCh
}

func (f *fakeVoiceSession) Close() error {
	f.mu.Lock()
	f.closes++
	f.state = iriscore.VoiceSessionStateClosed
	f.mu.Unlock()
	close(f.events)
	return nil
}

// emit delivers an event through the fake's event channel.
func (f *fakeVoiceSession) emit(evt iriscore.VoiceEvent) {
	f.events <- evt
}

// fail terminates the fake's event stream with a terminal error.
func (f *fakeVoiceSession) fail(err error) {
	f.errCh <- err
	close(f.errCh)
	close(f.events)
}

func newTestSession(t *testing.T, session *fakeVoiceSession) *Session {
	t.Helper()
	return NewSession(context.Background(), session)
}

func TestSession_HandleLifecycle(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	if got := s.ID(); got != "sess-1" {
		t.Errorf("ID = %q, want sess-1", got)
	}
	if got := s.State(); got != iriscore.VoiceSessionStateActive {
		t.Errorf("state = %q, want active", got)
	}
	if s.Closed() {
		t.Error("session must not be closed before Close")
	}

	if err := s.SendText(context.Background(), "hello"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if len(fake.sendTexts) != 1 || fake.sendTexts[0] != "hello" {
		t.Errorf("sendTexts = %v", fake.sendTexts)
	}

	if err := s.SendAudio(context.Background(), iriscore.AudioChunk{Data: []byte("audio"), IsFinal: false}); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}
	if len(fake.audioChunks) != 1 {
		t.Errorf("audioChunks = %v", fake.audioChunks)
	}

	if err := s.SubmitToolResult(context.Background(), iriscore.ToolResult{CallID: "call-1", Content: "ok"}); err != nil {
		t.Fatalf("SubmitToolResult: %v", err)
	}
	if len(fake.toolResults) != 1 || fake.toolResults[0].CallID != "call-1" {
		t.Errorf("toolResults = %v", fake.toolResults)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !s.Closed() {
		t.Error("Closed must report true after Close")
	}
	if fake.closes != 1 {
		t.Errorf("underlying closes = %d, want 1", fake.closes)
	}

	// Idempotent: subsequent Close is a no-op.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if fake.closes != 1 {
		t.Errorf("underlying closes after idempotent Close = %d, want 1", fake.closes)
	}

	// Post-close operations are rejected without touching the provider.
	if err := s.SendText(context.Background(), "late"); err == nil {
		t.Fatal("expected SendText on closed session to fail")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error = %v", err)
	}
	if len(fake.sendTexts) != 1 {
		t.Errorf("sendTexts after close = %v, want unchanged", fake.sendTexts)
	}
}

func TestSession_TurnIDsMonotonic(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turn1, ctx1 := s.StartTurn()
	turn2, ctx2 := s.StartTurn()

	if turn1 == "" || turn2 == "" || turn1 == turn2 {
		t.Fatalf("turn IDs = %q/%q, want distinct monotonic IDs", turn1, turn2)
	}
	// Starting a newer turn supersedes (cancels) the previous one.
	select {
	case <-ctx1.Done():
	default:
		t.Error("superseded turn context must be cancelled")
	}
	if s.ActiveTurnID() != turn2 {
		t.Errorf("ActiveTurnID = %q, want %q", s.ActiveTurnID(), turn2)
	}
	if !s.CompleteTurn(turn2) {
		t.Error("CompleteTurn(active) must succeed")
	}
	select {
	case <-ctx2.Done():
	default:
		t.Error("completed turn context must be released")
	}
	if s.ActiveTurnID() != "" {
		t.Errorf("ActiveTurnID after completion = %q, want empty", s.ActiveTurnID())
	}
	if s.CompleteTurn(turn2) {
		t.Error("re-completing a completed turn must fail")
	}
}

func TestSession_InterruptCancelsTurnAndPropagates(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turnID, turnCtx := s.StartTurn()

	interruption, ok := s.Interrupt()
	if !ok {
		t.Fatal("expected interrupt of the active turn to succeed")
	}
	if interruption.CancelledTurnID != turnID {
		t.Errorf("CancelledTurnID = %q, want %q", interruption.CancelledTurnID, turnID)
	}
	if interruption.Reason != iriscore.VoiceInterruptionClientCancel {
		t.Errorf("Reason = %q, want client_cancel", interruption.Reason)
	}

	// Cancellation propagation: the turn context is cancelled...
	select {
	case <-turnCtx.Done():
	default:
		t.Error("interrupted turn context must be cancelled")
	}
	// ...and the interrupt is forwarded to the underlying Iris session.
	if fake.interrupts != 1 {
		t.Errorf("underlying interrupts = %d, want 1", fake.interrupts)
	}

	// Stale audio from the cancelled turn is suppressed.
	if s.ShouldPlayAudio(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventAudioOutput,
		TurnID: turnID,
		Audio:  &iriscore.AudioChunk{Data: []byte("stale")},
	}) {
		t.Error("stale audio after interruption must not be playable")
	}
	// Interrupting again is a no-op.
	if _, ok := s.Interrupt(); ok {
		t.Error("interrupt with no active turn must report false")
	}
}

func TestSession_ShouldPlayAudio(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turnID, _ := s.StartTurn()
	evt := iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventAudioOutput,
		TurnID: turnID,
		Audio:  &iriscore.AudioChunk{Data: []byte("fresh")},
	}
	if !s.ShouldPlayAudio(evt) {
		t.Error("audio of the active turn must be playable")
	}

	// Non-audio events and audio without chunks never pass.
	if s.ShouldPlayAudio(iriscore.VoiceEvent{Type: iriscore.VoiceEventAgentTextDelta, TurnID: turnID}) {
		t.Error("non-audio events must not be playable")
	}
	if s.ShouldPlayAudio(iriscore.VoiceEvent{Type: iriscore.VoiceEventAudioOutput, TurnID: turnID}) {
		t.Error("audio_output without a chunk must not be playable")
	}
	if s.ShouldPlayAudio(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventAudioOutput,
		TurnID: "other-turn",
		Audio:  &iriscore.AudioChunk{Data: []byte("foreign")},
	}) {
		t.Error("audio from a foreign turn must not be playable")
	}
}

func TestSession_ObserveEvent_ProviderDrivenBargeIn(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turnID, turnCtx := s.StartTurn()

	// The provider reports barge-in for the active turn.
	s.ObserveEvent(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventTurnInterrupted,
		TurnID: turnID,
		Interruption: &iriscore.VoiceInterruption{
			CancelledTurnID: turnID,
			Reason:          iriscore.VoiceInterruptionBargeIn,
		},
	})

	select {
	case <-turnCtx.Done():
	default:
		t.Error("provider-driven barge-in must cancel the active turn context")
	}
	if s.ActiveTurnID() != "" {
		t.Errorf("ActiveTurnID after barge-in = %q, want empty", s.ActiveTurnID())
	}
	if s.ShouldPlayAudio(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventAudioOutput,
		TurnID: turnID,
		Audio:  &iriscore.AudioChunk{Data: []byte("stale")},
	}) {
		t.Error("stale audio after provider barge-in must not be playable")
	}
}

func TestSession_EventsEnvelopeCorrelation(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turnID, _ := s.StartTurn()
	fake.emit(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventTurnStarted,
		TurnID: turnID,
	})
	fake.emit(iriscore.VoiceEvent{
		Type:    iriscore.VoiceEventAgentTextCommitted,
		TurnID:  turnID,
		Text:    "done",
		IsFinal: true,
	})

	var got []iriscore.VoiceEvent
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case evt, ok := <-s.Events():
			if !ok {
				t.Fatal("events channel closed unexpectedly")
			}
			got = append(got, evt)
		case <-deadline:
			t.Fatal("timed out waiting for session events")
		}
	}
	if got[0].Type != iriscore.VoiceEventTurnStarted || got[0].TurnID != turnID {
		t.Errorf("event[0] = %+v", got[0])
	}
	if got[1].Type != iriscore.VoiceEventAgentTextCommitted || got[1].TurnID != turnID || got[1].Text != "done" {
		t.Errorf("event[1] = %+v", got[1])
	}
}

func TestSession_CloseCancelsTurnsAndEventStream(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	_, turnCtx := s.StartTurn()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Turn context is cancelled by session close.
	select {
	case <-turnCtx.Done():
	default:
		t.Error("session close must cancel the active turn")
	}

	// The underlying event stream is closed promptly.
	select {
	case _, ok := <-s.Events():
		if ok {
			t.Error("event stream must be closed after session close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event stream closure")
	}
}

func TestSession_TerminalError(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	fake.fail(errors.New("websocket dropped"))

	select {
	case err, ok := <-s.Err():
		if !ok {
			t.Fatal("error channel closed without a terminal error")
		}
		if err == nil || !strings.Contains(err.Error(), "websocket dropped") {
			t.Errorf("terminal error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal error")
	}
}

func TestSession_ParentContextCancellationTerminates(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	parent, cancel := context.WithCancel(context.Background())
	s := NewSession(parent, fake)

	if err := s.SendText(context.Background(), "before cancel"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	cancel()
	// Allow the cancellation to propagate.
	time.Sleep(10 * time.Millisecond)

	if err := s.SendText(context.Background(), "after cancel"); err == nil {
		t.Fatal("expected SendText after parent cancellation to fail")
	}
}

// TestSession_ConcurrentCloseAndOps exercises concurrent Close/SendText/
// Interrupt/StartTurn for data races and panics (run under -race via make test).
func TestSession_ConcurrentCloseAndOps(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.StartTurn()
				_ = s.SendText(context.Background(), "hello")
				_, _ = s.Interrupt()
				_ = s.Close()
			}
		}()
	}
	wg.Wait()

	if !s.Closed() {
		t.Error("session must be closed after concurrent Close calls")
	}
	if fake.closes != 1 {
		t.Errorf("underlying closes = %d, want exactly 1", fake.closes)
	}
}

func TestSession_SendAudioOnTurnCancelledContext(t *testing.T) {
	fake := newFakeVoiceSession("sess-1")
	s := newTestSession(t, fake)

	turnID, turnCtx := s.StartTurn()
	s.CompleteTurn(turnID)

	// A per-call context honouring the (cancelled) turn still reaches the
	// provider; the handle never blocks the caller on dead turns.
	if err := s.SendAudio(turnCtx, iriscore.AudioChunk{Data: []byte("late"), IsFinal: true}); err != nil {
		t.Errorf("SendAudio with cancelled turn ctx: %v", err)
	}
	if len(fake.audioChunks) != 1 {
		t.Errorf("audioChunks = %v", fake.audioChunks)
	}
}

// Compile-time checks: the fake is a valid Iris voice session, and the
// PetalFlow handle satisfies the session starter surface used by llmprovider.
var (
	_ iriscore.VoiceSession = (*fakeVoiceSession)(nil)
	_ SessionStarter        = StarterFunc(nil)
)

//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/voice"

	iriscore "github.com/petal-labs/iris/v2/core"
)

// The voiceSessionFakeProvider is registered in the Iris provider registry by
// speech_mocks_test.go as "voice-fake-mock" and needs no network.

func TestVoiceSessionStarter_ConfigFileCredentialResolution(t *testing.T) {
	// Credential resolution through the existing config-file flow.
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"providers": {"voice-fake-mock": {"api_key": "ek-mock-secret-do-not-log"}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("PETALFLOW_CONFIG", configPath)

	providers, err := hydrate.ResolveProviders(nil)
	if err != nil {
		t.Fatalf("resolving providers: %v", err)
	}

	starter, err := llmprovider.NewVoiceSessionStarter("voice-fake-mock", providers["voice-fake-mock"])
	if err != nil {
		t.Fatalf("creating voice session starter: %v", err)
	}

	session, err := starter.StartVoiceSession(context.Background(), iriscore.VoiceSessionConfig{
		SessionID: "integration-session-1",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("StartVoiceSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if session.ID() != "integration-session-1" {
		t.Errorf("session ID = %q, want configured correlation ID", session.ID())
	}
	if session.State() != iriscore.VoiceSessionStateActive {
		t.Errorf("session state = %q", session.State())
	}
}

func TestVoiceSessionStarter_TurnLifecycleAndInterrupt(t *testing.T) {
	providers := hydrate.ProviderMap{"voice-fake-mock": {APIKey: "ek-mock-key"}}
	starter, err := llmprovider.NewVoiceSessionStarter("voice-fake-mock", providers["voice-fake-mock"])
	if err != nil {
		t.Fatalf("creating voice session starter: %v", err)
	}

	session, err := starter.StartVoiceSession(context.Background(), iriscore.VoiceSessionConfig{})
	if err != nil {
		t.Fatalf("StartVoiceSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	// Turn 1: a complete conversational turn with a committed agent response.
	turn1, turn1Ctx := session.StartTurn()
	if turn1 == "" {
		t.Fatal("expected a turn ID")
	}
	if err := session.SendText(context.Background(), "tell me a joke"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	// Simulate the provider's event envelope for the turn.
	fakeVoiceMock.emitTurnCompleted(turn1, "Why did the graph go to therapy?")
	if !session.CompleteTurn(turn1) {
		t.Fatal("expected turn 1 to complete")
	}
	select {
	case <-turn1Ctx.Done():
	default:
		t.Error("completed turn context must be released")
	}

	// Turn 2: barge-in interruption cancels the turn and suppresses stale audio.
	turn2, turn2Ctx := session.StartTurn()
	interruption, ok := session.Interrupt()
	if !ok {
		t.Fatal("expected client-cancel interrupt of the active turn")
	}
	if interruption.CancelledTurnID != turn2 {
		t.Errorf("CancelledTurnID = %q, want %q", interruption.CancelledTurnID, turn2)
	}
	select {
	case <-turn2Ctx.Done():
	default:
		t.Error("interrupted turn context must be cancelled")
	}
	if session.ShouldPlayAudio(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventAudioOutput,
		TurnID: turn2,
		Audio:  &iriscore.AudioChunk{Data: []byte("stale")},
	}) {
		t.Error("stale audio from the interrupted turn must not be playable")
	}

	// The handle forwards the bounded typed event envelopes.
	deadline := time.After(2 * time.Second)
	var gotCompleted bool
	for !gotCompleted {
		select {
		case evt, ok := <-session.Events():
			if !ok {
				t.Fatal("events closed unexpectedly")
			}
			if evt.Type == iriscore.VoiceEventTurnCompleted && evt.TurnID == turn1 {
				gotCompleted = true
				if evt.Text != "Why did the graph go to therapy?" {
					t.Errorf("committed text = %q", evt.Text)
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn_completed event")
		}
	}
}

func TestVoiceSessionStarter_CancellationClosesEvents(t *testing.T) {
	providers := hydrate.ProviderMap{"voice-fake-mock": {APIKey: "ek-mock-key"}}
	starter, err := llmprovider.NewVoiceSessionStarter("voice-fake-mock", providers["voice-fake-mock"])
	if err != nil {
		t.Fatalf("creating voice session starter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	session, err := starter.StartVoiceSession(ctx, iriscore.VoiceSessionConfig{})
	if err != nil {
		t.Fatalf("StartVoiceSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	cancel()
	time.Sleep(10 * time.Millisecond)

	if err := session.SendText(context.Background(), "after cancel"); err == nil {
		t.Fatal("expected SendText after parent cancellation to fail")
	}
}

func TestVoiceSessionStarter_UnsupportedCapabilityFailsConstruction(t *testing.T) {
	const apiKey = "ek-mock-secret-do-not-log"
	_, err := llmprovider.NewVoiceSessionStarter("chat-only-mock", hydrate.ProviderConfig{APIKey: apiKey})
	if err == nil {
		t.Fatal("expected construction failure for provider without voice sessions")
	}
	if !strings.Contains(err.Error(), "does not support voice sessions") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks API key: %q", err.Error())
	}
}

func TestVoiceSessionStarter_MissingCredentialFailsConstruction(t *testing.T) {
	_, err := llmprovider.NewVoiceSessionStarter("voice-fake-mock", hydrate.ProviderConfig{})
	if err == nil {
		t.Fatal("expected construction failure for unresolved credentials")
	}
	if !strings.Contains(err.Error(), "requires an API key for speech") {
		t.Errorf("error = %v", err)
	}
}

// Compile-time boundary checks.
var _ voice.SessionStarter = voice.StarterFunc(nil)

//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/voice"
)

// TestVoiceToolBridge_ExecutesRegistryTools deterministically verifies the
// voice tool bridge end to end: a session started through the Iris voice
// pipeline fake delivers tool_call events, the bridge executes the matching
// PetalFlow tool through the registry, and the result is submitted back to
// the session with the model's call ID preserved.
func TestVoiceToolBridge_ExecutesRegistryTools(t *testing.T) {
	var executed bool
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("lookup_order", "look up an order", func(_ context.Context, args map[string]any) (map[string]any, error) {
		executed = true
		return map[string]any{"status": "shipped", "order": args["order_id"]}, nil
	}))

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

	bridge, err := voice.NewToolBridge(session, voice.ToolBridgeConfig{Registry: registry})
	if err != nil {
		t.Fatalf("NewToolBridge: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- bridge.Serve(ctx) }()

	// The provider pipeline emits a tool_call for the active turn.
	turnID, _ := session.StartTurn()
	fake := fakeVoiceMock.latest()
	if fake == nil {
		t.Fatal("expected a fake session from the harness")
	}
	if err := fake.emitter.Emit(ctx, iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventToolCall,
		TurnID: turnID,
		ToolCall: &iriscore.ToolCall{
			ID:        "call-42",
			Name:      "lookup_order",
			Arguments: []byte(`{"order_id":"A-1001"}`),
		},
	}); err != nil {
		t.Fatalf("emitting tool_call: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var result *iriscore.ToolResult
	for result == nil {
		for _, r := range fake.submittedResults() {
			if r.CallID == "call-42" {
				result = &r
				break
			}
		}
		if result != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for submitted tool result")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if (*result).IsError {
		t.Fatalf("unexpected error result: %+v", *result)
	}
	content, ok := (*result).Content.(map[string]any)
	if !ok || content["status"] != "shipped" || content["order"] != "A-1001" {
		t.Fatalf("result content = %+v", (*result).Content)
	}
	if !executed {
		t.Fatal("registry tool was not executed")
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

// TestVoiceToolBridge_UnknownToolFailsClosed verifies the safe tool-call
// semantics end to end: model-requested tools outside the registry are
// answered with an error result and never executed.
func TestVoiceToolBridge_UnknownToolFailsClosed(t *testing.T) {
	registry := core.NewToolRegistry() // empty registry: nothing is executable

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

	bridge, err := voice.NewToolBridge(session, voice.ToolBridgeConfig{Registry: registry})
	if err != nil {
		t.Fatalf("NewToolBridge: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- bridge.Serve(ctx) }()

	fake := fakeVoiceMock.latest()
	if fake == nil {
		t.Fatal("expected a fake session from the harness")
	}
	if err := fake.emitter.Emit(ctx, iriscore.VoiceEvent{
		Type: iriscore.VoiceEventToolCall,
		ToolCall: &iriscore.ToolCall{
			ID:        "call-7",
			Name:      "delete_everything",
			Arguments: []byte(`{}`),
		},
	}); err != nil {
		t.Fatalf("emitting tool_call: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var result *iriscore.ToolResult
	for result == nil {
		for _, r := range fake.submittedResults() {
			if r.CallID == "call-7" {
				result = &r
				break
			}
		}
		if result != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for submitted tool result")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if !(*result).IsError {
		t.Fatal("expected error result for tool outside the registry allowlist")
	}
	cancel()
	if err := <-serveDone; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

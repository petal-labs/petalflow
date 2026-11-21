package voice

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
	toolpolicy "github.com/petal-labs/petalflow/tool"
)

// toolBridgeHarness wires a fake voice session with a recording tool bridge.
type toolBridgeHarness struct {
	fake    *fakeVoiceSession
	session *Session
}

func newToolBridgeHarness(t *testing.T, cfg ToolBridgeConfig) (*toolBridgeHarness, *ToolBridge) {
	t.Helper()
	fake := newFakeVoiceSession("sess-tools")
	session := newTestSession(t, fake)
	bridge, err := NewToolBridge(session, cfg)
	if err != nil {
		t.Fatalf("NewToolBridge: %v", err)
	}
	return &toolBridgeHarness{fake: fake, session: session}, bridge
}

// submit emits a tool_call event through the fake session.
func (h *toolBridgeHarness) call(callID, name, args string) {
	h.fake.emit(iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventToolCall,
		TurnID: "turn-1",
		ToolCall: &iriscore.ToolCall{
			ID:        callID,
			Name:      name,
			Arguments: jsonRaw(args),
		},
	})
}

// waitResult polls the fake session's recorded tool results until one with
// callID appears or the deadline passes.
func (h *toolBridgeHarness) waitResult(t *testing.T, callID string) iriscore.ToolResult {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		h.fake.mu.Lock()
		for _, r := range h.fake.toolResults {
			if r.CallID == callID {
				h.fake.mu.Unlock()
				return r
			}
		}
		h.fake.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for tool result %q", callID)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func jsonRaw(s string) []byte {
	return []byte(s)
}

func TestToolBridge_ExecutesRegistryToolAndSubmitsResult(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("lookup", "find a fact", func(_ context.Context, args map[string]any) (map[string]any, error) {
		return map[string]any{"answer": args["q"]}, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "lookup", `{"q":"voices"}`)
	result := h.waitResult(t, "call-1")
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	content, ok := result.Content.(map[string]any)
	if !ok || content["answer"] != "voices" {
		t.Errorf("content = %+v", result.Content)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestToolBridge_UnknownToolRejectedWithoutExecution(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("known", "known tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"ran": true}, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "not-registered", `{}`)
	result := h.waitResult(t, "call-1")
	if !result.IsError {
		t.Fatal("expected error result for unregistered tool")
	}
	if !strings.Contains(fmt.Sprint(result.Content), "not available in this session") {
		t.Errorf("content = %+v", result.Content)
	}

	cancel()
	<-done
}

func TestToolBridge_OversizedArgsRejected(t *testing.T) {
	registry := core.NewToolRegistry()
	invoked := false
	registry.Register(core.NewFuncTool("tool", "tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		invoked = true
		return nil, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{
		Registry:     registry,
		ArgsMaxBytes: 16,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "tool", `{"blob":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	result := h.waitResult(t, "call-1")
	if !result.IsError || !strings.Contains(fmt.Sprint(result.Content), "exceed 16 bytes") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if invoked {
		t.Error("oversized arguments must never reach the tool")
	}

	cancel()
	<-done
}

func TestToolBridge_NonObjectArgsRejected(t *testing.T) {
	registry := core.NewToolRegistry()
	invoked := false
	registry.Register(core.NewFuncTool("tool", "tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		invoked = true
		return nil, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "tool", `["array","not","object"]`)
	result := h.waitResult(t, "call-1")
	if !result.IsError || !strings.Contains(fmt.Sprint(result.Content), "must be a JSON object") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if invoked {
		t.Error("non-object arguments must never reach the tool")
	}

	cancel()
	<-done
}

func TestToolBridge_ToolErrorBecomesErrorResult(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("failing", "failing tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("disk exploded")
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "failing", `{}`)
	result := h.waitResult(t, "call-1")
	if !result.IsError || !strings.Contains(fmt.Sprint(result.Content), "disk exploded") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.CallID != "call-1" {
		t.Errorf("CallID = %q, want call-1 (model correlation preserved)", result.CallID)
	}

	cancel()
	<-done
}

func TestToolBridge_TimeoutBounded(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("slow", "slow tool", func(ctx context.Context, _ map[string]any) (map[string]any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return map[string]any{}, nil
		}
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{
		Registry: registry,
		Timeout:  20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "slow", `{}`)
	result := h.waitResult(t, "call-1")
	if !result.IsError || !strings.Contains(fmt.Sprint(result.Content), "timed out after 20ms") {
		t.Fatalf("unexpected result: %+v", result)
	}

	cancel()
	<-done
}

// denyAllPolicy denies every tool outright.
type denyAllPolicy struct{}

func (denyAllPolicy) EvaluatePolicy(string) toolpolicy.PolicyDecision {
	return toolpolicy.PolicyNeedsApproval
}

func TestToolBridge_PolicyFailsClosed(t *testing.T) {
	registry := core.NewToolRegistry()
	invoked := false
	registry.Register(core.NewFuncTool("gated", "gated tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		invoked = true
		return nil, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	// Voice sessions cannot pause for durable approvals, so an approval
	// requirement fails closed as an error result.
	ctx := toolpolicy.ContextWithPolicyEvaluator(context.Background(), denyAllPolicy{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "gated", `{}`)
	result := h.waitResult(t, "call-1")
	if !result.IsError {
		t.Fatal("expected policy rejection as error result")
	}
	if !strings.Contains(fmt.Sprint(result.Content), "POLICY_APPROVAL_REQUIRED") {
		t.Errorf("content = %+v, want policy error code", result.Content)
	}
	if invoked {
		t.Error("policy-rejected tools must never execute")
	}

	cancel()
	<-done
}

func TestToolBridge_ConcurrencyBound(t *testing.T) {
	var current, max atomic.Int64
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("busy", "busy tool", func(ctx context.Context, _ map[string]any) (map[string]any, error) {
		cur := current.Add(1)
		for {
			old := max.Load()
			if cur <= old || max.CompareAndSwap(old, cur) {
				break
			}
		}
		defer current.Add(-1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Millisecond):
			return map[string]any{}, nil
		}
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{
		Registry:    registry,
		Concurrency: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "busy", `{}`)
	h.call("call-2", "busy", `{}`)
	h.waitResult(t, "call-1")
	h.waitResult(t, "call-2")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && current.Load() != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := max.Load(); got != 1 {
		t.Errorf("max concurrent executions = %d, want 1", got)
	}

	cancel()
	<-done
}

func TestToolBridge_ServeReturnsNilWhenEventStreamCloses(t *testing.T) {
	registry := core.NewToolRegistry()
	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	done := make(chan error, 1)
	go func() { done <- bridge.Serve(context.Background()) }()

	// Session termination closes the event stream.
	if err := h.session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve on closed session = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after event stream closed")
	}
}

func TestToolBridge_ServeRespectsContextCancellation(t *testing.T) {
	registry := core.NewToolRegistry()
	_, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	// The event stream stays open; cancellation alone must stop Serve.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

// TestToolBridge_ReplayBoundaryDocumentsLiveExecution verifies that session
// tool calls always execute live: runtime replay mocks in the surrounding
// context are never consulted, because sessions live outside run replay.
func TestToolBridge_ReplayBoundaryDocumentsLiveExecution(t *testing.T) {
	var invoked atomic.Bool
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("live", "live tool", func(_ context.Context, _ map[string]any) (map[string]any, error) {
		invoked.Store(true)
		return map[string]any{"executed": "live"}, nil
	}))

	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	// A replay mock set in the bridge context (host misuse) must not
	// replace live execution.
	ctx := runtime.ContextWithToolMocks(context.Background(), runtime.ToolMockSet{
		Mode:    runtime.ToolMockModeAll,
		Results: map[string]map[string]any{"call-1": {"mocked": true}},
	})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	h.call("call-1", "live", `{}`)
	result := h.waitResult(t, "call-1")
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	if !invoked.Load() {
		t.Error("session tools must execute live, not via replay mocks")
	}
	content, ok := result.Content.(map[string]any)
	if !ok || content["executed"] != "live" {
		t.Errorf("content = %+v, want live execution output", result.Content)
	}

	cancel()
	<-done
}

func TestToolBridge_IgnoresNonToolEvents(t *testing.T) {
	registry := core.NewToolRegistry()
	h, bridge := newToolBridgeHarness(t, ToolBridgeConfig{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(ctx) }()

	// Non-tool events must not crash or produce results.
	h.fake.emit(iriscore.VoiceEvent{Type: iriscore.VoiceEventSessionState, State: iriscore.VoiceSessionStateListening})
	h.fake.emit(iriscore.VoiceEvent{Type: iriscore.VoiceEventAudioOutput, Audio: &iriscore.AudioChunk{Data: []byte("x")}})
	h.fake.emit(iriscore.VoiceEvent{Type: iriscore.VoiceEventToolCall}) // nil ToolCall payload

	deadline := time.After(100 * time.Millisecond)
	<-deadline

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestToolBridge_RequiresRegistry(t *testing.T) {
	fake := newFakeVoiceSession("sess-x")
	session := newTestSession(t, fake)
	if _, err := NewToolBridge(session, ToolBridgeConfig{}); err == nil {
		t.Fatal("expected error for missing registry")
	}
}

func TestToolBridgeConfig_Defaults(t *testing.T) {
	registry := core.NewToolRegistry()
	bridge, err := NewToolBridge(newTestSession(t, newFakeVoiceSession("s")), ToolBridgeConfig{Registry: registry})
	if err != nil {
		t.Fatalf("NewToolBridge: %v", err)
	}
	cfg := bridge.Config()
	if cfg.Timeout != DefaultToolTimeout || cfg.Concurrency != DefaultToolConcurrency || cfg.ArgsMaxBytes != DefaultToolArgsMaxBytes {
		t.Errorf("defaults = %+v", cfg)
	}
}

package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	toolpolicy "github.com/petal-labs/petalflow/tool"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// mockPetalTool is a mock implementation of adapters.PetalTool for testing.
type mockPetalTool struct {
	name   string
	result map[string]any
	err    error
	calls  []map[string]any // captures all calls
}

func (m *mockPetalTool) Name() string {
	return m.name
}

func (m *mockPetalTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	m.calls = append(m.calls, args)
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestNewToolNode(t *testing.T) {
	tool := &mockPetalTool{name: "test-tool"}
	node := NewToolNode("my-tool", tool, ToolNodeConfig{})

	if node.ID() != "my-tool" {
		t.Errorf("expected ID 'my-tool', got %q", node.ID())
	}
	if node.Kind() != core.NodeKindTool {
		t.Errorf("expected kind %q, got %q", core.NodeKindTool, node.Kind())
	}
}

func TestNewToolNode_DefaultOutputKey(t *testing.T) {
	tool := &mockPetalTool{name: "test-tool"}
	node := NewToolNode("my-node", tool, ToolNodeConfig{})

	config := node.Config()
	if config.OutputKey != "my-node_output" {
		t.Errorf("expected default output key 'my-node_output', got %q", config.OutputKey)
	}
}

func TestNewToolNode_DefaultToolName(t *testing.T) {
	tool := &mockPetalTool{name: "special-tool"}
	node := NewToolNode("my-node", tool, ToolNodeConfig{})

	config := node.Config()
	if config.ToolName != "special-tool" {
		t.Errorf("expected tool name 'special-tool', got %q", config.ToolName)
	}
}

func TestNewToolNode_DefaultTimeout(t *testing.T) {
	tool := &mockPetalTool{name: "test-tool"}
	node := NewToolNode("test", tool, ToolNodeConfig{})

	config := node.Config()
	if config.Timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", config.Timeout)
	}
}

func TestToolNode_Run_BasicExecution(t *testing.T) {
	tool := &mockPetalTool{
		name:   "test-tool",
		result: map[string]any{"status": "success", "count": 42},
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		OutputKey: "result",
	})

	env := core.NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected result to be stored in envelope")
	}
	outputMap := output.(map[string]any)
	if outputMap["status"] != "success" {
		t.Errorf("expected status 'success', got %v", outputMap["status"])
	}
}

func TestToolNode_Run_WithArgsTemplate(t *testing.T) {
	tool := &mockPetalTool{
		name:   "weather",
		result: map[string]any{"temp": 72},
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		ArgsTemplate: map[string]string{
			"location": "user_location",
			"units":    "preferences.units",
		},
	})

	env := core.NewEnvelope().
		WithVar("user_location", "New York").
		WithVar("preferences", map[string]any{"units": "fahrenheit"})

	_, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check args were passed correctly
	if len(tool.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(tool.calls))
	}
	args := tool.calls[0]
	if args["location"] != "New York" {
		t.Errorf("expected location 'New York', got %v", args["location"])
	}
	if args["units"] != "fahrenheit" {
		t.Errorf("expected units 'fahrenheit', got %v", args["units"])
	}
}

func TestToolNode_Run_WithStaticArgs(t *testing.T) {
	tool := &mockPetalTool{
		name:   "test-tool",
		result: map[string]any{},
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		StaticArgs: map[string]any{
			"api_key": "secret123",
			"version": 2,
		},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := tool.calls[0]
	if args["api_key"] != "secret123" {
		t.Errorf("expected api_key 'secret123', got %v", args["api_key"])
	}
	if args["version"] != 2 {
		t.Errorf("expected version 2, got %v", args["version"])
	}
}

func TestToolNode_Run_MissingRequiredArgErrors(t *testing.T) {
	tool := &mockPetalTool{name: "t", result: map[string]any{"ok": true}}
	node := NewToolNode("test", tool, ToolNodeConfig{
		ArgsTemplate: map[string]string{"location": "user_location"},
		RequiredArgs: []string{"location"},
	})

	// user_location is not set on the envelope.
	result, err := node.Run(context.Background(), core.NewEnvelope())
	if err == nil {
		t.Fatal("expected an error for a missing required arg, got nil")
	}
	if !strings.Contains(err.Error(), "location") {
		t.Errorf("error = %v, want it to name the missing arg", err)
	}
	if result != nil {
		t.Errorf("expected nil envelope on failure, got %v", result)
	}
	if len(tool.calls) != 0 {
		t.Error("tool should not be invoked when a required arg is missing")
	}
}

func TestToolNode_Run_RequiredArgPresentSucceeds(t *testing.T) {
	tool := &mockPetalTool{name: "t", result: map[string]any{"ok": true}}
	node := NewToolNode("test", tool, ToolNodeConfig{
		ArgsTemplate: map[string]string{"location": "user_location"},
		RequiredArgs: []string{"location"},
		OutputKey:    "out",
	})

	env := core.NewEnvelope()
	env.SetVar("user_location", "Tokyo")

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("unexpected error when required arg is present: %v", err)
	}
	if len(tool.calls) != 1 || tool.calls[0]["location"] != "Tokyo" {
		t.Errorf("tool called with args %v, want location=Tokyo", tool.calls)
	}
}

func TestToolNode_Run_ToolFailureReturnsError(t *testing.T) {
	tool := &mockPetalTool{
		name: "failing-tool",
		err:  errors.New("tool failed"),
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		OutputKey:   "result",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	result, err := node.Run(context.Background(), core.NewEnvelope())

	// The node always returns its error; the runtime decides fail vs continue.
	if err == nil {
		t.Fatal("expected the tool failure to be returned as an error, got nil")
	}
	if result != nil {
		t.Errorf("expected a nil envelope on failure, got %v", result)
	}
}

func TestToolNode_Run_WithRegistry(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(core.NewFuncTool("multiply", "Multiplies numbers", func(ctx context.Context, args map[string]any) (map[string]any, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		return map[string]any{"result": a * b}, nil
	}))

	node := NewToolNodeWithRegistry("test", registry, ToolNodeConfig{
		ToolName:  "multiply",
		OutputKey: "result",
		StaticArgs: map[string]any{
			"a": 5.0,
			"b": 3.0,
		},
	})

	env := core.NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected result to be stored")
	}
	outputMap := output.(map[string]any)
	if outputMap["result"] != 15.0 {
		t.Errorf("expected 15, got %v", outputMap["result"])
	}
}

func TestToolNode_Run_ToolNotFoundInRegistry(t *testing.T) {
	registry := core.NewToolRegistry()

	node := NewToolNodeWithRegistry("test", registry, ToolNodeConfig{
		ToolName:    "nonexistent",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestToolNode_Run_NoToolOrRegistry(t *testing.T) {
	node := NewToolNode("test", nil, ToolNodeConfig{
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error when no tool or registry configured")
	}
}

func TestToolNode_Run_RetryOnError(t *testing.T) {
	callCount := 0
	tool := &countingMockTool{
		name:      "flaky-tool",
		failCount: 2,
		successResult: map[string]any{
			"status": "success",
		},
		callCount: &callCount,
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 3,
			Backoff:     time.Millisecond,
		},
	})

	env := core.NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}

	output, _ := result.GetVar("test_output")
	outputMap := output.(map[string]any)
	if outputMap["status"] != "success" {
		t.Errorf("expected status 'success', got %v", outputMap["status"])
	}
}

func TestToolNode_Run_Timeout(t *testing.T) {
	tool := &slowMockTool{
		name:  "slow-tool",
		delay: 500 * time.Millisecond,
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		Timeout:     50 * time.Millisecond,
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestToolNode_InterfaceCompliance(t *testing.T) {
	var _ core.Node = (*ToolNode)(nil)
}

func TestToolNode_Run_EmitsToolEvents(t *testing.T) {
	tool := &mockPetalTool{
		name:   "search",
		result: map[string]any{"found": true},
	}

	node := NewToolNode("my-tool", tool, ToolNodeConfig{
		OutputKey:   "result",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})

	ctx := runtime.ContextWithEmitter(context.Background(), emitter)
	env := core.NewEnvelope()
	env.Trace.RunID = "test-run"

	_, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have tool.call and tool.result events
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != runtime.EventToolCall {
		t.Errorf("events[0].Kind = %v, want %v", events[0].Kind, runtime.EventToolCall)
	}
	if events[0].Payload["tool_name"] != "search" {
		t.Errorf("tool_name = %v, want 'search'", events[0].Payload["tool_name"])
	}
	if events[1].Kind != runtime.EventToolResult {
		t.Errorf("events[1].Kind = %v, want %v", events[1].Kind, runtime.EventToolResult)
	}
	if events[1].Payload["is_error"] != false {
		t.Errorf("is_error = %v, want false", events[1].Payload["is_error"])
	}
}

func TestToolNode_Run_ToolCallArgumentsPrivacy(t *testing.T) {
	tool := &mockPetalTool{
		name:   "search",
		result: map[string]any{"found": true},
	}

	node := NewToolNode("my-tool", tool, ToolNodeConfig{
		OutputKey:    "result",
		StaticArgs:   map[string]any{"query": "secret search terms", "limit": 2},
		RequiredArgs: []string{"query", "limit"},
		RetryPolicy:  core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})

	// Default policy: no arguments content, only a shape descriptor.
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)
	env := core.NewEnvelope()
	env.Trace.RunID = "test-run"

	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var callEvent *runtime.Event
	for i := range events {
		if events[i].Kind == runtime.EventToolCall {
			callEvent = &events[i]
		}
	}
	if callEvent == nil {
		t.Fatal("tool.call event missing")
	}
	if _, present := callEvent.Payload["arguments"]; present {
		t.Errorf("tool.call carries arguments by default: %+v", callEvent.Payload)
	}
	shape, ok := callEvent.Payload["arguments_shape"].(map[string]string)
	if !ok {
		t.Fatalf("arguments_shape = %#v, want map[string]string", callEvent.Payload["arguments_shape"])
	}
	if shape["query"] != "string" || shape["limit"] != "number" {
		t.Errorf("arguments_shape = %+v", shape)
	}

	// Opt-in: captured arguments are redacted (values with secret names
	// masked, credential patterns scrubbed).
	events = nil
	capture := runtime.ContentCapture{Enabled: true}
	ctx = runtime.ContextWithContentCapture(
		runtime.ContextWithEmitter(context.Background(), emitter), capture)
	env = core.NewEnvelope()
	env.Trace.RunID = "test-run"

	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range events {
		if events[i].Kind != runtime.EventToolCall {
			continue
		}
		args, ok := events[i].Payload["arguments"].(map[string]any)
		if !ok {
			t.Fatalf("captured arguments = %#v, want map", events[i].Payload["arguments"])
		}
		if args["query"] != "secret search terms" {
			t.Errorf("query = %v, want captured value", args["query"])
		}
		if args["limit"] != 2 {
			t.Errorf("limit = %v, want captured value", args["limit"])
		}
	}
}

// countingMockTool fails a specified number of times before succeeding.
type countingMockTool struct {
	name          string
	failCount     int
	successResult map[string]any
	callCount     *int
}

func (m *countingMockTool) Name() string {
	return m.name
}

func (m *countingMockTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	*m.callCount++
	if *m.callCount <= m.failCount {
		return nil, errors.New("temporary failure")
	}
	return m.successResult, nil
}

// slowMockTool simulates a slow tool for timeout testing.
type slowMockTool struct {
	name  string
	delay time.Duration
}

func (m *slowMockTool) Name() string {
	return m.name
}

func (m *slowMockTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
		return map[string]any{"status": "ok"}, nil
	}
}

func TestToolNode_Run_PolicyRejection(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	var events []runtime.Event
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(name string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyDeny
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) {
		events = append(events, e)
	})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if err == nil {
		t.Fatal("denied tool invocation must fail")
	}
	if !toolpolicy.IsToolPolicyError(err) {
		t.Fatalf("rejection error not a policy error: %v", err)
	}
	if got := len(tool.calls); got != 0 {
		t.Fatalf("tool invoked %d times, want 0 (no side effect)", got)
	}

	var resultEvent *runtime.Event
	for i := range events {
		if events[i].Kind == runtime.EventToolResult {
			resultEvent = &events[i]
		}
	}
	if resultEvent == nil {
		t.Fatalf("no tool.result event emitted, got %d events", len(events))
	}
	if resultEvent.Payload["policy_rejected"] != true {
		t.Fatalf("policy_rejected payload = %v, want true", resultEvent.Payload["policy_rejected"])
	}
	if resultEvent.Payload["error_code"] != toolpolicy.ToolErrorCodePolicyDenied {
		t.Fatalf("error_code payload = %v, want %q", resultEvent.Payload["error_code"], toolpolicy.ToolErrorCodePolicyDenied)
	}
}

func TestToolNode_Run_PolicyAllowUsesEvaluator(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	var events []runtime.Event
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(name string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyAllow
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) {
		events = append(events, e)
	})

	env := core.NewEnvelope()
	if _, err := node.Run(runCtx, env); err != nil {
		t.Fatalf("allowed tool invocation failed: %v", err)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("tool invoked %d times, want 1", len(tool.calls))
	}
}

func TestToolNode_Run_PolicyApprovalRequired(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(name string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyNeedsApproval
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) {})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if !toolpolicy.IsToolPolicyError(err) || toolpolicy.PolicyErrorCode(err) != toolpolicy.ToolErrorCodePolicyApprovalRequired {
		t.Fatalf("needs-approval error = %v, want %q code", err, toolpolicy.ToolErrorCodePolicyApprovalRequired)
	}
	if len(tool.calls) != 0 {
		t.Fatal("needs-approval invocation must not run until approval exists")
	}
}

func TestToolNode_InvalidPayloadRecordedSafely(t *testing.T) {
	tool := &mockPetalTool{
		name: "sized-tool",
		err:  fmt.Errorf("%w: required input %q is missing", toolpolicy.ErrInvalidPayload, "q"),
	}
	node := NewToolNode("sized_node", tool, ToolNodeConfig{OutputKey: "out", RetryPolicy: core.RetryPolicy{MaxAttempts: 1}})

	var events []runtime.Event
	runCtx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) {
		events = append(events, e)
	})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if err == nil {
		t.Fatal("invalid payload must fail the invocation")
	}
	if !contains(err.Error(), "required input") || contains(err.Error(), "payload-value") {
		t.Fatalf("rejection message names fields only: %v", err)
	}

	var resultEvent *runtime.Event
	for i := range events {
		if events[i].Kind == runtime.EventToolResult {
			resultEvent = &events[i]
		}
	}
	if resultEvent == nil {
		t.Fatal("missing tool.result event")
	}
	if resultEvent.Payload["is_error"] != true {
		t.Fatalf("is_error = %v", resultEvent.Payload["is_error"])
	}
	if resultEvent.Payload["error_code"] != toolpolicy.ToolErrorCodeInvalidPayload {
		t.Fatalf("error_code payload = %v, want %q", resultEvent.Payload["error_code"], toolpolicy.ToolErrorCodeInvalidPayload)
	}
	for key, value := range resultEvent.Payload {
		// Rejection events must not echo payload contents or values.
		if s, ok := value.(string); ok && contains(s, "@") {
			t.Fatalf("event payload %q may leak values: %v", key, s)
		}
	}
	if len(tool.calls) != 1 {
		t.Fatalf("typed rejections must not retry: invoked %d times, want 1", len(tool.calls))
	}
}

func TestToolNode_RateLimitRecorded(t *testing.T) {
	tool := &mockPetalTool{
		name: "fluent-tool",
		err:  fmt.Errorf("%w: tool %q issued %d invocations in the last minute (limit %d)", toolpolicy.ErrToolRateLimited, "fluent-tool", 5, 5),
	}
	node := NewToolNode("fluent_node", tool, ToolNodeConfig{OutputKey: "out", RetryPolicy: core.RetryPolicy{MaxAttempts: 1}})

	var events []runtime.Event
	runCtx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) {
		events = append(events, e)
	})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if err == nil {
		t.Fatal("rate limit must fail the invocation")
	}
	var resultEvent *runtime.Event
	for i := range events {
		if events[i].Kind == runtime.EventToolResult {
			resultEvent = &events[i]
		}
	}
	if resultEvent == nil || resultEvent.Payload["error_code"] != toolpolicy.ToolErrorCodeRateLimited {
		t.Fatalf("rate limit error_code payload missing: %#v", resultEvent)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("typed rejections must not retry: invoked %d times, want 1", len(tool.calls))
	}
}

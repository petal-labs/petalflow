package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestNewToolNode_DefaultOnError(t *testing.T) {
	tool := &mockPetalTool{name: "test-tool"}
	node := NewToolNode("test", tool, ToolNodeConfig{})

	config := node.Config()
	if config.OnError != core.ErrorPolicyFail {
		t.Errorf("expected default OnError 'fail', got %q", config.OnError)
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

func TestToolNode_Run_Error_FailPolicy(t *testing.T) {
	tool := &mockPetalTool{
		name: "failing-tool",
		err:  errors.New("tool failed"),
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		OnError:     core.ErrorPolicyFail,
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestToolNode_Run_Error_ContinuePolicy(t *testing.T) {
	tool := &mockPetalTool{
		name: "failing-tool",
		err:  errors.New("tool failed"),
	}

	node := NewToolNode("test", tool, ToolNodeConfig{
		OnError:     core.ErrorPolicyContinue,
		OutputKey:   "result",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	result, err := node.Run(context.Background(), env)

	// Should not return error with continue policy
	if err != nil {
		t.Fatalf("expected no error with continue policy, got: %v", err)
	}

	// Output should be nil
	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected result key to exist")
	}
	if output != nil {
		t.Errorf("expected nil output, got %v", output)
	}

	// Error should be recorded
	errMsg, ok := result.GetVar("result_error")
	if !ok {
		t.Fatal("expected error message to be stored")
	}
	if errMsg == "" {
		t.Error("expected non-empty error message")
	}

	// Should have error in Errors list
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error recorded, got %d", len(result.Errors))
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

package irisadapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/petal-labs/iris/tools"
	"github.com/petal-labs/petalflow"
)

// mockTool is a mock implementation of tools.Tool for testing.
type mockTool struct {
	name        string
	description string
	callResult  any
	callError   error
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return m.description
}

func (m *mockTool) Schema() tools.ToolSchema {
	return tools.ToolSchema{
		JSONSchema: json.RawMessage(`{"type": "object"}`),
	}
}

func (m *mockTool) Call(ctx context.Context, args json.RawMessage) (any, error) {
	if m.callError != nil {
		return nil, m.callError
	}
	return m.callResult, nil
}

func TestNewToolAdapter(t *testing.T) {
	mock := &mockTool{name: "test-tool", description: "A test tool"}
	adapter := NewToolAdapter(mock)

	if adapter == nil {
		t.Fatal("expected adapter to be non-nil")
	}
	if adapter.Name() != "test-tool" {
		t.Errorf("expected name 'test-tool', got %q", adapter.Name())
	}
	if adapter.Description() != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", adapter.Description())
	}
}

func TestToolAdapter_Invoke_MapResult(t *testing.T) {
	mock := &mockTool{
		name:       "test-tool",
		callResult: map[string]any{"result": "success", "count": 42},
	}

	adapter := NewToolAdapter(mock)
	result, err := adapter.Invoke(context.Background(), map[string]any{"input": "test"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["result"] != "success" {
		t.Errorf("expected result 'success', got %v", result["result"])
	}
	// When result is already a map, it's returned directly (no JSON round-trip)
	if result["count"] != 42 {
		t.Errorf("expected count 42, got %v", result["count"])
	}
}

func TestToolAdapter_Invoke_StructResult(t *testing.T) {
	type TestResult struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	mock := &mockTool{
		name:       "test-tool",
		callResult: TestResult{Name: "test", Value: 100},
	}

	adapter := NewToolAdapter(mock)
	result, err := adapter.Invoke(context.Background(), map[string]any{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["name"] != "test" {
		t.Errorf("expected name 'test', got %v", result["name"])
	}
	if result["value"] != float64(100) {
		t.Errorf("expected value 100, got %v", result["value"])
	}
}

func TestToolAdapter_Invoke_NilResult(t *testing.T) {
	mock := &mockTool{
		name:       "test-tool",
		callResult: nil,
	}

	adapter := NewToolAdapter(mock)
	result, err := adapter.Invoke(context.Background(), map[string]any{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil empty map for nil result")
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestToolAdapter_Invoke_Error(t *testing.T) {
	expectedErr := errors.New("tool failed")
	mock := &mockTool{
		name:      "test-tool",
		callError: expectedErr,
	}

	adapter := NewToolAdapter(mock)
	_, err := adapter.Invoke(context.Background(), map[string]any{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error to contain original error")
	}
}

func TestToolAdapter_Invoke_PrimitiveResult(t *testing.T) {
	// When result is a primitive that can't be unmarshaled to map,
	// it should be wrapped in {"result": value}
	mock := &mockTool{
		name:       "test-tool",
		callResult: "simple string result",
	}

	adapter := NewToolAdapter(mock)
	result, err := adapter.Invoke(context.Background(), map[string]any{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["result"] != "simple string result" {
		t.Errorf("expected wrapped result, got %v", result)
	}
}

func TestToResultMap_Map(t *testing.T) {
	input := map[string]any{"key": "value"}
	result, err := toResultMap(input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("expected 'value', got %v", result["key"])
	}
}

func TestToResultMap_Nil(t *testing.T) {
	result, err := toResultMap(nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestPetalTool_InterfaceCompliance(t *testing.T) {
	var _ petalflow.PetalTool = (*ToolAdapter)(nil)
}

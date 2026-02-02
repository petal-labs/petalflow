package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestBaseNode(t *testing.T) {
	base := NewBaseNode("test-id", NodeKindLLM)

	if base.ID() != "test-id" {
		t.Errorf("BaseNode.ID() = %v, want 'test-id'", base.ID())
	}
	if base.Kind() != NodeKindLLM {
		t.Errorf("BaseNode.Kind() = %v, want %v", base.Kind(), NodeKindLLM)
	}
}

func TestNoopNode(t *testing.T) {
	node := NewNoopNode("noop-1")

	if node.ID() != "noop-1" {
		t.Errorf("NoopNode.ID() = %v, want 'noop-1'", node.ID())
	}
	if node.Kind() != NodeKindNoop {
		t.Errorf("NoopNode.Kind() = %v, want %v", node.Kind(), NodeKindNoop)
	}

	// Test Run passes through unchanged
	env := NewEnvelope()
	env.SetVar("test", "value")

	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("NoopNode.Run() error = %v", err)
	}
	if result != env {
		t.Error("NoopNode.Run() should return same envelope")
	}
}

func TestFuncNode(t *testing.T) {
	called := false
	fn := func(ctx context.Context, env *Envelope) (*Envelope, error) {
		called = true
		env.SetVar("processed", true)
		return env, nil
	}

	node := NewFuncNode("func-1", fn)

	if node.ID() != "func-1" {
		t.Errorf("FuncNode.ID() = %v, want 'func-1'", node.ID())
	}
	if node.Kind() != NodeKindNoop {
		t.Errorf("FuncNode.Kind() = %v, want %v (default)", node.Kind(), NodeKindNoop)
	}

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("FuncNode.Run() error = %v", err)
	}
	if !called {
		t.Error("FuncNode.Run() did not call function")
	}
	if v, ok := result.GetVar("processed"); !ok || v != true {
		t.Error("FuncNode.Run() did not set processed var")
	}
}

func TestFuncNode_WithKind(t *testing.T) {
	fn := func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return env, nil
	}

	node := NewFuncNode("func-1", fn).WithKind(NodeKindTool)

	if node.Kind() != NodeKindTool {
		t.Errorf("FuncNode.Kind() = %v, want %v", node.Kind(), NodeKindTool)
	}
}

func TestFuncNode_Error(t *testing.T) {
	expectedErr := errors.New("function error")
	fn := func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, expectedErr
	}

	node := NewFuncNode("func-err", fn)
	env := NewEnvelope()

	_, err := node.Run(context.Background(), env)

	if err != expectedErr {
		t.Errorf("FuncNode.Run() error = %v, want %v", err, expectedErr)
	}
}

func TestFuncNode_NilFunction(t *testing.T) {
	node := NewFuncNode("func-nil", nil)
	env := NewEnvelope()

	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("FuncNode.Run() with nil fn error = %v", err)
	}
	if result != env {
		t.Error("FuncNode.Run() with nil fn should return same envelope")
	}
}

func TestFuncNode_ContextCancellation(t *testing.T) {
	fn := func(ctx context.Context, env *Envelope) (*Envelope, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return env, nil
		}
	}

	node := NewFuncNode("func-ctx", fn)
	env := NewEnvelope()

	// Test with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, env)

	if err != context.Canceled {
		t.Errorf("FuncNode.Run() with canceled ctx error = %v, want %v", err, context.Canceled)
	}
}

func TestNode_InterfaceCompliance(t *testing.T) {
	// These compile-time checks ensure interface compliance
	var _ Node = (*NoopNode)(nil)
	var _ Node = (*FuncNode)(nil)
}

func TestFuncNode_ModifiesEnvelope(t *testing.T) {
	node := NewFuncNode("modifier", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		// Clone and modify to show pattern
		result := env.Clone()
		result.SetVar("modified", true)
		result.AppendMessage(Message{Role: "assistant", Content: "processed"})
		return result, nil
	})

	env := NewEnvelope()
	env.SetVar("original", true)

	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("Run() error = %v", err)
	}

	// Original should be unchanged
	if _, ok := env.GetVar("modified"); ok {
		t.Error("Original envelope was modified")
	}

	// Result should have both vars
	if v, ok := result.GetVar("original"); !ok || v != true {
		t.Error("Result missing original var")
	}
	if v, ok := result.GetVar("modified"); !ok || v != true {
		t.Error("Result missing modified var")
	}
	if len(result.Messages) != 1 {
		t.Errorf("Result has %d messages, want 1", len(result.Messages))
	}
}

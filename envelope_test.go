package petalflow

import (
	"testing"
)

func TestNewEnvelope(t *testing.T) {
	env := NewEnvelope()

	if env == nil {
		t.Fatal("NewEnvelope() returned nil")
	}
	if env.Vars == nil {
		t.Error("NewEnvelope().Vars is nil")
	}
	if env.Artifacts == nil {
		t.Error("NewEnvelope().Artifacts is nil")
	}
	if env.Messages == nil {
		t.Error("NewEnvelope().Messages is nil")
	}
	if env.Errors == nil {
		t.Error("NewEnvelope().Errors is nil")
	}
	if env.Trace.Started.IsZero() {
		t.Error("NewEnvelope().Trace.Started is zero")
	}
}

func TestEnvelope_Clone(t *testing.T) {
	original := NewEnvelope()
	original.Input = "test input"
	original.SetVar("key1", "value1")
	original.SetVar("key2", 42)
	original.AppendMessage(Message{Role: "user", Content: "hello"})
	original.AppendArtifact(Artifact{ID: "art1", Type: "document"})
	original.Trace.RunID = "run-123"

	clone := original.Clone()

	// Verify clone is not the same instance
	if clone == original {
		t.Error("Clone() returned same instance")
	}

	// Verify values are copied
	if clone.Input != original.Input {
		t.Error("Clone().Input not equal to original")
	}
	if clone.Trace.RunID != original.Trace.RunID {
		t.Error("Clone().Trace.RunID not equal to original")
	}

	// Verify maps are independent
	clone.SetVar("key1", "modified")
	if v, _ := original.GetVar("key1"); v != "value1" {
		t.Error("Modifying clone affected original Vars")
	}

	// Verify slices are independent
	clone.Messages[0].Content = "modified"
	if original.Messages[0].Content == "modified" {
		t.Error("Modifying clone affected original Messages")
	}
}

func TestEnvelope_Clone_Nil(t *testing.T) {
	var env *Envelope
	clone := env.Clone()

	if clone != nil {
		t.Error("Clone() of nil should return nil")
	}
}

func TestEnvelope_GetVar(t *testing.T) {
	env := NewEnvelope()
	env.SetVar("exists", "value")

	// Test existing var
	val, ok := env.GetVar("exists")
	if !ok {
		t.Error("GetVar() should return true for existing var")
	}
	if val != "value" {
		t.Errorf("GetVar() = %v, want 'value'", val)
	}

	// Test non-existing var
	val, ok = env.GetVar("missing")
	if ok {
		t.Error("GetVar() should return false for missing var")
	}
	if val != nil {
		t.Errorf("GetVar() for missing var = %v, want nil", val)
	}
}

func TestEnvelope_GetVarString(t *testing.T) {
	env := NewEnvelope()
	env.SetVar("string", "hello")
	env.SetVar("int", 42)

	// Test string var
	if got := env.GetVarString("string"); got != "hello" {
		t.Errorf("GetVarString('string') = %v, want 'hello'", got)
	}

	// Test non-string var
	if got := env.GetVarString("int"); got != "" {
		t.Errorf("GetVarString('int') = %v, want ''", got)
	}

	// Test missing var
	if got := env.GetVarString("missing"); got != "" {
		t.Errorf("GetVarString('missing') = %v, want ''", got)
	}
}

func TestEnvelope_GetVarNested(t *testing.T) {
	env := NewEnvelope()
	env.SetVar("response", map[string]any{
		"data": map[string]any{
			"id":   "123",
			"name": "test",
		},
		"status": "ok",
	})

	// Test nested access
	val, ok := env.GetVarNested("response.data.id")
	if !ok {
		t.Error("GetVarNested() should return true for existing nested var")
	}
	if val != "123" {
		t.Errorf("GetVarNested('response.data.id') = %v, want '123'", val)
	}

	// Test single level
	val, ok = env.GetVarNested("response.status")
	if !ok || val != "ok" {
		t.Errorf("GetVarNested('response.status') = %v, %v", val, ok)
	}

	// Test missing path
	_, ok = env.GetVarNested("response.missing.path")
	if ok {
		t.Error("GetVarNested() should return false for missing path")
	}

	// Test invalid path (non-map intermediate)
	env.SetVar("simple", "value")
	_, ok = env.GetVarNested("simple.nested")
	if ok {
		t.Error("GetVarNested() should return false for non-map intermediate")
	}
}

func TestEnvelope_SetVar(t *testing.T) {
	env := &Envelope{} // Start with nil Vars

	env.SetVar("key", "value")

	if env.Vars == nil {
		t.Error("SetVar() should initialize Vars if nil")
	}
	if v, ok := env.Vars["key"]; !ok || v != "value" {
		t.Error("SetVar() did not set the value correctly")
	}
}

func TestEnvelope_AppendArtifact(t *testing.T) {
	env := NewEnvelope()

	env.AppendArtifact(Artifact{ID: "a1", Type: "doc"})
	env.AppendArtifact(Artifact{ID: "a2", Type: "chunk"})

	if len(env.Artifacts) != 2 {
		t.Errorf("len(Artifacts) = %v, want 2", len(env.Artifacts))
	}
	if env.Artifacts[0].ID != "a1" {
		t.Error("First artifact ID incorrect")
	}
	if env.Artifacts[1].ID != "a2" {
		t.Error("Second artifact ID incorrect")
	}
}

func TestEnvelope_AppendMessage(t *testing.T) {
	env := NewEnvelope()

	env.AppendMessage(Message{Role: "user", Content: "hello"})
	env.AppendMessage(Message{Role: "assistant", Content: "hi"})

	if len(env.Messages) != 2 {
		t.Errorf("len(Messages) = %v, want 2", len(env.Messages))
	}
	if env.Messages[0].Role != "user" {
		t.Error("First message role incorrect")
	}
}

func TestEnvelope_AppendError(t *testing.T) {
	env := NewEnvelope()

	if env.HasErrors() {
		t.Error("HasErrors() should return false initially")
	}

	env.AppendError(NodeError{NodeID: "n1", Message: "error 1"})

	if !env.HasErrors() {
		t.Error("HasErrors() should return true after AppendError")
	}
	if len(env.Errors) != 1 {
		t.Errorf("len(Errors) = %v, want 1", len(env.Errors))
	}
}

func TestEnvelope_GetArtifactsByType(t *testing.T) {
	env := NewEnvelope()
	env.AppendArtifact(Artifact{ID: "a1", Type: "document"})
	env.AppendArtifact(Artifact{ID: "a2", Type: "chunk"})
	env.AppendArtifact(Artifact{ID: "a3", Type: "document"})

	docs := env.GetArtifactsByType("document")
	if len(docs) != 2 {
		t.Errorf("GetArtifactsByType('document') returned %v items, want 2", len(docs))
	}

	chunks := env.GetArtifactsByType("chunk")
	if len(chunks) != 1 {
		t.Errorf("GetArtifactsByType('chunk') returned %v items, want 1", len(chunks))
	}

	missing := env.GetArtifactsByType("missing")
	if len(missing) != 0 {
		t.Errorf("GetArtifactsByType('missing') returned %v items, want 0", len(missing))
	}
}

func TestEnvelope_GetLastMessage(t *testing.T) {
	env := NewEnvelope()

	// Empty messages
	if msg := env.GetLastMessage(); msg != nil {
		t.Error("GetLastMessage() should return nil for empty messages")
	}

	env.AppendMessage(Message{Role: "user", Content: "first"})
	env.AppendMessage(Message{Role: "assistant", Content: "last"})

	msg := env.GetLastMessage()
	if msg == nil {
		t.Fatal("GetLastMessage() returned nil")
	}
	if msg.Content != "last" {
		t.Errorf("GetLastMessage().Content = %v, want 'last'", msg.Content)
	}
}

func TestEnvelope_GetMessagesByRole(t *testing.T) {
	env := NewEnvelope()
	env.AppendMessage(Message{Role: "user", Content: "q1"})
	env.AppendMessage(Message{Role: "assistant", Content: "a1"})
	env.AppendMessage(Message{Role: "user", Content: "q2"})

	userMsgs := env.GetMessagesByRole("user")
	if len(userMsgs) != 2 {
		t.Errorf("GetMessagesByRole('user') returned %v items, want 2", len(userMsgs))
	}

	assistantMsgs := env.GetMessagesByRole("assistant")
	if len(assistantMsgs) != 1 {
		t.Errorf("GetMessagesByRole('assistant') returned %v items, want 1", len(assistantMsgs))
	}
}

func TestEnvelope_FluentMethods(t *testing.T) {
	env := NewEnvelope().
		WithInput("test input").
		WithVar("key1", "value1").
		WithVar("key2", 42).
		WithTrace(TraceInfo{RunID: "run-123"})

	if env.Input != "test input" {
		t.Error("WithInput() did not set input")
	}
	if v, _ := env.GetVar("key1"); v != "value1" {
		t.Error("WithVar() did not set key1")
	}
	if v, _ := env.GetVar("key2"); v != 42 {
		t.Error("WithVar() did not set key2")
	}
	if env.Trace.RunID != "run-123" {
		t.Error("WithTrace() did not set trace")
	}
}

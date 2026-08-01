package server

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
)

func TestEnvelopeToJSON_NilEnvelope(t *testing.T) {
	result := EnvelopeToJSON(nil)

	if result.Vars == nil {
		t.Fatal("expected non-nil Vars map for nil envelope")
	}
	if len(result.Vars) != 0 {
		t.Fatalf("expected empty Vars map, got %d entries", len(result.Vars))
	}
	if result.Messages != nil {
		t.Fatal("expected nil Messages for nil envelope")
	}
	if result.Artifacts != nil {
		t.Fatal("expected nil Artifacts for nil envelope")
	}
	if result.Trace != nil {
		t.Fatal("expected nil Trace for nil envelope")
	}
}

func TestEnvelopeToJSON_EmptyEnvelope(t *testing.T) {
	env := core.NewEnvelope()
	// Clear the auto-set Started time so trace is not emitted.
	env.Trace = core.TraceInfo{}

	result := EnvelopeToJSON(env)

	if result.Vars == nil {
		t.Fatal("expected non-nil Vars map")
	}
	if len(result.Vars) != 0 {
		t.Fatalf("expected empty Vars, got %d entries", len(result.Vars))
	}
	if len(result.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(result.Messages))
	}
	if len(result.Artifacts) != 0 {
		t.Fatalf("expected no artifacts, got %d", len(result.Artifacts))
	}
	if result.Trace != nil {
		t.Fatal("expected nil Trace for envelope with no RunID")
	}
}

func TestEnvelopeToJSON_WithVars(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.SetVar("greeting", "hello")
	env.SetVar("count", 42)
	env.SetVar("active", true)

	result := EnvelopeToJSON(env)

	tests := []struct {
		key  string
		want any
	}{
		{"greeting", "hello"},
		{"count", 42},
		{"active", true},
	}

	for _, tt := range tests {
		v, ok := result.Vars[tt.key]
		if !ok {
			t.Errorf("expected var %q to be present", tt.key)
			continue
		}
		if v != tt.want {
			t.Errorf("var %q = %v, want %v", tt.key, v, tt.want)
		}
	}
}

func TestEnvelopeToJSON_WithMessages(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.AppendMessage(core.Message{Role: "user", Content: "Hello there"})
	env.AppendMessage(core.Message{Role: "assistant", Content: "Hi!", Name: "bot"})
	env.AppendMessage(core.Message{Role: "system", Content: "Be helpful"})

	result := EnvelopeToJSON(env)

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}

	tests := []struct {
		idx     int
		role    string
		content string
		name    string
	}{
		{0, "user", "Hello there", ""},
		{1, "assistant", "Hi!", "bot"},
		{2, "system", "Be helpful", ""},
	}

	for _, tt := range tests {
		msg := result.Messages[tt.idx]
		if msg.Role != tt.role {
			t.Errorf("message[%d].Role = %q, want %q", tt.idx, msg.Role, tt.role)
		}
		if msg.Content != tt.content {
			t.Errorf("message[%d].Content = %q, want %q", tt.idx, msg.Content, tt.content)
		}
		if msg.Name != tt.name {
			t.Errorf("message[%d].Name = %q, want %q", tt.idx, msg.Name, tt.name)
		}
	}
}

func TestEnvelopeToJSON_WithArtifacts(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}

	// Text artifact.
	env.AppendArtifact(core.Artifact{
		ID:       "art-1",
		Type:     "document",
		MimeType: "text/plain",
		Text:     "Some text content",
	})

	// Binary artifact.
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header bytes
	env.AppendArtifact(core.Artifact{
		ID:       "art-2",
		Type:     "file",
		MimeType: "image/png",
		Bytes:    binaryData,
	})

	// Artifact with URI.
	env.AppendArtifact(core.Artifact{
		ID:   "art-3",
		Type: "citation",
		URI:  "https://example.com/doc.pdf",
	})

	result := EnvelopeToJSON(env)

	if len(result.Artifacts) != 3 {
		t.Fatalf("expected 3 artifacts, got %d", len(result.Artifacts))
	}

	// Check text artifact.
	a0 := result.Artifacts[0]
	if a0.ID != "art-1" {
		t.Errorf("artifact[0].ID = %q, want %q", a0.ID, "art-1")
	}
	if a0.Name != "art-1" {
		t.Errorf("artifact[0].Name = %q, want %q (should fallback to ID)", a0.Name, "art-1")
	}
	if a0.Type != "document" {
		t.Errorf("artifact[0].Type = %q, want %q", a0.Type, "document")
	}
	if a0.MimeType != "text/plain" {
		t.Errorf("artifact[0].MimeType = %q, want %q", a0.MimeType, "text/plain")
	}
	if a0.Text != "Some text content" {
		t.Errorf("artifact[0].Text = %q, want %q", a0.Text, "Some text content")
	}
	if a0.Content != "" {
		t.Errorf("artifact[0].Content should be empty for text artifact, got %q", a0.Content)
	}

	// Check binary artifact has base64-encoded content.
	a1 := result.Artifacts[1]
	if a1.ID != "art-2" {
		t.Errorf("artifact[1].ID = %q, want %q", a1.ID, "art-2")
	}
	if a1.Content == "" {
		t.Fatal("artifact[1].Content should have base64-encoded data")
	}
	expectedBase64 := "iVBORw=="
	if a1.Content != expectedBase64 {
		t.Errorf("artifact[1].Content = %q, want %q", a1.Content, expectedBase64)
	}

	// Check URI artifact.
	a2 := result.Artifacts[2]
	if a2.URI != "https://example.com/doc.pdf" {
		t.Errorf("artifact[2].URI = %q, want %q", a2.URI, "https://example.com/doc.pdf")
	}
}

func TestEnvelopeToJSON_WithTrace(t *testing.T) {
	env := core.NewEnvelope()
	started := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	env.Trace = core.TraceInfo{
		RunID:   "run-abc-123",
		TraceID: "trace-xyz-789",
		Started: started,
	}

	result := EnvelopeToJSON(env)

	if result.Trace == nil {
		t.Fatal("expected non-nil Trace")
	}
	if result.Trace.RunID != "run-abc-123" {
		t.Errorf("Trace.RunID = %q, want %q", result.Trace.RunID, "run-abc-123")
	}
	if result.Trace.TraceID != "trace-xyz-789" {
		t.Errorf("Trace.TraceID = %q, want %q", result.Trace.TraceID, "trace-xyz-789")
	}

	expectedTime := "2026-02-07T12:00:00Z"
	if result.Trace.StartedAt != expectedTime {
		t.Errorf("Trace.StartedAt = %q, want %q", result.Trace.StartedAt, expectedTime)
	}
}

func TestEnvelopeFromJSON(t *testing.T) {
	data := map[string]any{
		"prompt":      "Summarize this",
		"temperature": 0.7,
		"max_tokens":  100,
	}

	env := EnvelopeFromJSON(data)

	if env == nil {
		t.Fatal("expected non-nil envelope")
	}

	tests := []struct {
		key  string
		want any
	}{
		{"prompt", "Summarize this"},
		{"temperature", 0.7},
		{"max_tokens", 100},
	}

	for _, tt := range tests {
		v, ok := env.GetVar(tt.key)
		if !ok {
			t.Errorf("expected var %q to be present", tt.key)
			continue
		}
		if v != tt.want {
			t.Errorf("var %q = %v, want %v", tt.key, v, tt.want)
		}
	}
}

func TestEnvelopeFromJSON_NilInput(t *testing.T) {
	env := EnvelopeFromJSON(nil)

	if env == nil {
		t.Fatal("expected non-nil envelope from nil input")
	}
	if env.Vars == nil {
		t.Fatal("expected non-nil Vars map")
	}
	if len(env.Vars) != 0 {
		t.Fatalf("expected empty Vars map, got %d entries", len(env.Vars))
	}
}

func TestRoundTrip(t *testing.T) {
	// Create an envelope with vars.
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.SetVar("input", "test data")
	env.SetVar("count", 42)
	env.SetVar("nested", map[string]any{"key": "value"})

	// Convert to JSON representation.
	j := EnvelopeToJSON(env)

	// Verify vars match the original.
	if len(j.Vars) != len(env.Vars) {
		t.Fatalf("var count mismatch: got %d, want %d", len(j.Vars), len(env.Vars))
	}

	for k, want := range env.Vars {
		got, ok := j.Vars[k]
		if !ok {
			t.Errorf("missing var %q in JSON result", k)
			continue
		}

		// For nested maps, compare JSON serializations.
		if _, isMap := want.(map[string]any); isMap {
			wantBytes, _ := json.Marshal(want)
			gotBytes, _ := json.Marshal(got)
			if string(wantBytes) != string(gotBytes) {
				t.Errorf("var %q = %s, want %s", k, gotBytes, wantBytes)
			}
			continue
		}

		if got != want {
			t.Errorf("var %q = %v, want %v", k, got, want)
		}
	}
}

func TestEnvelopeToJSON_WithErrors(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.AppendError(core.NodeError{
		NodeID:  "tool-1",
		Kind:    core.NodeKindTool,
		Message: "tool failed",
		Attempt: 2,
		At:      time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		Details: map[string]any{"tool": "search"},
		Cause:   errors.New("connection refused"),
	})

	result := EnvelopeToJSON(env)

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.NodeID != "tool-1" {
		t.Errorf("error.NodeID = %q, want %q", e.NodeID, "tool-1")
	}
	if e.Kind != "tool" {
		t.Errorf("error.Kind = %q, want %q", e.Kind, "tool")
	}
	if e.Message != "tool failed" {
		t.Errorf("error.Message = %q, want %q", e.Message, "tool failed")
	}
	if e.Attempt != 2 {
		t.Errorf("error.Attempt = %d, want %d", e.Attempt, 2)
	}
	if e.At != "2026-02-07T12:00:00Z" {
		t.Errorf("error.At = %q, want %q", e.At, "2026-02-07T12:00:00Z")
	}
	if e.Details["tool"] != "search" {
		t.Errorf("error.Details[tool] = %v, want %q", e.Details["tool"], "search")
	}
	if e.Cause != "connection refused" {
		t.Errorf("error.Cause = %q, want %q", e.Cause, "connection refused")
	}
}

func TestEnvelopeToJSON_ErrorsMarshalToJSON(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.AppendError(core.NodeError{
		NodeID:  "llm-1",
		Kind:    core.NodeKindLLM,
		Message: "boom",
	})

	data, err := json.Marshal(EnvelopeToJSON(env))
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	errs, ok := decoded["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors array in JSON output, got %T", decoded["errors"])
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error entry, got %d", len(errs))
	}
	entry, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("error entry should be a JSON object, got %T", errs[0])
	}
	if entry["node_id"] != "llm-1" {
		t.Errorf("errors[0].node_id = %v, want %q", entry["node_id"], "llm-1")
	}
	if entry["message"] != "boom" {
		t.Errorf("errors[0].message = %v, want %q", entry["message"], "boom")
	}
}

func TestEnvelopeToJSON_OmitsErrorsWhenNone(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}

	data, err := json.Marshal(EnvelopeToJSON(env))
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if _, ok := decoded["errors"]; ok {
		t.Error("errors key should be omitted when there are no errors")
	}
}

func TestEnvelopeToJSON_PreservesMessageMeta(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.AppendMessage(core.Message{
		Role:    "assistant",
		Content: "hi",
		Meta:    map[string]any{"model": "gpt", "provider": "openai"},
	})

	result := EnvelopeToJSON(env)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Meta["model"] != "gpt" {
		t.Errorf("message.Meta[model] = %v, want %q", result.Messages[0].Meta["model"], "gpt")
	}
	if result.Messages[0].Meta["provider"] != "openai" {
		t.Errorf("message.Meta[provider] = %v, want %q", result.Messages[0].Meta["provider"], "openai")
	}
}

func TestEnvelopeToJSON_PreservesArtifactMeta(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.AppendArtifact(core.Artifact{
		ID:   "a1",
		Type: "json",
		Meta: map[string]any{"confidence": 0.9},
	})

	result := EnvelopeToJSON(env)

	if len(result.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(result.Artifacts))
	}
	if result.Artifacts[0].Meta["confidence"] != 0.9 {
		t.Errorf("artifact.Meta[confidence] = %v, want %v", result.Artifacts[0].Meta["confidence"], 0.9)
	}
}

func TestEnvelopeToJSON_PreservesInput(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{}
	env.Input = "primary payload"

	result := EnvelopeToJSON(env)

	if result.Input != "primary payload" {
		t.Errorf("Input = %v, want %q", result.Input, "primary payload")
	}
}

func TestEnvelopeToJSON_PreservesTraceParentAndSpan(t *testing.T) {
	env := core.NewEnvelope()
	env.Trace = core.TraceInfo{
		RunID:    "run-1",
		ParentID: "parent-1",
		SpanID:   "span-1",
	}

	result := EnvelopeToJSON(env)

	if result.Trace == nil {
		t.Fatal("expected non-nil Trace")
	}
	if result.Trace.ParentID != "parent-1" {
		t.Errorf("Trace.ParentID = %q, want %q", result.Trace.ParentID, "parent-1")
	}
	if result.Trace.SpanID != "span-1" {
		t.Errorf("Trace.SpanID = %q, want %q", result.Trace.SpanID, "span-1")
	}
}

func TestEnvelopeToJSON_JSONMarshal(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("prompt", "hello world")
	env.AppendMessage(core.Message{Role: "user", Content: "hi"})
	env.AppendArtifact(core.Artifact{
		ID:   "a1",
		Type: "document",
		Text: "doc content",
	})
	env.Trace = core.TraceInfo{
		RunID:   "run-001",
		TraceID: "trace-001",
		Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	j := EnvelopeToJSON(env)

	data, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Verify it is valid JSON by unmarshaling back.
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Check expected top-level keys.
	expectedKeys := []string{"vars", "messages", "artifacts", "trace"}
	for _, key := range expectedKeys {
		if _, ok := decoded[key]; !ok {
			t.Errorf("expected key %q in JSON output", key)
		}
	}

	// Verify vars content.
	vars, ok := decoded["vars"].(map[string]any)
	if !ok {
		t.Fatal("vars should be a JSON object")
	}
	if vars["prompt"] != "hello world" {
		t.Errorf("vars.prompt = %v, want %q", vars["prompt"], "hello world")
	}

	// Verify messages array.
	msgs, ok := decoded["messages"].([]any)
	if !ok {
		t.Fatal("messages should be a JSON array")
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// Verify trace object.
	trace, ok := decoded["trace"].(map[string]any)
	if !ok {
		t.Fatal("trace should be a JSON object")
	}
	if trace["run_id"] != "run-001" {
		t.Errorf("trace.run_id = %v, want %q", trace["run_id"], "run-001")
	}
	if trace["started_at"] != "2026-01-01T00:00:00Z" {
		t.Errorf("trace.started_at = %v, want %q", trace["started_at"], "2026-01-01T00:00:00Z")
	}
}

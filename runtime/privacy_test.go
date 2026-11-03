package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/budget"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

func TestClassifyPayloadField(t *testing.T) {
	tests := []struct {
		field string
		want  PayloadClass
	}{
		{"model", PayloadMetadata},
		{"input_tokens", PayloadMetadata},
		{"total_tokens", PayloadMetadata},
		{"system_prompt", PayloadContent},
		{"completion", PayloadContent},
		{"messages", PayloadContent},
		{"text", PayloadContent},
		{"delta", PayloadContent},
		{"arguments", PayloadContent},
		{"data_preview", PayloadContent},
		{"inputs", PayloadContent},
		{"password", PayloadSecret},
		{"db_password", PayloadSecret},
		{"api_key", PayloadSecret},
		{"apikey", PayloadSecret},
		{"client_secret", PayloadSecret},
		{"authorization", PayloadSecret},
		{"auth_token", PayloadSecret},
		{"token", PayloadSecret},
		{"credential", PayloadSecret},
	}
	for _, tt := range tests {
		if got := ClassifyPayloadField(tt.field); got != tt.want {
			t.Errorf("ClassifyPayloadField(%q) = %v, want %v", tt.field, got, tt.want)
		}
	}
}

func TestSanitizeEventDefaultDropsContentAndSecrets(t *testing.T) {
	e := NewEvent(EventLLMCall, "run-1").
		WithPayload("model", "gpt-4").
		WithPayload("system_prompt", "You are helpful.").
		WithPayload("messages", `[{"role":"user","content":"hello"}]`).
		WithPayload("input_text", "hello world").
		WithPayload("json_schema", `{"type":"object"}`).
		WithPayload("api_key", "sk-abcdef123456").
		WithPayload("max_tokens", 100)

	out := SanitizeEvent(e, ContentCapture{})

	if out.Payload["model"] != "gpt-4" {
		t.Errorf("model = %v, want kept", out.Payload["model"])
	}
	if out.Payload["max_tokens"] != 100 {
		t.Errorf("max_tokens = %v, want kept", out.Payload["max_tokens"])
	}
	for _, field := range []string{"system_prompt", "messages", "input_text", "json_schema", "api_key"} {
		if _, present := out.Payload[field]; present {
			t.Errorf("payload field %q survived default sanitization", field)
		}
	}
	if out.Payload["system_prompt_bytes"] != len("You are helpful.") {
		t.Errorf("system_prompt_bytes = %v, want %d", out.Payload["system_prompt_bytes"], len("You are helpful."))
	}
	if out.Payload["messages_count"] != 1 {
		t.Errorf("messages_count = %v, want 1", out.Payload["messages_count"])
	}
	if out.Payload["input_text_bytes"] != len("hello world") {
		t.Errorf("input_text_bytes = %v, want %d", out.Payload["input_text_bytes"], len("hello world"))
	}
}

func TestSanitizeEventToolArgumentsDescriptor(t *testing.T) {
	e := NewEvent(EventToolCall, "run-1").
		WithPayload("tool_name", "lookup").
		WithPayload("arguments", map[string]any{
			"query":   "find users",
			"limit":   10,
			"deep":    map[string]any{"nested": true},
			"api_key": "sk-abcdef123456",
		})

	out := SanitizeEvent(e, ContentCapture{})

	shape, ok := out.Payload["arguments_shape"].(map[string]string)
	if !ok {
		t.Fatalf("arguments_shape = %#v, want map[string]string", out.Payload["arguments_shape"])
	}
	if shape["query"] != "string" || shape["limit"] != "number" || shape["deep"] != "object" {
		t.Errorf("arguments_shape = %+v", shape)
	}
	// Key names appear in the shape (names are metadata); the secret
	// VALUE never does.
	if shape["api_key"] != "string" {
		t.Errorf("arguments_shape missing api_key kind: %+v", shape)
	}
	for _, kind := range shape {
		if strings.Contains(kind, "sk-") {
			t.Errorf("arguments_shape leaks secret value: %+v", shape)
		}
	}
	for key := range out.Payload {
		if strings.Contains(key, "arguments") && key != "arguments_shape" {
			t.Errorf("unexpected arguments payload key %q", key)
		}
	}
}

func TestSanitizeEventCapturesBoundedAndRedacted(t *testing.T) {
	long := strings.Repeat("a", 300) + " sk-abcdef123456789 tail"
	e := NewEvent(EventLLMResponse, "run-1").
		WithPayload("completion", long)

	capture := ContentCapture{Enabled: true, MaxBytes: 64}
	out := SanitizeEvent(e, capture)

	completion, ok := out.Payload["completion"].(string)
	if !ok {
		t.Fatalf("completion = %#v, want captured string", out.Payload["completion"])
	}
	if len(completion) > 64 {
		t.Errorf("captured completion exceeds bound: %d bytes", len(completion))
	}
	if strings.Contains(completion, "sk-abcdef123456789") {
		t.Errorf("captured completion leaks credential: %q", completion)
	}
}

func TestSanitizeEventCaptureMasksSensitiveMapFields(t *testing.T) {
	e := NewEvent(EventToolCall, "run-1").
		WithPayload("arguments", map[string]any{
			"password": "hunter2",
			"api_key":  "sk-abcdef123456",
			"note":     "bearer abcdefghijklmnop",
			"keep":     "plain value",
		})

	out := SanitizeEvent(e, ContentCapture{Enabled: true})

	args, ok := out.Payload["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments = %#v, want captured map", out.Payload["arguments"])
	}
	if args["password"] != RedactedPlaceholder {
		t.Errorf("password = %v, want masked", args["password"])
	}
	if args["api_key"] != RedactedPlaceholder {
		t.Errorf("api_key = %v, want masked", args["api_key"])
	}
	note, _ := args["note"].(string)
	if strings.Contains(note, "abcdefghijklmnop") {
		t.Errorf("note leaks bearer token: %q", note)
	}
	if args["keep"] != "plain value" {
		t.Errorf("keep = %v, want unchanged", args["keep"])
	}
}

func TestSanitizeEventOversizedMapDegradesToDescriptor(t *testing.T) {
	args := make(map[string]any)
	for i := 0; i < 100; i++ {
		args[string(rune('a'+i%26))+strings.Repeat("x", 100)] = strings.Repeat("v", 100)
	}
	e := NewEvent(EventToolCall, "run-1").
		WithPayload("tool_name", "bulk").
		WithPayload("arguments", args)

	out := SanitizeEvent(e, ContentCapture{Enabled: true, MaxBytes: 256})

	if _, present := out.Payload["arguments"]; present {
		t.Errorf("oversized arguments captured despite bound")
	}
	shape, ok := out.Payload["arguments_shape"].(map[string]string)
	if !ok {
		t.Fatalf("arguments_shape = %#v, want descriptor", out.Payload["arguments_shape"])
	}
	if len(shape) > descriptorKeyLimit {
		t.Errorf("arguments_shape has %d keys, want <= %d", len(shape), descriptorKeyLimit)
	}
}

func TestSanitizeEventScrubsErrorText(t *testing.T) {
	e := NewEvent(EventNodeFailed, "run-1").
		WithNode("n1", "llm").
		WithPayload("error", "provider 500: auth failed for key sk-abcdef123456789").
		WithPayload("error_code", "error")

	out := SanitizeEvent(e, ContentCapture{})

	msg, _ := out.Payload["error"].(string)
	if strings.Contains(msg, "sk-abcdef123456789") {
		t.Errorf("scrubbed error leaks credential: %q", msg)
	}
	if !strings.Contains(msg, "provider 500") {
		t.Errorf("scrubbed error lost diagnostic context: %q", msg)
	}
	if out.Payload["error_code"] != "error" {
		t.Errorf("error_code = %v, want kept", out.Payload["error_code"])
	}
}

func TestSanitizeEventLeavesMetadataEventsUntouched(t *testing.T) {
	e := NewEvent(EventNodeFinished, "run-1").
		WithNode("n1", "tool").
		WithElapsed(time.Millisecond)
	original := NewEvent(EventNodeFinished, "run-1").
		WithNode("n1", "tool").
		WithElapsed(time.Millisecond)

	out := SanitizeEvent(e, ContentCapture{})
	if len(out.Payload) != len(original.Payload) {
		t.Errorf("payload modified: %+v", out.Payload)
	}
}

func TestWithContentPayload(t *testing.T) {
	// Default: descriptor only.
	e := NewEvent(EventNodeOutputFinal, "run-1")
	e = e.WithContentPayload("text", "Hello!", ContentCapture{})
	if _, present := e.Payload["text"]; present {
		t.Errorf("text present by default: %+v", e.Payload)
	}
	if e.Payload["text_bytes"] != len("Hello!") {
		t.Errorf("text_bytes = %v", e.Payload["text_bytes"])
	}

	// Opt-in: captured value.
	e = NewEvent(EventNodeOutputFinal, "run-1")
	e = e.WithContentPayload("text", "Hello!", ContentCapture{Enabled: true})
	if e.Payload["text"] != "Hello!" {
		t.Errorf("text = %v, want captured", e.Payload["text"])
	}
	if _, present := e.Payload["text_bytes"]; present {
		t.Errorf("text_bytes emitted alongside captured text: %+v", e.Payload)
	}
}

func TestContentCaptureTruncationIsRuneSafe(t *testing.T) {
	capture := ContentCapture{Enabled: true, MaxBytes: 16}
	multibyte := "héllo wörld ünïcode"

	e := NewEvent(EventNodeOutputFinal, "run-1").WithContentPayload("text", multibyte, capture)
	text, _ := e.Payload["text"].(string)
	if len(text) > 16 {
		t.Errorf("captured text exceeds bound: %d bytes", len(text))
	}
	for _, r := range text {
		if r == 0xFFFD {
			t.Errorf("captured text contains replacement char: %q", text)
		}
	}
}

func TestErrorCodeFor(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{ErrRunCanceled, "canceled"},
		{ErrMaxHopsExceeded, "max_hops_exceeded"},
		{errors.New("boom"), "error"},
	}
	for _, tt := range tests {
		if got := ErrorCodeFor(tt.err); got != tt.want {
			t.Errorf("ErrorCodeFor(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}

	ledger, err := budget.NewLedger(budget.Ceiling{MaxTotalTokens: 1})
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	if _, reserveErr := ledger.Reserve("run", 10, 0); reserveErr == nil {
		t.Fatal("expected budget rejection")
	} else if got := ErrorCodeFor(reserveErr); got != "budget_exceeded" {
		t.Errorf("ErrorCodeFor(budget rejection) = %q, want budget_exceeded", got)
	}
}

func TestContentCaptureFromContextDefaultDeny(t *testing.T) {
	capture, ok := ContentCaptureFromContext(context.Background())
	if ok {
		t.Error("unexpected policy in bare context")
	}
	if capture.Enabled {
		t.Error("default policy must be metadata-only")
	}

	set := ContentCapture{Enabled: true, MaxBytes: 128}
	capture, ok = ContentCaptureFromContext(ContextWithContentCapture(context.Background(), set))
	if !ok {
		t.Error("policy missing from context")
	}
	if !capture.Enabled || capture.MaxBytes != 128 {
		t.Errorf("capture = %+v, want %+v", capture, set)
	}
}

// TestRuntimeFailureEventsScrubbedAndCoded drives a full runtime run
// whose node fails with a credential-bearing error: the node.failed and
// run.finished events must carry a typed error code and a scrubbed
// message, never the raw error.
func TestRuntimeFailureEventsScrubbedAndCoded(t *testing.T) {
	g := graph.NewGraph("fail-privacy")
	g.AddNode(core.NewFuncNode("fail", func(context.Context, *core.Envelope) (*core.Envelope, error) {
		return nil, errors.New("upstream 500 at https://api.example.com/v1 with api_key=sk-abcdef123456789")
	}))
	g.SetEntry("fail")

	var failures, finishes []Event
	opts := DefaultRunOptions()
	opts.EventHandler = func(e Event) {
		switch e.Kind {
		case EventNodeFailed:
			failures = append(failures, e)
		case EventRunFinished:
			finishes = append(finishes, e)
		}
	}

	_, _ = NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)

	if len(failures) != 1 {
		t.Fatalf("node.failed events = %d, want 1", len(failures))
	}
	failed := failures[0]
	msg, _ := failed.Payload["error"].(string)
	if strings.Contains(msg, "sk-abcdef123456789") {
		t.Errorf("node.failed error leaks credential: %q", msg)
	}
	if !strings.Contains(msg, "upstream 500") {
		t.Errorf("node.failed error lost context: %q", msg)
	}
	if failed.Payload["error_code"] != "error" {
		t.Errorf("node.failed error_code = %v, want \"error\"", failed.Payload["error_code"])
	}

	if len(finishes) != 1 {
		t.Fatalf("run.finished events = %d, want 1", len(finishes))
	}
	finished := finishes[0]
	msg, _ = finished.Payload["error"].(string)
	if strings.Contains(msg, "sk-abcdef123456789") {
		t.Errorf("run.finished error leaks credential: %q", msg)
	}
	if finished.Payload["error_code"] != "error" {
		t.Errorf("run.finished error_code = %v, want \"error\"", finished.Payload["error_code"])
	}
}

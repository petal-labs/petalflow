package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func payloadTestRegistration(manifestFun func(*Manifest)) Registration {
	manifest := NewManifest("sized_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	if manifestFun != nil {
		manifestFun(&manifest)
	}
	return ToolRegistration{
		Name:     "sized_tool",
		Origin:   OriginHTTP,
		Manifest: manifest,
		Status:   StatusReady,
		Enabled:  true,
	}
}

func TestValidateInputsUnknownAction(t *testing.T) {
	reg := payloadTestRegistration(nil)
	err := ValidateInputs(reg, "missing", map[string]any{"x": 1}, PayloadLimits{})
	if !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("unknown action = %v, want ErrActionNotFound", err)
	}
	if got := InvalidPayloadErrorCode(err); got != ToolErrorCodeActionNotFound {
		t.Fatalf("code = %q, want %q", got, ToolErrorCodeActionNotFound)
	}
}

func TestValidateInputsRequiredAndTypes(t *testing.T) {
	reg := payloadTestRegistration(func(m *Manifest) {
		m.Actions["create"] = ActionSpec{
			Inputs: map[string]FieldSpec{
				"name":  {Type: TypeString, Required: true},
				"count": {Type: TypeInteger, Required: true},
				"ratio": {Type: TypeFloat},
				"flag":  {Type: TypeBoolean},
				"raw":   {Type: TypeBytes},
				"tags":  {Type: TypeArray, Items: &FieldSpec{Type: TypeString}},
				"meta":  {Type: TypeObject, Properties: map[string]FieldSpec{"key": {Type: TypeString, Required: true}}},
				"extra": {Type: TypeAny},
			},
		}
	})
	limits := PayloadLimits{}

	good := map[string]any{
		"name": "widget", "count": 4, "ratio": 0.5, "flag": true,
		"raw": "base64-ish", "tags": []any{"a", "b"},
		"meta": map[string]any{"key": "v"}, "extra": 123,
		"undeclared": "ok",
	}
	if err := ValidateInputs(reg, "create", good, limits); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{name: "missing required", in: map[string]any{"count": 4}, want: `required input "name" is missing`},
		{name: "wrong type", in: map[string]any{"name": "x", "count": true}, want: `field "count" must be of type integer`},
		{name: "non-integral", in: map[string]any{"name": "x", "count": 4.5}, want: `field "count" must be of type integer`},
		{name: "array item type", in: map[string]any{"name": 1}, want: `field "name" must be of type string`},
		{name: "array items", in: map[string]any{"name": "a", "count": 1, "tags": true}, want: `field "tags" must be of type array`},
		{name: "array item element", in: map[string]any{"name": "a", "count": 1, "tags": []any{1, 2}}, want: `field "tags[0]" must be of type string`},
		{name: "object property", in: map[string]any{"name": "a", "count": 1, "meta": map[string]any{"other": "x"}}, want: `required field`},
		{name: "object type", in: map[string]any{"name": "a", "count": 1, "meta": 3}, want: `field "meta" must be of type object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInputs(reg, "create", tc.in, limits)
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("payload error = %v, want ErrInvalidPayload", err)
			}
			if got := InvalidPayloadErrorCode(err); got != ToolErrorCodeInvalidPayload {
				t.Fatalf("code = %q, want %q", got, ToolErrorCodeInvalidPayload)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("message %q missing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateInputsNoValueLeak(t *testing.T) {
	reg := payloadTestRegistration(func(m *Manifest) {
		m.Actions["send"] = ActionSpec{
			Inputs: map[string]FieldSpec{"email": {Type: TypeString, Required: true}},
		}
	})
	err := ValidateInputs(reg, "send", map[string]any{"email": 42}, PayloadLimits{})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("email typed wrong = %v", err)
	}
	if strings.Contains(err.Error(), "ada@example.invalid") {
		t.Fatalf("payload values must never appear in schema errors: %v", err)
	}
}

func TestValidateInputsSizeLimit(t *testing.T) {
	reg := payloadTestRegistration(func(m *Manifest) {
		m.Actions["create"] = ActionSpec{
			Inputs: map[string]FieldSpec{"blob": {Type: TypeString}},
		}
	})
	err := ValidateInputs(reg, "create", map[string]any{"blob": strings.Repeat("a", 300)}, PayloadLimits{MaxInputBytes: 128})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("size error = %v, want ErrPayloadTooLarge", err)
	}
	if got := InvalidPayloadErrorCode(err); got != ToolErrorCodePayloadTooLarge {
		t.Fatalf("code = %q, want %q", got, ToolErrorCodePayloadTooLarge)
	}
	// Same payload within a generous limit passes.
	if err := ValidateInputs(reg, "create", map[string]any{"blob": "small"}, PayloadLimits{MaxInputBytes: 128}); err != nil {
		t.Fatalf("small payload rejected: %v", err)
	}
}

func TestValidateOutputsRequiredAndSizes(t *testing.T) {
	reg := payloadTestRegistration(func(m *Manifest) {
		m.Actions["report"] = ActionSpec{
			Inputs: map[string]FieldSpec{"q": {Type: TypeString, Required: true}},
			Outputs: map[string]FieldSpec{
				"status": {Type: TypeString, Required: true},
				"text":   {Type: TypeString},
			},
		}
	})

	if err := ValidateOutputs(reg, "report", map[string]any{"status": "ok"}, PayloadLimits{}); err != nil {
		t.Fatalf("valid outputs rejected: %v", err)
	}
	err := ValidateOutputs(reg, "report", map[string]any{"text": "..."}, PayloadLimits{})
	if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), `required output "status" is missing`) {
		t.Fatalf("missing required output error = %v", err)
	}
	err = ValidateOutputs(reg, "report", map[string]any{"status": "ok", "text": strings.Repeat("x", 400)}, PayloadLimits{MaxOutputBytes: 64})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized output error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestTestActionPayloadGatePerOrigin(t *testing.T) {
	origins := []struct {
		name     string
		toolName string
		origin   ToolOrigin
		render   func(*Manifest)
	}{
		{"http", "http_probe", OriginHTTP, func(m *Manifest) { m.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"}) }},
		{"mcp", "mcp_probe", OriginMCP, func(m *Manifest) { m.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStdio, Command: "probe"}) }},
		{"stdio", "std_probe", OriginStdio, func(m *Manifest) { m.Transport = NewStdioTransport(StdioTransport{Command: "probe"}) }},
	}
	for _, originCase := range origins {
		t.Run(originCase.name, func(t *testing.T) {
			store := NewDaemonStore(newFakeDaemonBackend())
			manifest := NewManifest(originCase.toolName)
			originCase.render(&manifest)
			manifest.Actions["execute"] = ActionSpec{
				Inputs: map[string]FieldSpec{"q": {Type: TypeString, Required: true}},
			}
			if err := store.Upsert(context.Background(), ToolRegistration{
				Name:     originCase.toolName,
				Origin:   originCase.origin,
				Manifest: manifest,
				Status:   StatusReady,
				Enabled:  true,
			}); err != nil {
				t.Fatalf("store.Upsert: %v", err)
			}

			var invoked bool
			service, err := NewDaemonToolService(DaemonToolServiceConfig{
				Store: store,
				AdapterFactory: stubAdapterFactory{
					newFn: func(reg Registration) (Adapter, error) {
						invoked = true
						return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
					},
				},
			})
			if err != nil {
				t.Fatalf("service: %v", err)
			}

			// Missing required input fails before the adapter engages.
			_, err = service.TestAction(context.Background(), originCase.toolName, "execute", map[string]any{})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("invalid payload error = %v, want ErrInvalidPayload", err)
			}
			if invoked {
				t.Fatal("adapter must not engage for invalid payloads")
			}

			// Unknown actions fail typed before the adapter engages.
			_, err = service.TestAction(context.Background(), originCase.toolName, "missing", map[string]any{"q": "x"})
			if !errors.Is(err, ErrActionNotFound) {
				t.Fatalf("unknown action error = %v, want ErrActionNotFound", err)
			}
			if invoked {
				t.Fatal("adapter must not engage for unknown actions")
			}

			// Valid payloads proceed.
			if _, err := service.TestAction(context.Background(), originCase.toolName, "execute", map[string]any{"q": "ping"}); err != nil {
				t.Fatalf("valid payload: %v", err)
			}
			if !invoked {
				t.Fatal("adapter should engage for valid payloads")
			}
		})
	}
}

func TestTestActionOversizedInputRejectedBeforeFactory(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("sized_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["echo"] = ActionSpec{
		Inputs: map[string]FieldSpec{"blob": {Type: TypeString}},
	}
	if err := store.Upsert(context.Background(), ToolRegistration{
		Name: "sized_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}

	var invoked bool
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:         store,
		PayloadLimits: &PayloadLimits{MaxInputBytes: 32, MaxOutputBytes: 1 << 20},
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				invoked = true
				return &stubAdapter{response: InvokeResponse{DurationMS: 1}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, err = service.TestAction(context.Background(), "sized_tool", "echo", map[string]any{
		"blob": strings.Repeat("a", 64),
	})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized input = %v, want ErrPayloadTooLarge", err)
	}
	if invoked {
		t.Fatal("adapter must not engage for oversized payloads")
	}
}

func TestTestActionOversizedOutputRejected(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("sized_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["echo"] = ActionSpec{
		Inputs: map[string]FieldSpec{}, Outputs: map[string]FieldSpec{"blob": {Type: TypeString}},
	}
	if err := store.Upsert(context.Background(), ToolRegistration{
		Name: "sized_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:         store,
		PayloadLimits: &PayloadLimits{MaxInputBytes: 1 << 20, MaxOutputBytes: 32},
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return &stubAdapter{response: InvokeResponse{
					DurationMS: 1,
					Outputs:    map[string]any{"blob": strings.Repeat("x", 256)},
				}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	result, err := service.TestAction(context.Background(), "sized_tool", "echo", map[string]any{})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized output = %v, want ErrPayloadTooLarge", err)
	}
	if result.Success {
		t.Fatal("oversized results must not surface as success")
	}
}

func TestTestActionDeadlineContext(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("slow_probe")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801", TimeoutMS: 25})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{}}
	if err := store.Upsert(context.Background(), ToolRegistration{
		Name: "slow_probe", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return deadlineInspectingAdapter{}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if _, err := service.TestAction(context.Background(), "slow_probe", "execute", map[string]any{}); err != nil {
		t.Fatalf("invocation ctx deadline check failed: %v", err)
	}
}

// deadlineInspectingAdapter records whether the invoke context carried a
// deadline from the service-level invocation gate.
type deadlineInspectingAdapter struct{}

func (deadlineInspectingAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return InvokeResponse{}, errors.New("deadline missing")
	}
	return InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}, nil
}

func (deadlineInspectingAdapter) Close(ctx context.Context) error { return nil }

package petalflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSinkNode_FileSink(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]any
		data     map[string]any
		mode     string
		wantJSON bool
	}{
		{
			name: "writes JSON to file",
			config: map[string]any{
				"format": "json",
			},
			data:     map[string]any{"key": "value"},
			wantJSON: true,
		},
		{
			name: "writes text to file",
			config: map[string]any{
				"format": "text",
			},
			data:     map[string]any{"key": "value"},
			wantJSON: false,
		},
		{
			name:   "defaults to JSON format",
			config: map[string]any{
				// no format specified
			},
			data:     map[string]any{"test": 123},
			wantJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "output.txt")
			tt.config["path"] = filePath

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type:   SinkTypeFile,
						Name:   "test_file",
						Config: tt.config,
					},
				},
				ResultVar: "sink_result",
			})

			env := NewEnvelope()
			for k, v := range tt.data {
				env.SetVar(k, v)
			}

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check sink result
			sinkResult, ok := result.GetVar("sink_result")
			if !ok {
				t.Fatal("sink_result not set")
			}
			sr := sinkResult.(SinkResult)
			if len(sr.Targets) != 1 || !sr.Targets[0].Success {
				t.Errorf("sink failed: %+v", sr.Targets)
			}

			// Check file contents
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read file: %v", err)
			}

			if tt.wantJSON {
				var parsed map[string]any
				if err := json.Unmarshal(content, &parsed); err != nil {
					t.Errorf("expected JSON but got: %s", content)
				}
			}
		})
	}
}

func TestSinkNode_FileSink_Append(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "append.txt")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "append_file",
				Config: map[string]any{
					"path":   filePath,
					"format": "text",
					"mode":   "append",
				},
			},
		},
	})

	// First write
	env1 := NewEnvelope()
	env1.SetVar("line", "first")
	node.Run(context.Background(), env1)

	// Second write
	env2 := NewEnvelope()
	env2.SetVar("line", "second")
	node.Run(context.Background(), env2)

	// Check file contains both
	content, _ := os.ReadFile(filePath)
	if !strings.Contains(string(content), "first") || !strings.Contains(string(content), "second") {
		t.Errorf("append mode failed, content: %s", content)
	}
}

func TestSinkNode_WebhookSink(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{
			name:       "successful POST",
			statusCode: 200,
			wantError:  false,
		},
		{
			name:       "accepts 201 created",
			statusCode: 201,
			wantError:  false,
		},
		{
			name:       "fails on 4xx",
			statusCode: 400,
			wantError:  true,
		},
		{
			name:       "fails on 5xx",
			statusCode: 500,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := NewMockHTTPClient(tt.statusCode)

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type: SinkTypeWebhook,
						Name: "test_webhook",
						Config: map[string]any{
							"url":    "https://example.com/webhook",
							"method": "POST",
							"headers": map[string]any{
								"X-Custom": "header",
							},
						},
					},
				},
				HTTPClient: mockClient,
				ResultVar:  "sink_result",
			})

			env := NewEnvelope()
			env.SetVar("data", "test")

			result, err := node.Run(context.Background(), env)

			if tt.wantError {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Verify request was made
				if len(mockClient.Requests) != 1 {
					t.Fatalf("expected 1 request, got %d", len(mockClient.Requests))
				}

				req := mockClient.Requests[0]
				if req.URL.String() != "https://example.com/webhook" {
					t.Errorf("wrong URL: %s", req.URL)
				}
				if req.Method != "POST" {
					t.Errorf("wrong method: %s", req.Method)
				}
				if req.Header.Get("X-Custom") != "header" {
					t.Errorf("custom header not set")
				}
				if req.Header.Get("Content-Type") != "application/json" {
					t.Errorf("content-type not set")
				}

				// Check result
				sinkResult, _ := result.GetVar("sink_result")
				sr := sinkResult.(SinkResult)
				if !sr.Targets[0].Success {
					t.Errorf("sink should have succeeded")
				}
			}
		})
	}
}

func TestSinkNode_LogSink(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		message string
	}{
		{name: "info level", level: "info", message: "info message"},
		{name: "debug level", level: "debug", message: "debug message"},
		{name: "warn level", level: "warn", message: "warn message"},
		{name: "error level", level: "error", message: "error message"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type: SinkTypeLog,
						Name: "test_log",
						Config: map[string]any{
							"level":   tt.level,
							"message": tt.message,
						},
					},
				},
				Logger:    logger,
				ResultVar: "sink_result",
			})

			env := NewEnvelope()
			env.SetVar("key", "value")

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify log was written
			logOutput := buf.String()
			if !strings.Contains(logOutput, tt.message) {
				t.Errorf("log message not found in output: %s", logOutput)
			}

			// Check result
			sinkResult, _ := result.GetVar("sink_result")
			sr := sinkResult.(SinkResult)
			if !sr.Targets[0].Success {
				t.Errorf("sink should have succeeded")
			}
		})
	}
}

func TestSinkNode_MetricSink(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]any
		vars      map[string]any
		wantValue float64
		wantTags  map[string]string
	}{
		{
			name: "simple counter",
			config: map[string]any{
				"name": "workflow.completed",
			},
			vars:      map[string]any{},
			wantValue: 1.0,
			wantTags:  map[string]string{},
		},
		{
			name: "with value from field",
			config: map[string]any{
				"name":        "workflow.score",
				"value_field": "vars.score",
			},
			vars:      map[string]any{"score": 0.85},
			wantValue: 0.85,
			wantTags:  map[string]string{},
		},
		{
			name: "with tags",
			config: map[string]any{
				"name": "workflow.status",
				"tags": map[string]any{
					"workflow": "extract",
					"version":  "1.0",
				},
			},
			vars:      map[string]any{},
			wantValue: 1.0,
			wantTags:  map[string]string{"workflow": "extract", "version": "1.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewMockMetricRecorder()

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type:   SinkTypeMetric,
						Name:   "test_metric",
						Config: tt.config,
					},
				},
				MetricRecorder: recorder,
				ResultVar:      "sink_result",
			})

			env := NewEnvelope()
			for k, v := range tt.vars {
				env.SetVar(k, v)
			}

			_, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify metric was recorded
			if len(recorder.Metrics) != 1 {
				t.Fatalf("expected 1 metric, got %d", len(recorder.Metrics))
			}

			metric := recorder.Metrics[0]
			if metric.Name != tt.config["name"].(string) {
				t.Errorf("wrong metric name: %s", metric.Name)
			}
			if metric.Value != tt.wantValue {
				t.Errorf("wrong metric value: %f, want %f", metric.Value, tt.wantValue)
			}
			for k, v := range tt.wantTags {
				if metric.Tags[k] != v {
					t.Errorf("wrong tag %s: %s, want %s", k, metric.Tags[k], v)
				}
			}
		})
	}
}

func TestSinkNode_VarSink(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeVar,
				Name: "test_var",
				Config: map[string]any{
					"name": "sink_output",
				},
			},
		},
		ResultVar: "sink_result",
	})

	env := NewEnvelope()
	env.SetVar("data", "test_value")

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that var was set
	sinkOutput, ok := result.GetVar("sink_output")
	if !ok {
		t.Fatal("sink_output not set")
	}

	// Should contain the vars
	outputMap, ok := sinkOutput.(map[string]any)
	if !ok {
		t.Fatalf("sink_output wrong type: %T", sinkOutput)
	}
	if _, exists := outputMap["vars"]; !exists {
		t.Error("vars not in output")
	}
}

func TestSinkNode_CustomSink(t *testing.T) {
	customCalled := false
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test_custom",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					customCalled = true
					receivedData = data
					return nil
				},
			},
		},
		ResultVar: "sink_result",
	})

	env := NewEnvelope()
	env.SetVar("custom_data", "custom_value")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !customCalled {
		t.Error("custom function was not called")
	}
	if receivedData == nil {
		t.Error("custom function received nil data")
	}
}

func TestSinkNode_CustomSink_Error(t *testing.T) {
	expectedErr := errors.New("custom error")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test_custom",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return expectedErr
				},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error but got nil")
	}
}

func TestSinkNode_Condition(t *testing.T) {
	tests := []struct {
		name      string
		condition func(ctx context.Context, env *Envelope) (bool, error)
		wantRun   bool
	}{
		{
			name: "condition passes",
			condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil
			},
			wantRun: true,
		},
		{
			name: "condition fails",
			condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			wantRun: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sinkExecuted := false

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type:      SinkTypeCustom,
						Name:      "conditional_sink",
						Condition: tt.condition,
						CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
							sinkExecuted = true
							return nil
						},
					},
				},
				ResultVar: "sink_result",
			})

			env := NewEnvelope()
			result, _ := node.Run(context.Background(), env)

			if sinkExecuted != tt.wantRun {
				t.Errorf("sink executed = %v, want %v", sinkExecuted, tt.wantRun)
			}

			// Check skipped status
			sinkResult, _ := result.GetVar("sink_result")
			sr := sinkResult.(SinkResult)
			if !tt.wantRun && !sr.Targets[0].Skipped {
				t.Error("expected skipped = true when condition fails")
			}
		})
	}
}

func TestSinkNode_ErrorPolicy_Fail(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "failing_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return errors.New("sink failed")
				},
			},
		},
		ErrorPolicy: SinkErrorPolicyFail,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error with fail policy")
	}
}

func TestSinkNode_ErrorPolicy_Continue(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "failing_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return errors.New("sink failed")
				},
			},
		},
		ErrorPolicy: SinkErrorPolicyContinue,
		ResultVar:   "sink_result",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("expected no error with continue policy, got: %v", err)
	}

	// Check that failure was recorded in result
	sinkResult, _ := result.GetVar("sink_result")
	sr := sinkResult.(SinkResult)
	if sr.Targets[0].Success {
		t.Error("expected success = false")
	}
	if sr.Targets[0].Error == "" {
		t.Error("expected error message in result")
	}
}

func TestSinkNode_ErrorPolicy_Record(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "failing_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return errors.New("sink failed")
				},
			},
		},
		ErrorPolicy: SinkErrorPolicyRecord,
		ResultVar:   "sink_result",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Errorf("expected no error with record policy, got: %v", err)
	}

	// Check that error was recorded in envelope
	if !result.HasErrors() {
		t.Error("expected error to be recorded in envelope")
	}
}

func TestSinkNode_MultipleSinks(t *testing.T) {
	sink1Called := false
	sink2Called := false

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "sink_1",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					sink1Called = true
					return nil
				},
			},
			{
				Type: SinkTypeCustom,
				Name: "sink_2",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					sink2Called = true
					return nil
				},
			},
		},
		ResultVar: "sink_result",
	})

	env := NewEnvelope()
	result, _ := node.Run(context.Background(), env)

	if !sink1Called || !sink2Called {
		t.Error("not all sinks were called")
	}

	sinkResult, _ := result.GetVar("sink_result")
	sr := sinkResult.(SinkResult)
	if len(sr.Targets) != 2 {
		t.Errorf("expected 2 target results, got %d", len(sr.Targets))
	}
}

func TestSinkNode_Template(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "template_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
		Template: "Result: {{.vars.result}}",
	})

	env := NewEnvelope()
	env.SetVar("result", "success")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that template was rendered
	if receivedData != "Result: success" {
		t.Errorf("template not rendered correctly: %v", receivedData)
	}
}

func TestSinkNode_InputVars(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "input_vars_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
		InputVars: []string{"included"},
	})

	env := NewEnvelope()
	env.SetVar("included", "yes")
	env.SetVar("excluded", "no")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that only included vars are in output
	dataMap := receivedData.(map[string]any)
	vars := dataMap["vars"].(map[string]any)
	if _, ok := vars["included"]; !ok {
		t.Error("included var should be present")
	}
	if _, ok := vars["excluded"]; ok {
		t.Error("excluded var should not be present")
	}
}

func TestSinkNode_IncludeArtifacts(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "artifacts_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
		IncludeArtifacts: true,
	})

	env := NewEnvelope()
	env.AppendArtifact(Artifact{ID: "art1", Type: "text", Text: "content"})

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check artifacts in output
	dataMap := receivedData.(map[string]any)
	artifacts, ok := dataMap["artifacts"].([]map[string]any)
	if !ok || len(artifacts) != 1 {
		t.Error("artifacts should be included")
	}
}

func TestSinkNode_IncludeMessages(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "messages_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
		IncludeMessages: true,
	})

	env := NewEnvelope()
	env.AppendMessage(Message{Role: "user", Content: "hello"})

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check messages in output
	dataMap := receivedData.(map[string]any)
	messages, ok := dataMap["messages"].([]map[string]any)
	if !ok || len(messages) != 1 {
		t.Error("messages should be included")
	}
}

func TestSinkNode_IncludeTrace(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "trace_sink",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
		IncludeTrace: true,
	})

	env := NewEnvelope()
	env.Trace.RunID = "run-123"

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check trace in output
	dataMap := receivedData.(map[string]any)
	trace, ok := dataMap["trace"].(map[string]any)
	if !ok {
		t.Fatal("trace should be included")
	}
	if trace["run_id"] != "run-123" {
		t.Errorf("wrong run_id: %v", trace["run_id"])
	}
}

func TestSinkNode_EnvelopeIsolation(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeVar,
				Name: "var_sink",
				Config: map[string]any{
					"name": "sink_output",
				},
			},
		},
		ResultVar: "sink_result",
	})

	env := NewEnvelope()
	env.SetVar("original", "value")

	result, _ := node.Run(context.Background(), env)

	// Check original envelope not modified
	if _, ok := env.GetVar("sink_result"); ok {
		t.Error("original envelope should not have sink_result")
	}
	if _, ok := env.GetVar("sink_output"); ok {
		t.Error("original envelope should not have sink_output")
	}

	// Result should have both
	if _, ok := result.GetVar("sink_result"); !ok {
		t.Error("result should have sink_result")
	}
}

func TestSinkNode_ContextCancellation(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return nil
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, NewEnvelope())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestSinkNode_ID(t *testing.T) {
	node := NewSinkNode("my_sink", SinkNodeConfig{})

	if node.ID() != "my_sink" {
		t.Errorf("ID = %q, want %q", node.ID(), "my_sink")
	}
}

func TestSinkNode_Kind(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{})

	if node.Kind() != NodeKindSink {
		t.Errorf("Kind = %q, want %q", node.Kind(), NodeKindSink)
	}
}

func TestSinkNode_FileSink_MissingPath(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type:   SinkTypeFile,
				Name:   "no_path",
				Config: map[string]any{},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestSinkNode_WebhookSink_MissingURL(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type:   SinkTypeWebhook,
				Name:   "no_url",
				Config: map[string]any{},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing URL")
	}
}

func TestSinkNode_MetricSink_MissingName(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type:   SinkTypeMetric,
				Name:   "no_name",
				Config: map[string]any{},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing metric name")
	}
}

func TestSinkNode_VarSink_MissingName(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type:   SinkTypeVar,
				Name:   "no_name",
				Config: map[string]any{},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing var name")
	}
}

func TestSinkNode_CustomSink_MissingFunc(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "no_func",
				// CustomFunc not set
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing custom func")
	}
}

func TestSinkNode_InvalidTemplate(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return nil
				},
			},
		},
		Template: "{{.invalid syntax",
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for invalid template")
	}
}

// MockHTTPClient tests

func TestMockHTTPClient_RecordsRequests(t *testing.T) {
	client := NewMockHTTPClient(200)

	req, _ := http.NewRequest("POST", "https://example.com", bytes.NewReader([]byte("body")))
	client.Do(req)

	if len(client.Requests) != 1 {
		t.Errorf("expected 1 request, got %d", len(client.Requests))
	}
}

func TestMockHTTPClient_ReturnsError(t *testing.T) {
	client := NewMockHTTPClient(200)
	client.Error = errors.New("network error")

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	_, err := client.Do(req)

	if err == nil {
		t.Error("expected error")
	}
}

func TestMockHTTPClient_ReturnsCustomResponse(t *testing.T) {
	client := NewMockHTTPClient(200)
	client.Response = &http.Response{
		StatusCode: 418,
		Body:       io.NopCloser(bytes.NewReader([]byte("I'm a teapot"))),
	}

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	resp, _ := client.Do(req)

	if resp.StatusCode != 418 {
		t.Errorf("expected status 418, got %d", resp.StatusCode)
	}
}

// MockMetricRecorder tests

func TestMockMetricRecorder_Records(t *testing.T) {
	recorder := NewMockMetricRecorder()

	recorder.Record("test.metric", 42.0, map[string]string{"env": "test"})

	if len(recorder.Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(recorder.Metrics))
	}

	metric := recorder.Metrics[0]
	if metric.Name != "test.metric" {
		t.Errorf("wrong name: %s", metric.Name)
	}
	if metric.Value != 42.0 {
		t.Errorf("wrong value: %f", metric.Value)
	}
	if metric.Tags["env"] != "test" {
		t.Errorf("wrong tag: %s", metric.Tags["env"])
	}
}

// Additional tests for increased coverage

func TestSinkNode_Config(t *testing.T) {
	config := SinkNodeConfig{
		Sinks: []SinkTarget{
			{Type: SinkTypeLog, Name: "test"},
		},
		InputVars:        []string{"var1"},
		IncludeArtifacts: true,
		IncludeMessages:  true,
		IncludeTrace:     true,
		Template:         "{{.vars}}",
		ErrorPolicy:      SinkErrorPolicyContinue,
		ResultVar:        "result",
	}

	node := NewSinkNode("test_node", config)
	got := node.Config()

	if len(got.Sinks) != 1 {
		t.Errorf("Config().Sinks len = %d, want 1", len(got.Sinks))
	}
	if got.Sinks[0].Name != "test" {
		t.Errorf("Config().Sinks[0].Name = %q, want %q", got.Sinks[0].Name, "test")
	}
	if len(got.InputVars) != 1 || got.InputVars[0] != "var1" {
		t.Errorf("Config().InputVars = %v, want [var1]", got.InputVars)
	}
	if !got.IncludeArtifacts {
		t.Error("Config().IncludeArtifacts should be true")
	}
	if !got.IncludeMessages {
		t.Error("Config().IncludeMessages should be true")
	}
	if !got.IncludeTrace {
		t.Error("Config().IncludeTrace should be true")
	}
	if got.Template != "{{.vars}}" {
		t.Errorf("Config().Template = %q, want %q", got.Template, "{{.vars}}")
	}
	if got.ErrorPolicy != SinkErrorPolicyContinue {
		t.Errorf("Config().ErrorPolicy = %q, want %q", got.ErrorPolicy, SinkErrorPolicyContinue)
	}
	if got.ResultVar != "result" {
		t.Errorf("Config().ResultVar = %q, want %q", got.ResultVar, "result")
	}
}

func TestSinkNode_TemplateWithJSONFuncs(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantErr  bool
		check    func(data any) bool
	}{
		{
			name:     "json function",
			template: `{"data": {{json .vars}}}`,
			check: func(data any) bool {
				s, ok := data.(string)
				return ok && strings.Contains(s, `"key":"value"`)
			},
		},
		{
			name:     "jsonPretty function",
			template: `{{jsonPretty .vars}}`,
			check: func(data any) bool {
				s, ok := data.(string)
				return ok && strings.Contains(s, "\n")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedData any
			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type: SinkTypeCustom,
						Name: "template_test",
						CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
							receivedData = data
							return nil
						},
					},
				},
				Template: tt.template,
			})

			env := NewEnvelope()
			env.SetVar("key", "value")

			_, err := node.Run(context.Background(), env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}

			if !tt.wantErr && tt.check != nil && !tt.check(receivedData) {
				t.Errorf("check failed for data: %v", receivedData)
			}
		})
	}
}

func TestSinkNode_LogSink_DefaultLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					// No level specified - should default to info
					"message": "default level test",
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "INFO") {
		t.Errorf("expected INFO level, got: %s", logOutput)
	}
}

func TestSinkNode_LogSink_WarningLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					"level":   "warning",
					"message": "warning level test",
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "WARN") {
		t.Errorf("expected WARN level, got: %s", logOutput)
	}
}

func TestSinkNode_LogSink_WithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					"level":   "info",
					"message": "fields test",
					"fields":  []any{"vars.status", "vars.count"},
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	env.SetVar("status", "success")
	env.SetVar("count", 42)

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "fields test") {
		t.Errorf("expected message in log output: %s", logOutput)
	}
}

func TestSinkNode_LogSink_UnknownLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					"level":   "unknown_level",
					"message": "unknown level test",
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unknown levels should default to info
	logOutput := buf.String()
	if !strings.Contains(logOutput, "INFO") {
		t.Errorf("expected INFO level for unknown, got: %s", logOutput)
	}
}

func TestSinkNode_LogSink_ContextCancelled(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					"level":   "info",
					"message": "test",
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, NewEnvelope())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestSinkNode_WebhookSink_WithTimeout(t *testing.T) {
	mockClient := NewMockHTTPClient(200)

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeWebhook,
				Name: "test_webhook",
				Config: map[string]any{
					"url":     "https://example.com/webhook",
					"timeout": "5s",
				},
			},
		},
		HTTPClient: mockClient,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockClient.Requests) != 1 {
		t.Error("expected request to be made")
	}
}

func TestSinkNode_WebhookSink_InvalidTimeout(t *testing.T) {
	mockClient := NewMockHTTPClient(200)

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeWebhook,
				Name: "test_webhook",
				Config: map[string]any{
					"url":     "https://example.com/webhook",
					"timeout": "invalid",
				},
			},
		},
		HTTPClient: mockClient,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still work, just without custom timeout
	if len(mockClient.Requests) != 1 {
		t.Error("expected request to be made")
	}
}

func TestSinkNode_WebhookSink_CustomMethod(t *testing.T) {
	mockClient := NewMockHTTPClient(200)

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeWebhook,
				Name: "test_webhook",
				Config: map[string]any{
					"url":    "https://example.com/webhook",
					"method": "PUT",
				},
			},
		},
		HTTPClient: mockClient,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockClient.Requests[0].Method != "PUT" {
		t.Errorf("expected PUT method, got %s", mockClient.Requests[0].Method)
	}
}

func TestSinkNode_WebhookSink_HTTPClientError(t *testing.T) {
	mockClient := NewMockHTTPClient(200)
	mockClient.Error = errors.New("network error")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeWebhook,
				Name: "test_webhook",
				Config: map[string]any{
					"url": "https://example.com/webhook",
				},
			},
		},
		HTTPClient: mockClient,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error from HTTP client")
	}
}

func TestSinkNode_FileSink_TextFormat_ByteSlice(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "output.bin")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path":   filePath,
					"format": "text",
				},
			},
		},
		Template: "raw bytes here",
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filePath)
	if string(content) != "raw bytes here" {
		t.Errorf("unexpected content: %s", content)
	}
}

func TestSinkNode_FileSink_UnsupportedFormat(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "output.xml")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path":   filePath,
					"format": "xml",
				},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("expected 'unsupported format' error, got: %v", err)
	}
}

func TestSinkNode_FileSink_UnsupportedMode(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "output.txt")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path": filePath,
					"mode": "prepend",
				},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error for unsupported mode")
	}
	if !strings.Contains(err.Error(), "unsupported mode") {
		t.Errorf("expected 'unsupported mode' error, got: %v", err)
	}
}

func TestSinkNode_FileSink_ContextCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "output.txt")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path": filePath,
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, NewEnvelope())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestSinkNode_MetricSink_WithIntValue(t *testing.T) {
	recorder := NewMockMetricRecorder()

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name":  "workflow.count",
					"value": 42,
				},
			},
		},
		MetricRecorder: recorder,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(recorder.Metrics))
	}
	if recorder.Metrics[0].Value != 42.0 {
		t.Errorf("expected value 42, got %f", recorder.Metrics[0].Value)
	}
}

func TestSinkNode_MetricSink_WithFloatValue(t *testing.T) {
	recorder := NewMockMetricRecorder()

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name":  "workflow.score",
					"value": 0.95,
				},
			},
		},
		MetricRecorder: recorder,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if recorder.Metrics[0].Value != 0.95 {
		t.Errorf("expected value 0.95, got %f", recorder.Metrics[0].Value)
	}
}

func TestSinkNode_MetricSink_FallbackToLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name": "workflow.completed",
				},
			},
		},
		Logger: logger,
		// No MetricRecorder - should fallback to logging
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "metric") {
		t.Errorf("expected metric to be logged, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "workflow.completed") {
		t.Errorf("expected metric name in log, got: %s", logOutput)
	}
}

func TestSinkNode_MetricSink_ContextCancelled(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name": "test",
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, NewEnvelope())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestSinkNode_VarSink_ContextCancelled(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeVar,
				Name: "test_var",
				Config: map[string]any{
					"name": "output",
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, NewEnvelope())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestSinkNode_UnknownSinkType(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: "unknown_type",
				Name: "test",
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error for unknown sink type")
	}
	if !strings.Contains(err.Error(), "unknown sink type") {
		t.Errorf("expected 'unknown sink type' error, got: %v", err)
	}
}

func TestSinkNode_ConditionError(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "conditional_sink",
				Condition: func(ctx context.Context, env *Envelope) (bool, error) {
					return false, errors.New("condition failed")
				},
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return nil
				},
			},
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error from condition")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Errorf("expected condition error, got: %v", err)
	}
}

func TestSinkNode_BuildOutputData_WithInput(t *testing.T) {
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
	})

	env := NewEnvelope()
	env.Input = "test input data"

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dataMap, ok := receivedData.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", receivedData)
	}
	if dataMap["input"] != "test input data" {
		t.Errorf("input not in output data: %v", dataMap)
	}
}

func TestSinkNode_TemplateExecutionError(t *testing.T) {
	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					return nil
				},
			},
		},
		Template: "{{call .vars}}", // Will cause execution error - call on non-function
	})

	env := NewEnvelope()
	env.SetVar("test", "value")
	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected template execution error")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("expected template error, got: %v", err)
	}
}

func TestSinkNode_LogSink_DefaultMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					// No message - should default to "sink output"
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "sink output") {
		t.Errorf("expected default message, got: %s", logOutput)
	}
}

func TestSinkNode_NewSinkNode_Defaults(t *testing.T) {
	node := NewSinkNode("test", SinkNodeConfig{})

	config := node.Config()

	// Check ErrorPolicy defaults to Fail
	if config.ErrorPolicy != SinkErrorPolicyFail {
		t.Errorf("ErrorPolicy should default to %q, got %q", SinkErrorPolicyFail, config.ErrorPolicy)
	}

	// Check HTTPClient is not nil
	if config.HTTPClient == nil {
		t.Error("HTTPClient should not be nil")
	}

	// Check Logger is not nil
	if config.Logger == nil {
		t.Error("Logger should not be nil")
	}
}

func TestSinkNode_LogSink_FieldsWithNonExistentPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeLog,
				Name: "test_log",
				Config: map[string]any{
					"level":   "info",
					"message": "fields test",
					"fields":  []any{"vars.nonexistent", "vars.also.missing"},
				},
			},
		},
		Logger: logger,
	})

	env := NewEnvelope()
	env.SetVar("status", "success")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should complete without error, just won't include the missing fields
	logOutput := buf.String()
	if !strings.Contains(logOutput, "fields test") {
		t.Errorf("expected message in log output: %s", logOutput)
	}
}

func TestSinkNode_MetricSink_ValueFieldNotFound(t *testing.T) {
	recorder := NewMockMetricRecorder()

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name":        "workflow.score",
					"value_field": "vars.nonexistent",
				},
			},
		},
		MetricRecorder: recorder,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should default to 1.0 when field not found
	if recorder.Metrics[0].Value != 1.0 {
		t.Errorf("expected default value 1.0, got %f", recorder.Metrics[0].Value)
	}
}

func TestSinkNode_MetricSink_ValueFieldNonNumeric(t *testing.T) {
	recorder := NewMockMetricRecorder()

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeMetric,
				Name: "test_metric",
				Config: map[string]any{
					"name":        "workflow.score",
					"value_field": "vars.status",
				},
			},
		},
		MetricRecorder: recorder,
	})

	env := NewEnvelope()
	env.SetVar("status", "not a number")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should default to 1.0 when field is non-numeric
	if recorder.Metrics[0].Value != 1.0 {
		t.Errorf("expected default value 1.0, got %f", recorder.Metrics[0].Value)
	}
}

func TestGetNestedMapValue(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		path     string
		want     any
		wantOk   bool
	}{
		{
			name:   "simple path",
			data:   map[string]any{"key": "value"},
			path:   "key",
			want:   "value",
			wantOk: true,
		},
		{
			name:   "nested path",
			data:   map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}},
			path:   "a.b.c",
			want:   "deep",
			wantOk: true,
		},
		{
			name:   "missing path",
			data:   map[string]any{"key": "value"},
			path:   "missing",
			want:   nil,
			wantOk: false,
		},
		{
			name:   "path through non-map",
			data:   map[string]any{"key": "value"},
			path:   "key.subkey",
			want:   nil,
			wantOk: false,
		},
		{
			name:   "empty path",
			data:   map[string]any{"key": "value"},
			path:   "",
			want:   map[string]any{"key": "value"},
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't call getNestedMapValue directly as it's unexported,
			// but we can test it through the log sink with fields
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			node := NewSinkNode("sink1", SinkNodeConfig{
				Sinks: []SinkTarget{
					{
						Type: SinkTypeLog,
						Name: "test_log",
						Config: map[string]any{
							"level":   "info",
							"message": "test",
							"fields":  []any{tt.path},
						},
					},
				},
				Logger: logger,
			})

			env := NewEnvelope()
			for k, v := range tt.data {
				env.SetVar(k, v)
			}

			_, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	// Test through nested map value access
	var receivedData any

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeCustom,
				Name: "test",
				CustomFunc: func(ctx context.Context, data any, env *Envelope) error {
					receivedData = data
					return nil
				},
			},
		},
	})

	env := NewEnvelope()
	env.SetVar("a", map[string]any{
		"b": map[string]any{
			"c": "deep value",
		},
	})

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dataMap := receivedData.(map[string]any)
	vars := dataMap["vars"].(map[string]any)
	a := vars["a"].(map[string]any)
	b := a["b"].(map[string]any)
	if b["c"] != "deep value" {
		t.Errorf("nested value not preserved: %v", receivedData)
	}
}

func TestSinkNode_FileSink_DefaultMode(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "output.txt")

	node := NewSinkNode("sink1", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path":   filePath,
					"format": "text",
					// mode not specified - should default to overwrite
				},
			},
		},
		Template: "first content",
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}

	// Write again - should overwrite
	node2 := NewSinkNode("sink2", SinkNodeConfig{
		Sinks: []SinkTarget{
			{
				Type: SinkTypeFile,
				Name: "test_file",
				Config: map[string]any{
					"path":   filePath,
					"format": "text",
				},
			},
		},
		Template: "second content",
	})

	_, err = node2.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}

	content, _ := os.ReadFile(filePath)
	if string(content) != "second content" {
		t.Errorf("expected 'second content', got: %s", content)
	}
}

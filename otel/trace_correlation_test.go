package otel_test

import (
	"context"
	"testing"
	"time"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/nodes"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/runtime"
)

// correlatedRunStub is a deterministic LLM client; the graph wraps it in
// a context-instrumented client exactly like the serve ClientFactory.
type correlatedRunStub struct{}

func (correlatedRunStub) Complete(_ context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{
		Text:     "the answer",
		Model:    req.Model,
		Provider: "stub",
		Usage:    core.LLMTokenUsage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
	}, nil
}

// TestRunSpanTreeCorrelation drives a full workflow run (LLM node with
// a context-instrumented provider client, then a tool node) through the
// runtime with the tracing and metrics handlers wired the way
// `petalflow serve` wires them, and asserts one correlated trace tree:
// run -> node -> chat (LLM) and run -> node -> tool, with tenant and
// workflow context attached.
func TestRunSpanTreeCorrelation(t *testing.T) {
	exporter, tp := newTestTracer()
	metricReader, meterProvider := newTestMeter()
	tracer := tp.Tracer("petalflow")

	tracing := petalotel.NewTracingHandler(tracer)
	metrics, err := petalotel.NewMetricsHandler(meterProvider.Meter("petalflow"))
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	llmNode := nodes.NewLLMNode("answer", llmprovider.NewContextInstrumentedClient(correlatedRunStub{}), nodes.LLMNodeConfig{
		Model:     "test-model",
		InputVars: []string{"query"},
		OutputKey: "llm_result",
	})

	lookupTool := core.NewFuncTool("lookup", "looks up a value", func(_ context.Context, args map[string]any) (map[string]any, error) {
		return map[string]any{"found": true}, nil
	})
	toolNode := nodes.NewToolNode("lookup-node", lookupTool, nodes.ToolNodeConfig{
		OutputKey:  "tool_result",
		StaticArgs: map[string]any{"key": "value"},
	})

	g, err := graph.NewGraphBuilder("correlation-test").
		AddNode(llmNode).
		Edge(toolNode).
		Build()
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	opts := runtime.DefaultRunOptions()
	opts.WorkflowID = "wf-1"
	opts.TenantID = "tenant-a"
	opts.EventHandler = runtime.MultiEventHandler(tracing.Handle, metrics.Handle)
	opts.EventEmitterDecorator = func(emit runtime.EventEmitter) runtime.EventEmitter {
		return petalotel.EnrichEmitter(emit, tracing)
	}

	result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope().WithVar("query", "subject"), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok := result.GetVar("tool_result"); !ok {
		t.Fatal("tool did not run")
	}

	spans := exporter.GetSpans()
	byName := map[string]*tracetest.SpanStub{}
	for i := range spans {
		if _, duplicate := byName[spans[i].Name]; duplicate {
			t.Fatalf("duplicate span %q", spans[i].Name)
		}
		byName[spans[i].Name] = &spans[i]
	}

	runSpan, ok := byName["run:correlation-test"]
	if !ok {
		t.Fatalf("run span missing; spans = %v", spanNames(spans))
	}
	llmNodeSpan, ok := byName["node:answer"]
	if !ok {
		t.Fatalf("llm node span missing; spans = %v", spanNames(spans))
	}
	toolNodeSpan, ok := byName["node:lookup-node"]
	if !ok {
		t.Fatalf("tool node span missing; spans = %v", spanNames(spans))
	}
	chatSpan, ok := byName["chat test-model"]
	if !ok {
		t.Fatalf("chat (LLM) span missing; spans = %v", spanNames(spans))
	}
	toolSpan, ok := byName["lookup"]
	if !ok {
		t.Fatalf("tool span missing; spans = %v", spanNames(spans))
	}

	// One trace tree: every span shares the run's trace ID, and the
	// parent chain is run -> node -> operation.
	traceID := runSpan.SpanContext.TraceID()
	for name, span := range byName {
		if span.SpanContext.TraceID() != traceID {
			t.Errorf("span %q is not part of the run trace", name)
		}
	}
	if llmNodeSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Error("llm node span is not a child of the run span")
	}
	if toolNodeSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Error("tool node span is not a child of the run span")
	}
	if chatSpan.Parent.SpanID() != llmNodeSpan.SpanContext.SpanID() {
		t.Error("chat span is not a child of the llm node span")
	}
	if toolSpan.Parent.SpanID() != toolNodeSpan.SpanContext.SpanID() {
		t.Error("tool span is not a child of the tool node span")
	}

	// Correlation context on the run span.
	if got := spanAttributeValue(*runSpan, "petalflow.tenant_id"); got != "tenant-a" {
		t.Errorf("run span tenant_id = %q, want tenant-a", got)
	}
	if got := spanAttributeValue(*runSpan, "petalflow.workflow_id"); got != "wf-1" {
		t.Errorf("run span workflow_id = %q, want wf-1", got)
	}

	// GenAI conventions on the LLM span.
	if got := spanAttributeValue(*chatSpan, "gen_ai.operation.name"); got != "chat" {
		t.Errorf("chat span operation = %q, want chat", got)
	}
	if got := spanAttributeValue(*chatSpan, "gen_ai.request.model"); got != "test-model" {
		t.Errorf("chat span request model = %q, want test-model", got)
	}
	if got := spanAttributeValue(*chatSpan, "gen_ai.provider.name"); got != "stub" {
		t.Errorf("chat span provider = %q, want stub", got)
	}
	if got := spanAttributeValue(*chatSpan, "gen_ai.usage.input_tokens"); got != "4" {
		t.Errorf("chat span input tokens = %q, want 4", got)
	}

	// GenAI conventions on the tool span.
	if got := spanAttributeValue(*toolSpan, "gen_ai.operation.name"); got != "execute_tool" {
		t.Errorf("tool span operation = %q, want execute_tool", got)
	}
	if got := spanAttributeValue(*toolSpan, "gen_ai.tool.name"); got != "lookup" {
		t.Errorf("tool span tool name = %q, want lookup", got)
	}
	if got := spanAttributeValue(*toolSpan, "gen_ai.tool.description"); got != "looks up a value" {
		t.Errorf("tool span description = %q, want tool description", got)
	}
	if toolSpan.Status.Code != otelcodes.Ok {
		t.Errorf("tool span status = %v, want Ok", toolSpan.Status.Code)
	}

	// Privacy default holds inside the trace tree: no completion
	// content on the chat span.
	if hasSpanAttribute(*chatSpan, "petalflow.llm.completion") {
		t.Error("chat span carries completion content by default")
	}

	// Events carry trace/span correlation IDs (EnrichEmitter), and the
	// GenAI + runtime metrics recorded for the run.
	rm := collectMetrics(t, metricReader)
	if findMetric(rm, "gen_ai.client.token.usage") == nil {
		t.Error("gen_ai.client.token.usage metric missing")
	}
	if findMetric(rm, "gen_ai.client.operation.duration") == nil {
		t.Error("gen_ai.client.operation.duration metric missing")
	}
	if findMetric(rm, "petalflow.node.executions") == nil {
		t.Error("petalflow.node.executions metric missing")
	}
}

func spanNames(spans []tracetest.SpanStub) []string {
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name)
	}
	return names
}

// Compile-time: keep the sdk imports referenced.
var (
	_ sdktrace.TracerProvider
	_ sdkmetric.MeterProvider
	_ metricdata.ResourceMetrics
	_ time.Duration
)

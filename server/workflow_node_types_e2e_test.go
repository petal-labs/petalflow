package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestWorkflowNodeTypesE2E_DaemonAPI_RunCoverage(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)

	cases := []struct {
		name   string
		node   map[string]any
		nodes  []map[string]any
		edges  []map[string]any
		entry  string
		input  map[string]any
		assert func(t *testing.T, run RunResponse, events []runtime.Event)
	}{
		{
			name: "llm_router",
			nodes: []map[string]any{
				{
					"id":   "route_with_llm",
					"type": "llm_router",
					"config": map[string]any{
						"provider": "openai",
						"model":    "gpt-4o-mini",
						"allowed_targets": map[string]any{
							"openai": "route_a",
							"other":  "route_b",
						},
					},
				},
				{
					"id":   "route_a",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "A",
						"output_var": "route_marker",
					},
				},
				{
					"id":   "route_b",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "B",
						"output_var": "route_marker",
					},
				},
			},
			edges: []map[string]any{
				{"source": "route_with_llm", "target": "route_a"},
				{"source": "route_with_llm", "target": "route_b"},
			},
			entry: "route_with_llm",
			assert: func(t *testing.T, run RunResponse, events []runtime.Event) {
				t.Helper()
				if run.Output.Vars["route_marker"] != "A" {
					t.Fatalf("route_marker = %v, want %q", run.Output.Vars["route_marker"], "A")
				}
				raw, ok := run.Output.Vars["route_with_llm_decision"]
				if !ok {
					t.Fatal("expected route_with_llm_decision in output vars")
				}
				decision, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("decision type = %T, want map[string]any", raw)
				}
				targets := routeDecisionTargets(decision)
				if len(targets) == 0 || targets[0] != "route_a" {
					t.Fatalf("llm router targets = %v, want [route_a]", targets)
				}
				assertEventKindsPresent(t, events, runtime.EventRouteDecision)
				if hasNodeEvent(events, runtime.EventNodeStarted, "route_b") {
					t.Fatal("route_b should not be executed for llm_router decision")
				}
			},
		},
		{
			name: "rule_router",
			nodes: []map[string]any{
				{
					"id":   "route_with_rules",
					"type": "rule_router",
					"config": map[string]any{
						"default_target": "route_default",
						"rules": []any{
							map[string]any{
								"target": "route_priority",
								"reason": "priority=true",
								"conditions": []any{
									map[string]any{
										"var_path": "priority",
										"op":       "eq",
										"value":    true,
									},
								},
							},
						},
					},
				},
				{
					"id":   "route_priority",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "priority",
						"output_var": "chosen_route",
					},
				},
				{
					"id":   "route_default",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "default",
						"output_var": "chosen_route",
					},
				},
			},
			edges: []map[string]any{
				{"source": "route_with_rules", "target": "route_priority"},
				{"source": "route_with_rules", "target": "route_default"},
			},
			entry: "route_with_rules",
			input: map[string]any{
				"priority": true,
			},
			assert: func(t *testing.T, run RunResponse, events []runtime.Event) {
				t.Helper()
				if run.Output.Vars["chosen_route"] != "priority" {
					t.Fatalf("chosen_route = %v, want %q", run.Output.Vars["chosen_route"], "priority")
				}
				raw, ok := run.Output.Vars["route_with_rules_decision"]
				if !ok {
					t.Fatal("expected route_with_rules_decision in output vars")
				}
				decision, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("decision type = %T, want map[string]any", raw)
				}
				targets := routeDecisionTargets(decision)
				if len(targets) == 0 || targets[0] != "route_priority" {
					t.Fatalf("rule router targets = %v, want [route_priority]", targets)
				}
				assertEventKindsPresent(t, events, runtime.EventRouteDecision)
				if hasNodeEvent(events, runtime.EventNodeStarted, "route_default") {
					t.Fatal("route_default should not be executed for priority=true")
				}
			},
		},
		{
			name: "filter",
			node: map[string]any{
				"id":   "filter_scores",
				"type": "filter",
				"config": map[string]any{
					"target":     "var",
					"input_var":  "items",
					"output_var": "filtered",
					"filters": []any{
						map[string]any{
							"type":        "top_n",
							"n":           1,
							"score_field": "score",
							"order":       "desc",
						},
					},
				},
			},
			input: map[string]any{
				"items": []any{
					map[string]any{"name": "low", "score": 1},
					map[string]any{"name": "high", "score": 10},
				},
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				raw, ok := run.Output.Vars["filtered"]
				if !ok {
					t.Fatal("expected filtered output var")
				}
				items, ok := raw.([]any)
				if !ok {
					t.Fatalf("filtered type = %T, want []any", raw)
				}
				if len(items) != 1 {
					t.Fatalf("filtered len = %d, want 1", len(items))
				}
				item, _ := items[0].(map[string]any)
				if item["name"] != "high" {
					t.Fatalf("top item name = %v, want %q", item["name"], "high")
				}
			},
		},
		{
			name: "transform",
			node: map[string]any{
				"id":   "format_name",
				"type": "transform",
				"config": map[string]any{
					"transform":  "template",
					"template":   "hello {{.name}}",
					"output_var": "formatted",
				},
			},
			input: map[string]any{
				"name": "PetalFlow",
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				if run.Output.Vars["formatted"] != "hello PetalFlow" {
					t.Fatalf("formatted = %v, want %q", run.Output.Vars["formatted"], "hello PetalFlow")
				}
			},
		},
		{
			name: "merge",
			nodes: []map[string]any{
				{
					"id":   "branch_router",
					"type": "rule_router",
					"config": map[string]any{
						"allow_multiple": true,
						"rules": []any{
							map[string]any{
								"target": "branch_a",
								"conditions": []any{
									map[string]any{
										"var_path": "seed",
										"op":       "exists",
									},
								},
							},
							map[string]any{
								"target": "branch_b",
								"conditions": []any{
									map[string]any{
										"var_path": "seed",
										"op":       "exists",
									},
								},
							},
						},
					},
				},
				{
					"id":   "branch_a",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "A",
						"output_var": "alpha",
					},
				},
				{
					"id":   "branch_b",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "B",
						"output_var": "beta",
					},
				},
				{
					"id":   "merge_only",
					"type": "merge",
				},
			},
			edges: []map[string]any{
				{"source": "branch_router", "target": "branch_a"},
				{"source": "branch_router", "target": "branch_b"},
				{"source": "branch_a", "target": "merge_only"},
				{"source": "branch_b", "target": "merge_only"},
			},
			entry: "branch_router",
			input: map[string]any{
				"seed": "yes",
			},
			assert: func(t *testing.T, run RunResponse, events []runtime.Event) {
				t.Helper()
				if run.Output.Vars["alpha"] != "A" {
					t.Fatalf("alpha = %v, want %q", run.Output.Vars["alpha"], "A")
				}
				if run.Output.Vars["beta"] != "B" {
					t.Fatalf("beta = %v, want %q", run.Output.Vars["beta"], "B")
				}
				if !hasNodeEvent(events, runtime.EventNodeStarted, "merge_only") {
					t.Fatal("expected merge_only node to execute")
				}
				assertEventKindsPresent(t, events, runtime.EventRouteDecision)
			},
		},
		{
			name: "tool",
			node: map[string]any{
				"id":   "run_template_tool",
				"type": "tool",
				"config": map[string]any{
					"tool_name":  "template_render.render",
					"output_key": "tool_output",
					"static_args": map[string]any{
						"template": "hello {{.name}}",
						"values": map[string]any{
							"name": "PetalFlow",
						},
					},
				},
			},
			assert: func(t *testing.T, run RunResponse, events []runtime.Event) {
				t.Helper()
				raw, ok := run.Output.Vars["tool_output"]
				if !ok {
					t.Fatal("expected tool_output var")
				}
				out, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("tool_output type = %T, want map[string]any", raw)
				}
				if out["rendered"] != "hello PetalFlow" {
					t.Fatalf("rendered = %v, want %q", out["rendered"], "hello PetalFlow")
				}
				assertEventKindsPresent(t, events, runtime.EventToolCall, runtime.EventToolResult)
			},
		},
		{
			name: "gate",
			nodes: []map[string]any{
				{
					"id":   "gate_redirect",
					"type": "gate",
					"config": map[string]any{
						"condition_var":    "is_allowed",
						"on_fail":          "redirect",
						"redirect_node_id": "route_blocked",
						"result_var":       "gate_result",
					},
				},
				{
					"id":   "route_allowed",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "allowed",
						"output_var": "gate_route",
					},
				},
				{
					"id":   "route_blocked",
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "blocked",
						"output_var": "gate_route",
					},
				},
			},
			edges: []map[string]any{
				{"source": "gate_redirect", "target": "route_allowed"},
				{"source": "gate_redirect", "target": "route_blocked"},
			},
			entry: "gate_redirect",
			input: map[string]any{
				"is_allowed": false,
			},
			assert: func(t *testing.T, run RunResponse, events []runtime.Event) {
				t.Helper()
				if run.Output.Vars["gate_route"] != "blocked" {
					t.Fatalf("gate_route = %v, want %q", run.Output.Vars["gate_route"], "blocked")
				}
				raw, ok := run.Output.Vars["gate_result"]
				if !ok {
					t.Fatal("expected gate_result var")
				}
				result, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("gate_result type = %T, want map[string]any", raw)
				}
				if result["passed"] != false {
					t.Fatalf("gate_result.passed = %v, want false", result["passed"])
				}
				assertEventKindsPresent(t, events, runtime.EventRouteDecision)
				if hasNodeEvent(events, runtime.EventNodeStarted, "route_allowed") {
					t.Fatal("route_allowed should not execute when gate redirects to route_blocked")
				}
			},
		},
		{
			name: "guardian",
			node: map[string]any{
				"id":   "validate_candidate",
				"type": "guardian",
				"config": map[string]any{
					"input_var":  "candidate",
					"result_var": "guardian_result",
					"checks": []any{
						map[string]any{
							"name":            "required_name",
							"type":            "required",
							"required_fields": []any{"name"},
						},
					},
				},
			},
			input: map[string]any{
				"candidate": map[string]any{
					"name": "PetalFlow",
				},
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				raw, ok := run.Output.Vars["guardian_result"]
				if !ok {
					t.Fatal("expected guardian_result var")
				}
				result, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("guardian_result type = %T, want map[string]any", raw)
				}
				if result["passed"] != true {
					t.Fatalf("guardian_result.passed = %v, want true", result["passed"])
				}
			},
		},
		{
			name: "sink",
			node: map[string]any{
				"id":   "sink_to_var",
				"type": "sink",
				"config": map[string]any{
					"result_var": "sink_result",
					"sinks": []any{
						map[string]any{
							"type": "var",
							"name": "capture",
							"config": map[string]any{
								"name": "captured_payload",
							},
						},
					},
				},
			},
			input: map[string]any{
				"topic": "workflow-node-type-coverage",
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				if _, ok := run.Output.Vars["captured_payload"]; !ok {
					t.Fatal("expected captured_payload var to be set by var sink")
				}
				if _, ok := run.Output.Vars["sink_result"]; !ok {
					t.Fatal("expected sink_result var")
				}
			},
		},
		{
			name: "noop",
			node: map[string]any{
				"id":   "noop_step",
				"type": "noop",
			},
			input: map[string]any{
				"echo": "still here",
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				if run.Output.Vars["echo"] != "still here" {
					t.Fatalf("echo = %v, want %q", run.Output.Vars["echo"], "still here")
				}
			},
		},
		{
			name: "func",
			node: map[string]any{
				"id":   "func_step",
				"type": "func",
			},
			input: map[string]any{
				"echo": "func passthrough",
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				if run.Output.Vars["echo"] != "func passthrough" {
					t.Fatalf("echo = %v, want %q", run.Output.Vars["echo"], "func passthrough")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workflowID := "node_type_" + tc.name
			payload := graphWorkflowPayload(workflowID, tc.node)
			if len(tc.nodes) > 0 {
				payload = graphWorkflowPayloadFromParts(workflowID, tc.nodes, tc.edges, tc.entry)
			}
			createGraphWorkflow(t, handler, payload)

			run := runWorkflow(t, handler, workflowID, tc.input)
			if run.Status != "completed" {
				t.Fatalf("run status = %q, want %q", run.Status, "completed")
			}
			if run.RunID == "" {
				t.Fatal("run_id should not be empty")
			}

			events := getRunEvents(t, handler, run.RunID)
			assertRunLifecycleEvents(t, events, run.RunID)
			tc.assert(t, run, events)
		})
	}
}

func TestWorkflowNodeTypesE2E_DaemonAPI_ExpectedHydrateGaps(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)

	cases := []struct {
		name          string
		nodeType      string
		config        map[string]any
		wantErrorCode string
		wantMessage   string
	}{
		{
			name:          "human_requires_handler_binding",
			nodeType:      "human",
			config:        map[string]any{"mode": "approval", "prompt": "approve?", "output_var": "approval"},
			wantErrorCode: "HYDRATE_ERROR",
			wantMessage:   "requires a HumanHandler",
		},
		{
			name:          "map_requires_mapper_binding",
			nodeType:      "map",
			config:        map[string]any{"input_var": "items", "output_var": "mapped"},
			wantErrorCode: "HYDRATE_ERROR",
			wantMessage:   "map node hydration requires a mapper binding",
		},
		{
			name:          "cache_requires_wrapped_node_binding",
			nodeType:      "cache",
			config:        map[string]any{"output_key": "cached"},
			wantErrorCode: "HYDRATE_ERROR",
			wantMessage:   "cache node hydration requires a wrapped node binding",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workflowID := "node_type_gap_" + tc.nodeType
			createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
				"id":     "node",
				"type":   tc.nodeType,
				"config": tc.config,
			}))

			errBody := runWorkflowExpectError(t, handler, workflowID, map[string]any{
				"items": []any{1, 2, 3},
			}, http.StatusUnprocessableEntity)

			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(errBody, &payload); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if payload.Error.Code != tc.wantErrorCode {
				t.Fatalf("error code = %q, want %q (body=%s)", payload.Error.Code, tc.wantErrorCode, string(errBody))
			}
			if !strings.Contains(payload.Error.Message, tc.wantMessage) {
				t.Fatalf("error message = %q, want substring %q", payload.Error.Message, tc.wantMessage)
			}
		})
	}
}

func createGraphWorkflow(t *testing.T, handler http.Handler, payload map[string]any) WorkflowRecord {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(mustJSON(t, payload)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create graph workflow failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var rec WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	return rec
}

func runWorkflowExpectError(
	t *testing.T,
	handler http.Handler,
	id string,
	input map[string]any,
	wantStatus int,
) []byte {
	t.Helper()

	body := mustJSON(t, RunRequest{
		Input:   input,
		Options: RunReqOptions{Timeout: "30s"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/"+id+"/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != wantStatus {
		t.Fatalf("run workflow status=%d, want %d body=%s", w.Code, wantStatus, w.Body.String())
	}
	return w.Body.Bytes()
}

func graphWorkflowPayload(workflowID string, node map[string]any) map[string]any {
	nodeID := fmt.Sprintf("%v", node["id"])
	return map[string]any{
		"id":      workflowID,
		"version": "1.0",
		"nodes":   []any{node},
		"edges":   []any{},
		"entry":   nodeID,
	}
}

func graphWorkflowPayloadFromParts(
	workflowID string,
	nodes []map[string]any,
	edges []map[string]any,
	entry string,
) map[string]any {
	return map[string]any{
		"id":      workflowID,
		"version": "1.0",
		"nodes":   nodes,
		"edges":   edges,
		"entry":   entry,
	}
}

func routeDecisionTargets(decision map[string]any) []any {
	if targets, ok := decision["targets"].([]any); ok {
		return targets
	}
	if targets, ok := decision["Targets"].([]any); ok {
		return targets
	}
	return nil
}

func hasNodeEvent(events []runtime.Event, kind runtime.EventKind, nodeID string) bool {
	for _, event := range events {
		if event.Kind == kind && event.NodeID == nodeID {
			return true
		}
	}
	return false
}

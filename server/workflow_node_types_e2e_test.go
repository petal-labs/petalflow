package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestWorkflowNodeTypesE2E_DaemonAPI_RunCoverage(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)
	webhookCalls := 0
	webhookTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer webhookTarget.Close()

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
			name: "webhook_call",
			node: map[string]any{
				"id":   "send_webhook",
				"type": "webhook_call",
				"config": map[string]any{
					"url":        webhookTarget.URL,
					"method":     "POST",
					"result_var": "webhook_result",
					"input_vars": []any{"topic"},
				},
			},
			input: map[string]any{
				"topic": "workflow-node-type-coverage",
			},
			assert: func(t *testing.T, run RunResponse, _ []runtime.Event) {
				t.Helper()
				raw, ok := run.Output.Vars["webhook_result"]
				if !ok {
					t.Fatal("expected webhook_result var")
				}
				result, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("webhook_result type = %T, want map[string]any", raw)
				}
				if result["ok"] != true {
					t.Fatalf("webhook_result.ok = %v, want true", result["ok"])
				}
				if webhookCalls == 0 {
					t.Fatal("expected webhook target to receive at least one call")
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

func TestWorkflowNodeTypesE2E_DaemonAPI_BindingCoverage(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)

	t.Run("human_auto_approve", func(t *testing.T) {
		workflowID := "node_type_human_bound"
		createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
			"id":   "human_gate",
			"type": "human",
			"config": map[string]any{
				"mode":       "approval",
				"prompt":     "approve release?",
				"output_var": "approval",
			},
		}))

		run := runWorkflowWithOptions(t, handler, workflowID, map[string]any{
			"topic": "issue-113",
		}, RunReqOptions{
			Timeout: "30s",
			Human: &RunReqHumanOptions{
				Mode:        "auto_approve",
				RespondedBy: "node-types-e2e",
			},
		})
		if run.Status != "completed" {
			t.Fatalf("run status = %q, want completed", run.Status)
		}
		if approved, _ := run.Output.Vars["approval_approved"].(bool); !approved {
			t.Fatalf("approval_approved = %v, want true", run.Output.Vars["approval_approved"])
		}
		raw, ok := run.Output.Vars["approval"]
		if !ok {
			t.Fatal("expected approval response output var")
		}
		resp, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("approval type = %T, want map[string]any", raw)
		}
		if resp["responded_by"] != "node-types-e2e" {
			t.Fatalf("responded_by = %v, want %q", resp["responded_by"], "node-types-e2e")
		}

		events := getRunEvents(t, handler, run.RunID)
		assertRunLifecycleEvents(t, events, run.RunID)
		if !hasNodeEvent(events, runtime.EventNodeFinished, "human_gate") {
			t.Fatal("expected human_gate node to complete")
		}
	})

	t.Run("map_mapper_binding", func(t *testing.T) {
		workflowID := "node_type_map_bound"
		createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
			"id":   "map_items",
			"type": "map",
			"config": map[string]any{
				"input_var":  "items",
				"output_var": "mapped",
				"item_var":   "item",
				"mapper_binding": map[string]any{
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "item={{.item.name}}",
						"output_var": "label",
					},
				},
			},
		}))

		run := runWorkflowWithOptions(t, handler, workflowID, map[string]any{
			"items": []any{
				map[string]any{"name": "alpha"},
				map[string]any{"name": "beta"},
			},
		}, RunReqOptions{Timeout: "30s"})
		if run.Status != "completed" {
			t.Fatalf("run status = %q, want completed", run.Status)
		}

		raw, ok := run.Output.Vars["mapped"]
		if !ok {
			t.Fatal("expected mapped output var")
		}
		mapped, ok := raw.([]any)
		if !ok {
			t.Fatalf("mapped type = %T, want []any", raw)
		}
		if len(mapped) != 2 {
			t.Fatalf("mapped len = %d, want 2", len(mapped))
		}
		first, ok := mapped[0].(map[string]any)
		if !ok {
			t.Fatalf("mapped[0] type = %T, want map[string]any", mapped[0])
		}
		if first["label"] != "item=alpha" {
			t.Fatalf("mapped[0].label = %v, want %q", first["label"], "item=alpha")
		}

		events := getRunEvents(t, handler, run.RunID)
		assertRunLifecycleEvents(t, events, run.RunID)
		if !hasNodeEvent(events, runtime.EventNodeFinished, "map_items") {
			t.Fatal("expected map_items node to complete")
		}
	})

	t.Run("cache_wrapped_binding", func(t *testing.T) {
		workflowID := "node_type_cache_bound"
		createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
			"id":   "cache_step",
			"type": "cache",
			"config": map[string]any{
				"output_var": "cache_meta",
				"cache_key":  "fixed-key",
				"wrapped_binding": map[string]any{
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "payload={{.value}}",
						"output_var": "cache_payload",
					},
				},
			},
		}))

		run := runWorkflowWithOptions(t, handler, workflowID, map[string]any{
			"value": "first",
		}, RunReqOptions{Timeout: "30s"})

		meta, ok := run.Output.Vars["cache_meta"].(map[string]any)
		if !ok {
			t.Fatalf("cache_meta type = %T, want map[string]any", run.Output.Vars["cache_meta"])
		}
		if hit, _ := meta["hit"].(bool); hit {
			t.Fatalf("cache_meta.hit = %v, want false", meta["hit"])
		}
		if run.Output.Vars["cache_payload"] != "payload=first" {
			t.Fatalf("cache_payload = %v, want %q", run.Output.Vars["cache_payload"], "payload=first")
		}

		events := getRunEvents(t, handler, run.RunID)
		assertRunLifecycleEvents(t, events, run.RunID)
		if !hasNodeEvent(events, runtime.EventNodeFinished, "cache_step") {
			t.Fatal("expected cache_step node to complete")
		}
	})

	t.Run("human_strict_mode_runtime_error", func(t *testing.T) {
		workflowID := "node_type_human_strict"
		createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
			"id":   "human_gate",
			"type": "human",
			"config": map[string]any{
				"mode":       "approval",
				"prompt":     "approve release?",
				"output_var": "approval",
			},
		}))

		errBody := runWorkflowExpectError(t, handler, workflowID, nil, http.StatusInternalServerError)
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(errBody, &payload); err != nil {
			t.Fatalf("unmarshal strict mode error response: %v", err)
		}
		if payload.Error.Code != "RUNTIME_ERROR" {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, "RUNTIME_ERROR")
		}
		if payload.Error.Message == "" {
			t.Fatal("strict mode runtime error message should not be empty")
		}
	})
}

func TestWorkflowNodeTypesE2E_DaemonAPI_BindingCoverage_WithTracing(t *testing.T) {
	handler, spans := newDaemonWorkflowLifecycleHandlerWithTracing(t)

	workflowID := "node_type_bound_tracing"
	createGraphWorkflow(t, handler, graphWorkflowPayloadFromParts(workflowID, []map[string]any{
		{
			"id":   "map_items",
			"type": "map",
			"config": map[string]any{
				"input_var":  "items",
				"output_var": "mapped",
				"item_var":   "item",
				"mapper_binding": map[string]any{
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "item={{.item.name}}",
						"output_var": "label",
					},
				},
			},
		},
		{
			"id":   "cache_step",
			"type": "cache",
			"config": map[string]any{
				"output_var": "cache_meta",
				"cache_key":  "bound-tracing",
				"wrapped_binding": map[string]any{
					"type": "transform",
					"config": map[string]any{
						"transform":  "template",
						"template":   "cache-ready",
						"output_var": "cache_payload",
					},
				},
			},
		},
		{
			"id":   "human_gate",
			"type": "human",
			"config": map[string]any{
				"mode":       "approval",
				"prompt":     "approve?",
				"output_var": "approval",
			},
		},
	}, []map[string]any{
		{"source": "map_items", "target": "cache_step"},
		{"source": "cache_step", "target": "human_gate"},
	}, "map_items"))

	run := runWorkflowWithOptions(t, handler, workflowID, map[string]any{
		"items": []any{
			map[string]any{"name": "alpha"},
		},
	}, RunReqOptions{
		Timeout: "30s",
		Human: &RunReqHumanOptions{
			Mode: "auto_approve",
		},
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want completed", run.Status)
	}

	events := getRunEvents(t, handler, run.RunID)
	assertRunLifecycleEvents(t, events, run.RunID)

	for _, nodeID := range []string{"map_items", "cache_step", "human_gate"} {
		if !hasNodeEvent(events, runtime.EventNodeFinished, nodeID) {
			t.Fatalf("expected node %q to emit node.finished", nodeID)
		}
	}

	var traced []runtime.Event
	for _, event := range events {
		if event.TraceID != "" || event.SpanID != "" {
			traced = append(traced, event)
		}
	}
	if len(traced) == 0 {
		t.Fatal("expected at least one traced event")
	}
	traceID := traced[0].TraceID
	for i, event := range traced {
		if event.TraceID == "" || event.SpanID == "" {
			t.Fatalf("traced event[%d] missing trace metadata (trace_id=%q span_id=%q)", i, event.TraceID, event.SpanID)
		}
		if traceID != "" && event.TraceID != traceID {
			t.Fatalf("traced event[%d] trace_id=%q, want %q", i, event.TraceID, traceID)
		}
	}

	if ended := spans.Ended(); len(ended) < 2 {
		t.Fatalf("ended span count = %d, want >= 2", len(ended))
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

func runWorkflowWithOptions(
	t *testing.T,
	handler http.Handler,
	id string,
	input map[string]any,
	options RunReqOptions,
) RunResponse {
	t.Helper()

	if options.Timeout == "" {
		options.Timeout = "30s"
	}
	body := mustJSON(t, RunRequest{
		Input:   input,
		Options: options,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/workflows/"+id+"/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("run workflow status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp RunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	return resp
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

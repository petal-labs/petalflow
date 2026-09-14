package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
)

// memoryWorkflow recalls history + knowledge, echoes the assembled context
// through a transform, and stores the turn back to memory.
func memoryWorkflow() map[string]any {
	return map[string]any{
		"id": "memory-workflow", "version": "1.0",
		"nodes": []map[string]any{
			{"id": "recall", "type": "memory_recall", "config": map[string]any{
				"query_var": "question", "output_var": "context", "record_messages": true,
				"budget": map[string]any{"max_artifacts": 1},
			}},
			{"id": "reply", "type": "transform", "config": map[string]any{
				"transform": "template", "template": "Answer to {{.question}}", "output_var": "answer",
			}},
			{"id": "store", "type": "memory_store", "config": map[string]any{
				"entries": []map[string]any{
					{"role": "user", "var": "question"},
					{"role": "assistant", "var": "answer"},
				},
			}},
		},
		"edges": []map[string]any{{"source": "recall", "target": "reply"}, {"source": "reply", "target": "store"}},
		"entry": "recall",
	}
}

func TestRun_MemoryScopeFlowsThroughHTTP(t *testing.T) {
	provider := memory.NewInMemoryProvider()
	tenantScope := memory.Scope{TenantID: "tenant-a", Namespace: "support"}
	if err := provider.AddDocument(tenantScope, "", core.Artifact{ID: "doc", Text: "Refunds take five business days", Meta: map[string]any{"title": "Refunds"}}); err != nil {
		t.Fatal(err)
	}

	workflowStore := newTestSQLiteStore(t)
	auth := security.BearerTokenAuthenticator("token", security.Identity{Subject: "api", TenantID: "tenant-a"})
	var events []runtime.Event
	srv := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore,
		Providers: hydrate.ProviderMap{}, Bus: bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t), RunStore: runtime.NewMemoryRunStore(),
		Memory:        MemoryConfig{Provider: provider},
		RuntimeEvents: func(e runtime.Event) { events = append(events, e) },
		Security:      SecurityConfig{RequireAuth: true, Authenticator: auth},
	})
	handler := srv.Handler()
	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		return authedJSONRequest(t, handler, method, path, body, "token")
	}

	create := authed(http.MethodPost, "/api/workflows/graph", memoryWorkflow())
	if create.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", create.Code, create.Body.String())
	}

	run := func(question string) RunResponse {
		t.Helper()
		resp := authed(http.MethodPost, "/api/workflows/memory-workflow/run", map[string]any{
			"input":   map[string]any{"question": question},
			"options": map[string]any{"namespace": "support", "session_id": "sess-1"},
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("run = %d %s", resp.Code, resp.Body.String())
		}
		var out RunResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := run("How long do refunds take?")
	if ctx, _ := first.Output.Vars["context"].(string); ctx == "" || !contains(ctx, "Refunds") {
		t.Fatalf("first run context = %q", ctx)
	}
	if len(first.Output.Messages) != 0 {
		t.Fatalf("first run should have no recalled history: %+v", first.Output.Messages)
	}

	second := run("And for exchanges?")
	if len(second.Output.Messages) != 2 || second.Output.Messages[0].Content != "How long do refunds take?" {
		t.Fatalf("second run should recall the first turn: %+v", second.Output.Messages)
	}

	// The provider partitioned by the authenticated tenant, not a body value.
	recalled, err := provider.Recall(context.Background(), memory.RecallRequest{Scope: memory.Scope{TenantID: "tenant-a", Namespace: "support", SessionID: "sess-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled.Messages) != 4 {
		t.Fatalf("stored turns = %d, want 4", len(recalled.Messages))
	}
	other, _ := provider.Recall(context.Background(), memory.RecallRequest{Scope: memory.Scope{Namespace: "support", SessionID: "sess-1"}})
	if len(other.Messages) != 0 {
		t.Fatal("memory must be isolated by tenant")
	}

	var started, recalls, stores int
	for _, e := range events {
		switch e.Kind {
		case runtime.EventRunStarted:
			started++
			if e.Payload["namespace"] != "support" || e.Payload["session_id"] != "sess-1" {
				t.Fatalf("run.started payload = %v", e.Payload)
			}
		case runtime.EventMemoryRecall:
			recalls++
			if _, has := e.Payload["query"]; has {
				t.Fatal("query content must not be recorded")
			}
		case runtime.EventMemoryStore:
			stores++
		}
	}
	if started != 2 || recalls != 2 || stores != 2 {
		t.Fatalf("events started=%d recalls=%d stores=%d", started, recalls, stores)
	}
}

func TestRun_MemoryScopeValidationAndHydration(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	auth := security.BearerTokenAuthenticator("token", security.Identity{Subject: "api", TenantID: "tenant-a"})

	// Without a provider, memory nodes are rejected at hydration.
	noMemory := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore, Providers: hydrate.ProviderMap{},
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	})
	handler := noMemory.Handler()
	if resp := authedJSONRequest(t, handler, http.MethodPost, "/api/workflows/graph", memoryWorkflow(), "token"); resp.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.Code, resp.Body.String())
	}
	resp := authedJSONRequest(t, handler, http.MethodPost, "/api/workflows/memory-workflow/run", map[string]any{
		"options": map[string]any{"namespace": "support", "session_id": "sess-1"},
	}, "token")
	if resp.Code != http.StatusUnprocessableEntity || !contains(resp.Body.String(), "HYDRATE_ERROR") {
		t.Fatalf("run without provider = %d %s", resp.Code, resp.Body.String())
	}

	withMemory := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore, Providers: hydrate.ProviderMap{},
		Memory:   MemoryConfig{Provider: memory.NewInMemoryProvider()},
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	})
	handler = withMemory.Handler()

	// A malformed scope is a 400 before anything runs.
	resp = authedJSONRequest(t, handler, http.MethodPost, "/api/workflows/memory-workflow/run", map[string]any{
		"options": map[string]any{"session_id": "sess\n1"},
	}, "token")
	if resp.Code != http.StatusBadRequest || !contains(resp.Body.String(), "INVALID_SCOPE") {
		t.Fatalf("malformed scope = %d %s", resp.Code, resp.Body.String())
	}

	// A run without a session cannot use conversation memory: the node's
	// invalid-scope error is reported as a client error, not a server fault.
	resp = authedJSONRequest(t, handler, http.MethodPost, "/api/workflows/memory-workflow/run", map[string]any{
		"input": map[string]any{"question": "hi"},
	}, "token")
	if resp.Code != http.StatusBadRequest || !contains(resp.Body.String(), "INVALID_SCOPE") {
		t.Fatalf("run without scope = %d %s", resp.Code, resp.Body.String())
	}
}

func authedJSONRequest(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(mustJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

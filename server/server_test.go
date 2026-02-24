package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
)

// testServer creates a Server with defaults suitable for testing.
func testServer(t *testing.T) *Server {
	t.Helper()
	workflowStore := newTestSQLiteStore(t)

	return NewServer(ServerConfig{
		Store:         workflowStore,
		ScheduleStore: workflowStore,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t),
		CORSOrigin: "*",
		MaxBody:    1 << 20,
	})
}

func TestHealth(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("got status %q, want %q", body["status"], "ok")
	}
}

func TestCORSHeaders(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS origin = %q, want %q", got, "*")
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodOptions, "/api/workflows", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestMaxBody(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     newTestWorkflowStore(t),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		MaxBody: 10, // 10 bytes
	})

	// Send a body larger than 10 bytes
	bigBody := strings.Repeat("x", 100)
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", strings.NewReader(bigBody))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestNodeTypes(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/node-types", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var types []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &types); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(types) == 0 {
		t.Fatal("expected at least one node type from registry")
	}
}

// validGraphJSON returns a minimal valid graph definition.
func validGraphJSON(id string) []byte {
	gd := map[string]any{
		"id":      id,
		"version": "1.0",
		"nodes": []map[string]any{
			{"id": "start", "type": "func"},
		},
		"edges": []map[string]any{},
		"entry": "start",
	}
	b, _ := json.Marshal(gd)
	return b
}

func validWebhookGraphJSON(id string, methods []string, auth map[string]any) []byte {
	methodVals := make([]any, 0, len(methods))
	for _, method := range methods {
		methodVals = append(methodVals, method)
	}

	triggerConfig := map[string]any{
		"methods": methodVals,
	}
	if auth != nil {
		triggerConfig["auth"] = auth
	}

	gd := map[string]any{
		"id":      id,
		"version": "1.0",
		"nodes": []map[string]any{
			{
				"id":     "incoming",
				"type":   "webhook_trigger",
				"config": triggerConfig,
			},
			{
				"id":   "extract_event",
				"type": "transform",
				"config": map[string]any{
					"transform":  "template",
					"template":   "{{.webhook_body.event}}",
					"output_var": "event_name",
				},
			},
		},
		"edges": []map[string]any{
			{"source": "incoming", "target": "extract_event"},
		},
		"entry": "extract_event",
	}
	b, _ := json.Marshal(gd)
	return b
}

func TestGraphWorkflow_CRUD(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	// POST /api/workflows/graph → 201
	body := validGraphJSON("test-graph")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST graph: got %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var created WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.ID != "test-graph" {
		t.Fatalf("created.ID = %q, want %q", created.ID, "test-graph")
	}

	// POST duplicate → 409
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(body))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("POST duplicate: got %d, want %d", w.Code, http.StatusConflict)
	}

	// GET /api/workflows/test-graph → 200
	r = httptest.NewRequest(http.MethodGet, "/api/workflows/test-graph", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d, want %d", w.Code, http.StatusOK)
	}

	// GET missing → 404
	r = httptest.NewRequest(http.MethodGet, "/api/workflows/nonexistent", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET missing: got %d, want %d", w.Code, http.StatusNotFound)
	}

	// GET /api/workflows → 200 with 1 item
	r = httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("LIST: got %d, want %d", w.Code, http.StatusOK)
	}
	var list []WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("LIST: got %d items, want 1", len(list))
	}

	// PUT /api/workflows/test-graph → 200
	updatedBody := validGraphJSON("test-graph")
	r = httptest.NewRequest(http.MethodPut, "/api/workflows/test-graph", bytes.NewReader(updatedBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// DELETE /api/workflows/test-graph → 204
	r = httptest.NewRequest(http.MethodDelete, "/api/workflows/test-graph", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: got %d, want %d", w.Code, http.StatusNoContent)
	}

	// DELETE missing → 404
	r = httptest.NewRequest(http.MethodDelete, "/api/workflows/test-graph", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGraphWorkflow_ValidationError(t *testing.T) {
	srv := testServer(t)

	// Graph with edge referencing unknown node -> validation error
	bad := map[string]any{
		"id":      "bad",
		"version": "1.0",
		"nodes": []map[string]any{
			{"id": "a", "type": "func"},
		},
		"edges": []map[string]any{
			{"source": "a", "sourceHandle": "out", "target": "ghost", "targetHandle": "in"},
		},
	}
	body, _ := json.Marshal(bad)

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d; body: %s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
}

func TestGraphWorkflow_InvalidJSON(t *testing.T) {
	srv := testServer(t)

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRunWorkflow_NotFound(t *testing.T) {
	srv := testServer(t)

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/missing/run", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRunWorkflow_WebhookTriggerSuccess(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	workflowID := "webhook-success"
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(validWebhookGraphJSON(workflowID, []string{"POST"}, nil)))
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create workflow status = %d, want %d body=%s", createW.Code, http.StatusCreated, createW.Body.String())
	}

	webhookBody := `{"event":"order.created","id":"evt_123"}`
	runReq := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/webhooks/incoming", strings.NewReader(webhookBody))
	runReq.Header.Set("Content-Type", "application/json")
	runW := httptest.NewRecorder()
	handler.ServeHTTP(runW, runReq)
	if runW.Code != http.StatusOK {
		t.Fatalf("webhook run status = %d, want %d body=%s", runW.Code, http.StatusOK, runW.Body.String())
	}

	var resp RunResponse
	if err := json.Unmarshal(runW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	if resp.RunID == "" {
		t.Fatal("run_id should not be empty")
	}
	if got := resp.Output.Vars["event_name"]; got != "order.created" {
		t.Fatalf("event_name = %v, want order.created", got)
	}

	rawBody, ok := resp.Output.Vars["webhook_body"]
	if !ok {
		t.Fatal("expected webhook_body var in run output")
	}
	bodyMap, ok := rawBody.(map[string]any)
	if !ok || bodyMap["event"] != "order.created" {
		t.Fatalf("webhook_body = %#v, want event=order.created", rawBody)
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+resp.RunID+"/events", nil)
	eventsW := httptest.NewRecorder()
	handler.ServeHTTP(eventsW, eventsReq)
	if eventsW.Code != http.StatusOK {
		t.Fatalf("events status = %d, want 200 body=%s", eventsW.Code, eventsW.Body.String())
	}
	eventsBody := eventsW.Body.String()
	if !strings.Contains(eventsBody, `"trigger":"webhook"`) {
		t.Fatalf("expected webhook trigger metadata in events: %s", eventsBody)
	}
	if !strings.Contains(eventsBody, `"webhook_trigger_id":"incoming"`) {
		t.Fatalf("expected webhook_trigger_id metadata in events: %s", eventsBody)
	}
}

func TestRunWorkflow_WebhookTriggerMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	workflowID := "webhook-method"
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(validWebhookGraphJSON(workflowID, []string{"PUT"}, nil)))
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create workflow status = %d, want %d body=%s", createW.Code, http.StatusCreated, createW.Body.String())
	}

	runReq := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/webhooks/incoming", strings.NewReader(`{"event":"x"}`))
	runReq.Header.Set("Content-Type", "application/json")
	runW := httptest.NewRecorder()
	handler.ServeHTTP(runW, runReq)
	if runW.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d body=%s", runW.Code, http.StatusMethodNotAllowed, runW.Body.String())
	}
	if !strings.Contains(runW.Body.String(), `"METHOD_NOT_ALLOWED"`) {
		t.Fatalf("expected METHOD_NOT_ALLOWED code, body=%s", runW.Body.String())
	}
}

func TestRunWorkflow_WebhookTriggerHeaderTokenAuth(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	t.Setenv("PETALFLOW_WEBHOOK_TEST_TOKEN", "secret-token")
	authCfg := map[string]any{
		"type":   "header_token",
		"header": "X-Test-Webhook-Token",
		"token":  "env:PETALFLOW_WEBHOOK_TEST_TOKEN",
	}

	workflowID := "webhook-auth"
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(validWebhookGraphJSON(workflowID, []string{"POST"}, authCfg)))
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create workflow status = %d, want %d body=%s", createW.Code, http.StatusCreated, createW.Body.String())
	}

	missingAuthReq := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/webhooks/incoming", strings.NewReader(`{"event":"x"}`))
	missingAuthReq.Header.Set("Content-Type", "application/json")
	missingAuthW := httptest.NewRecorder()
	handler.ServeHTTP(missingAuthW, missingAuthReq)
	if missingAuthW.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want %d body=%s", missingAuthW.Code, http.StatusUnauthorized, missingAuthW.Body.String())
	}

	authedReq := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/webhooks/incoming", strings.NewReader(`{"event":"x"}`))
	authedReq.Header.Set("Content-Type", "application/json")
	authedReq.Header.Set("X-Test-Webhook-Token", "secret-token")
	authedW := httptest.NewRecorder()
	handler.ServeHTTP(authedW, authedReq)
	if authedW.Code != http.StatusOK {
		t.Fatalf("authed status = %d, want %d body=%s", authedW.Code, http.StatusOK, authedW.Body.String())
	}
}

func TestRunWorkflow_WithFuncNode(t *testing.T) {
	store := newTestWorkflowStore(t)

	// Create a graph with a single FuncNode that sets output
	gd := map[string]any{
		"id":      "run-test",
		"version": "1.0",
		"nodes": []map[string]any{
			{"id": "echo", "type": "func"},
		},
		"edges": []map[string]any{},
		"entry": "echo",
	}
	gdBytes, _ := json.Marshal(gd)

	srv := NewServer(ServerConfig{
		Store:     store,
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t),
	})
	handler := srv.Handler()

	// Create workflow
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(gdBytes))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// Run workflow
	runBody, _ := json.Marshal(RunRequest{
		Input: map[string]any{"greeting": "hello"},
	})
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/run-test/run", bytes.NewReader(runBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("run: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp RunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("run status = %q, want %q", resp.Status, "completed")
	}
	if resp.RunID == "" {
		t.Fatal("run_id should not be empty")
	}
	// Input vars should be present in output
	if resp.Output.Vars["greeting"] != "hello" {
		t.Fatalf("output.vars[greeting] = %v, want %q", resp.Output.Vars["greeting"], "hello")
	}
}

func TestRunWorkflow_UsesProviderConfiguredViaAPI(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	providerStore, err := NewProviderSQLiteStore(workflowStore.DB())
	if err != nil {
		t.Fatalf("NewProviderSQLiteStore() error = %v", err)
	}

	srv := NewServer(ServerConfig{
		Store:         workflowStore,
		ScheduleStore: workflowStore,
		ProviderStore: providerStore,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			if name != "anthropic" {
				return nil, fmt.Errorf("unexpected provider %q", name)
			}
			if cfg.APIKey != "sk-ant-test" {
				return nil, fmt.Errorf("unexpected api key %q", cfg.APIKey)
			}
			return &workflowLifecycleLLMClient{provider: name}, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t),
	})
	handler := srv.Handler()

	createProviderBody := map[string]any{
		"type":          "anthropic",
		"name":          "My Anthropic",
		"default_model": "claude-sonnet-4-20250514",
		"api_key":       "sk-ant-test",
	}
	createProviderReq := httptest.NewRequest(http.MethodPost, "/api/providers", bytes.NewReader(mustJSON(t, createProviderBody)))
	createProviderReq.Header.Set("Content-Type", "application/json")
	createProviderW := httptest.NewRecorder()
	handler.ServeHTTP(createProviderW, createProviderReq)
	if createProviderW.Code != http.StatusCreated {
		t.Fatalf("create provider failed: status=%d body=%s", createProviderW.Code, createProviderW.Body.String())
	}

	wf := agent.AgentWorkflow{
		Version: "1.0.0",
		Kind:    "agent_workflow",
		ID:      "hello-petalflow",
		Name:    "Hello PetalFlow",
		Agents: map[string]agent.Agent{
			"greeter": {
				Role:     "Friendly Greeter",
				Goal:     "Welcome users",
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
			},
		},
		Tasks: map[string]agent.Task{
			"greet": {
				Description:    "Say hello to {{input.name}}.",
				Agent:          "greeter",
				ExpectedOutput: "A welcome message",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"greet"},
		},
	}
	postAgentWorkflow(t, handler, wf)

	runResp := runWorkflow(t, handler, wf.ID, map[string]any{"name": "PetalFlow"})
	if runResp.Status != "completed" {
		t.Fatalf("run status = %q, want completed", runResp.Status)
	}
}

func TestRunWorkflow_UsesPersistedProviderAPIKeyAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "petalflow.sqlite")

	workflowStore1, err := NewSQLiteStore(SQLiteStoreConfig{DSN: dbPath})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store1) error = %v", err)
	}
	providerStore1, err := NewProviderSQLiteStore(workflowStore1.DB())
	if err != nil {
		t.Fatalf("NewProviderSQLiteStore(store1) error = %v", err)
	}

	srv1 := NewServer(ServerConfig{
		Store:         workflowStore1,
		ScheduleStore: workflowStore1,
		ProviderStore: providerStore1,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			if name != "anthropic" {
				return nil, fmt.Errorf("unexpected provider %q", name)
			}
			if cfg.APIKey != "sk-ant-restart" {
				return nil, fmt.Errorf("unexpected api key %q", cfg.APIKey)
			}
			return &workflowLifecycleLLMClient{provider: name}, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t),
	})
	handler1 := srv1.Handler()

	createProviderBody := map[string]any{
		"type":          "anthropic",
		"name":          "My Anthropic",
		"default_model": "claude-sonnet-4-20250514",
		"api_key":       "sk-ant-restart",
	}
	createProviderReq := httptest.NewRequest(http.MethodPost, "/api/providers", bytes.NewReader(mustJSON(t, createProviderBody)))
	createProviderReq.Header.Set("Content-Type", "application/json")
	createProviderW := httptest.NewRecorder()
	handler1.ServeHTTP(createProviderW, createProviderReq)
	if createProviderW.Code != http.StatusCreated {
		t.Fatalf("create provider failed: status=%d body=%s", createProviderW.Code, createProviderW.Body.String())
	}

	wf := agent.AgentWorkflow{
		Version: "1.0.0",
		Kind:    "agent_workflow",
		ID:      "hello-petalflow-restart",
		Name:    "Hello PetalFlow",
		Agents: map[string]agent.Agent{
			"greeter": {
				Role:     "Friendly Greeter",
				Goal:     "Welcome users",
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
			},
		},
		Tasks: map[string]agent.Task{
			"greet": {
				Description:    "Say hello to {{input.name}}.",
				Agent:          "greeter",
				ExpectedOutput: "A welcome message",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"greet"},
		},
	}
	postAgentWorkflow(t, handler1, wf)

	if err := workflowStore1.Close(); err != nil {
		t.Fatalf("workflowStore1.Close() error = %v", err)
	}

	workflowStore2, err := NewSQLiteStore(SQLiteStoreConfig{DSN: dbPath})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store2) error = %v", err)
	}
	t.Cleanup(func() { _ = workflowStore2.Close() })

	providerStore2, err := NewProviderSQLiteStore(workflowStore2.DB())
	if err != nil {
		t.Fatalf("NewProviderSQLiteStore(store2) error = %v", err)
	}

	srv2 := NewServer(ServerConfig{
		Store:         workflowStore2,
		ScheduleStore: workflowStore2,
		ProviderStore: providerStore2,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			if name != "anthropic" {
				return nil, fmt.Errorf("unexpected provider %q", name)
			}
			if cfg.APIKey != "sk-ant-restart" {
				return nil, fmt.Errorf("unexpected api key %q", cfg.APIKey)
			}
			return &workflowLifecycleLLMClient{provider: name}, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t),
	})
	handler2 := srv2.Handler()

	runResp := runWorkflow(t, handler2, wf.ID, map[string]any{"name": "PetalFlow"})
	if runResp.Status != "completed" {
		t.Fatalf("run status = %q, want completed", runResp.Status)
	}
}

func TestRunWorkflow_StreamNoBus_EmitsCompletionEvent(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     newTestWorkflowStore(t),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		CORSOrigin: "*",
		MaxBody:    1 << 20,
	})
	handler := srv.Handler()

	workflowBody := validGraphJSON("stream-no-bus")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(workflowBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	runBody, _ := json.Marshal(RunRequest{
		Options: RunReqOptions{Stream: true},
	})
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/stream-no-bus/run", bytes.NewReader(runBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("stream run: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: run.started") {
		t.Fatalf("expected run.started event in stream body: %s", body)
	}
	if !strings.Contains(body, "event: run.finished") {
		t.Fatalf("expected run.finished event in stream body: %s", body)
	}
	if strings.Contains(body, "event: run.error") {
		t.Fatalf("did not expect run.error event in stream body: %s", body)
	}
}

func TestRunWorkflow_StreamWithBus_EmitsCompletionEvent(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	workflowBody := validGraphJSON("stream-with-bus")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(workflowBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	runBody, _ := json.Marshal(RunRequest{
		Options: RunReqOptions{Stream: true},
	})
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/stream-with-bus/run", bytes.NewReader(runBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("stream run: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: run.started") {
		t.Fatalf("expected run.started event in stream body: %s", body)
	}
	if !strings.Contains(body, "event: run.finished") {
		t.Fatalf("expected run.finished event in stream body: %s", body)
	}
	if strings.Contains(body, "event: run.error") {
		t.Fatalf("did not expect run.error event in stream body: %s", body)
	}
}

func TestRunWorkflow_StreamWithBus_RunIDMatchesPersistedEvents(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	workflowBody := validGraphJSON("stream-with-bus-events")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(workflowBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	runBody, _ := json.Marshal(RunRequest{
		Options: RunReqOptions{Stream: true},
	})
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/stream-with-bus-events/run", bytes.NewReader(runBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("stream run: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	streamBody := w.Body.String()
	runID := firstRunIDFromSSE(streamBody)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("expected run_id in stream body, got: %s", streamBody)
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/events", nil)
	eventsW := httptest.NewRecorder()
	handler.ServeHTTP(eventsW, eventsReq)
	if eventsW.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d body=%s", eventsW.Code, http.StatusOK, eventsW.Body.String())
	}

	eventsBody := eventsW.Body.String()
	if !strings.Contains(eventsBody, `"RunID":"`+runID+`"`) {
		t.Fatalf("expected persisted events for run_id=%q, body=%s", runID, eventsBody)
	}
	if !strings.Contains(eventsBody, "event: run.started") {
		t.Fatalf("expected run.started event in persisted events stream, body=%s", eventsBody)
	}
}

func TestRunEvents_NoStore(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     newTestWorkflowStore(t),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
	})

	r := httptest.NewRequest(http.MethodGet, "/api/runs/some-run/events", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestRunHistory_ListGetExport(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	createBody := validGraphJSON("history-run-workflow")
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create workflow status = %d, want %d body=%s", createW.Code, http.StatusCreated, createW.Body.String())
	}

	runReqBody, _ := json.Marshal(RunRequest{
		Input: map[string]any{"topic": "history"},
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/workflows/history-run-workflow/run", bytes.NewReader(runReqBody))
	runW := httptest.NewRecorder()
	handler.ServeHTTP(runW, runReq)
	if runW.Code != http.StatusOK {
		t.Fatalf("run workflow status = %d, want %d body=%s", runW.Code, http.StatusOK, runW.Body.String())
	}

	var runResp RunResponse
	if err := json.Unmarshal(runW.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	if strings.TrimSpace(runResp.RunID) == "" {
		t.Fatal("run response run_id should not be empty")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list runs status = %d, want %d body=%s", listW.Code, http.StatusOK, listW.Body.String())
	}

	var listed []runHistoryItemForTest
	if err := json.Unmarshal(listW.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal list runs: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("expected at least one run in list response")
	}
	sort.Slice(listed, func(i, j int) bool {
		return listed[i].RunID < listed[j].RunID
	})

	var found *runHistoryItemForTest
	for i := range listed {
		if listed[i].RunID == runResp.RunID {
			found = &listed[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("run_id %q not found in list response: %+v", runResp.RunID, listed)
	}
	if found.Status != "completed" {
		t.Fatalf("list status = %q, want %q", found.Status, "completed")
	}
	if found.WorkflowID != "history-run-workflow" {
		t.Fatalf("list workflow_id = %q, want %q", found.WorkflowID, "history-run-workflow")
	}
	if found.StartedAt.IsZero() {
		t.Fatal("list started_at should be set")
	}
	if found.CompletedAt == nil || found.CompletedAt.IsZero() {
		t.Fatal("list completed_at should be set")
	}

	statusFilterReq := httptest.NewRequest(http.MethodGet, "/api/runs?status=completed", nil)
	statusFilterW := httptest.NewRecorder()
	handler.ServeHTTP(statusFilterW, statusFilterReq)
	if statusFilterW.Code != http.StatusOK {
		t.Fatalf("status filter runs status = %d, want %d body=%s", statusFilterW.Code, http.StatusOK, statusFilterW.Body.String())
	}
	var statusFiltered []runHistoryItemForTest
	if err := json.Unmarshal(statusFilterW.Body.Bytes(), &statusFiltered); err != nil {
		t.Fatalf("unmarshal status-filter list runs: %v", err)
	}
	if len(statusFiltered) == 0 {
		t.Fatal("expected at least one run in status filtered response")
	}

	workflowFilterReq := httptest.NewRequest(http.MethodGet, "/api/runs?workflow_id=history-run-workflow", nil)
	workflowFilterW := httptest.NewRecorder()
	handler.ServeHTTP(workflowFilterW, workflowFilterReq)
	if workflowFilterW.Code != http.StatusOK {
		t.Fatalf("workflow filter runs status = %d, want %d body=%s", workflowFilterW.Code, http.StatusOK, workflowFilterW.Body.String())
	}
	var workflowFiltered []runHistoryItemForTest
	if err := json.Unmarshal(workflowFilterW.Body.Bytes(), &workflowFiltered); err != nil {
		t.Fatalf("unmarshal workflow-filter list runs: %v", err)
	}
	if len(workflowFiltered) == 0 {
		t.Fatal("expected at least one run in workflow filtered response")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+runResp.RunID, nil)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get run status = %d, want %d body=%s", getW.Code, http.StatusOK, getW.Body.String())
	}

	var fetched runHistoryItemForTest
	if err := json.Unmarshal(getW.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal get run: %v", err)
	}
	if fetched.RunID != runResp.RunID {
		t.Fatalf("get run_id = %q, want %q", fetched.RunID, runResp.RunID)
	}
	if fetched.Status != "completed" {
		t.Fatalf("get status = %q, want %q", fetched.Status, "completed")
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+runResp.RunID+"/export", nil)
	exportW := httptest.NewRecorder()
	handler.ServeHTTP(exportW, exportReq)
	if exportW.Code != http.StatusOK {
		t.Fatalf("export run status = %d, want %d body=%s", exportW.Code, http.StatusOK, exportW.Body.String())
	}
	if contentType := exportW.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("export content-type = %q, want application/json", contentType)
	}

	var exportResp runExportForTest
	if err := json.Unmarshal(exportW.Body.Bytes(), &exportResp); err != nil {
		t.Fatalf("unmarshal export run: %v", err)
	}
	if exportResp.Run.RunID != runResp.RunID {
		t.Fatalf("export run_id = %q, want %q", exportResp.Run.RunID, runResp.RunID)
	}
	if len(exportResp.Events) == 0 {
		t.Fatal("expected exported events to be non-empty")
	}
}

func TestRunHistory_OrphanedRunningRunIsNotReportedAsRunning(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	startedAt := time.Now().Add(-90 * time.Second).UTC()
	started := runtime.NewEvent(runtime.EventRunStarted, "orphan-run")
	started.Time = startedAt
	started.Seq = 1
	started.Payload = map[string]any{
		"workflow_id": "orphan-workflow",
	}
	if err := srv.eventStore.Append(context.Background(), started); err != nil {
		t.Fatalf("append started event: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/runs/orphan-run", nil)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get run status = %d, want %d body=%s", getW.Code, http.StatusOK, getW.Body.String())
	}

	var fetched runHistoryItemForTest
	if err := json.Unmarshal(getW.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal get run: %v", err)
	}
	if fetched.Status == "running" {
		t.Fatalf("orphaned run status = %q, expected terminal status", fetched.Status)
	}
}

func TestRunHistory_ActiveRunningRunRemainsRunning(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	startedAt := time.Now().Add(-15 * time.Second).UTC()
	started := runtime.NewEvent(runtime.EventRunStarted, "active-run")
	started.Time = startedAt
	started.Seq = 1
	started.Payload = map[string]any{
		"workflow_id": "active-workflow",
	}
	if err := srv.eventStore.Append(context.Background(), started); err != nil {
		t.Fatalf("append started event: %v", err)
	}

	srv.markRunActive("active-run")
	defer srv.markRunInactive("active-run")

	getReq := httptest.NewRequest(http.MethodGet, "/api/runs/active-run", nil)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get run status = %d, want %d body=%s", getW.Code, http.StatusOK, getW.Body.String())
	}

	var fetched runHistoryItemForTest
	if err := json.Unmarshal(getW.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal get run: %v", err)
	}
	if fetched.Status != "running" {
		t.Fatalf("active run status = %q, want %q", fetched.Status, "running")
	}
}

func TestRunHistory_NoStore(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     newTestWorkflowStore(t),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
	})
	handler := srv.Handler()

	cases := []string{
		"/api/runs",
		"/api/runs/some-run",
		"/api/runs/some-run/export",
	}

	for _, path := range cases {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("GET %s status = %d, want %d", path, w.Code, http.StatusNotImplemented)
		}
	}
}

func TestRunHistory_GetAndExportMissingRun(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	for _, path := range []string{
		"/api/runs/missing-run",
		"/api/runs/missing-run/export",
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, w.Code, http.StatusNotFound)
		}
	}
}

type runHistoryItemForTest struct {
	ID          string     `json:"id"`
	RunID       string     `json:"run_id"`
	WorkflowID  string     `json:"workflow_id"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationMs  int64      `json:"duration_ms,omitempty"`
}

func firstRunIDFromSSE(body string) string {
	for _, marker := range []string{`"run_id":"`, `"RunID":"`} {
		idx := strings.Index(body, marker)
		if idx < 0 {
			continue
		}
		start := idx + len(marker)
		end := strings.Index(body[start:], `"`)
		if end < 0 {
			continue
		}
		return body[start : start+end]
	}
	return ""
}

type runExportForTest struct {
	Run    runHistoryItemForTest `json:"run"`
	Events []map[string]any      `json:"events"`
}

func TestIntegrationFlow(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	// 1. POST workflow
	body := validGraphJSON("flow-test")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d; %s", w.Code, w.Body.String())
	}

	// 2. GET workflow
	r = httptest.NewRequest(http.MethodGet, "/api/workflows/flow-test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}

	// 3. Run workflow
	runBody, _ := json.Marshal(RunRequest{Input: map[string]any{"x": 42}})
	r = httptest.NewRequest(http.MethodPost, "/api/workflows/flow-test/run", bytes.NewReader(runBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("run: %d; %s", w.Code, w.Body.String())
	}
	var resp RunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "completed" {
		t.Fatalf("run status = %q", resp.Status)
	}

	// 4. DELETE workflow
	r = httptest.NewRequest(http.MethodDelete, "/api/workflows/flow-test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}

	// 5. Verify deleted
	r = httptest.NewRequest(http.MethodGet, "/api/workflows/flow-test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", w.Code)
	}
}

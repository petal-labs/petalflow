package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// testServer creates a Server with defaults suitable for testing.
func testServer() *Server {
	return NewServer(ServerConfig{
		Store:     NewMemoryStore(),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: bus.NewMemEventStore(),
		CORSOrigin: "*",
		MaxBody:    1 << 20,
	})
}

func TestHealth(t *testing.T) {
	srv := testServer()
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
	srv := testServer()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS origin = %q, want %q", got, "*")
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := testServer()
	r := httptest.NewRequest(http.MethodOptions, "/api/workflows", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestMaxBody(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     NewMemoryStore(),
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
	srv := testServer()
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

func TestGraphWorkflow_CRUD(t *testing.T) {
	srv := testServer()
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
	srv := testServer()

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
	srv := testServer()

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRunWorkflow_NotFound(t *testing.T) {
	srv := testServer()

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/missing/run", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRunWorkflow_WithFuncNode(t *testing.T) {
	store := NewMemoryStore()

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
		EventStore: bus.NewMemEventStore(),
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

func TestRunEvents_NoStore(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     NewMemoryStore(),
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

func TestIntegrationFlow(t *testing.T) {
	srv := testServer()
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

// Suppress unused import warnings — bus.EventStore is used via bus.NewMemEventStore.
var _ = context.Background

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

type providerTestLLMClient struct {
	shouldFail bool
}

func (c *providerTestLLMClient) Complete(_ context.Context, _ core.LLMRequest) (core.LLMResponse, error) {
	if c.shouldFail {
		return core.LLMResponse{}, errors.New("authentication failed")
	}
	return core.LLMResponse{Text: "ok"}, nil
}

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

func testPersistentServer(t *testing.T, stateDBPath string) *Server {
	t.Helper()
	stateStore, err := NewSQLiteStateStore(stateDBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStateStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = stateStore.Close()
	})

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
		StateStore: stateStore,
	})
}

func TestProviderEndpoints_CRUDAndTest(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     NewMemoryStore(),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			if name == "unsupported" {
				return nil, errors.New("unsupported provider")
			}
			return &providerTestLLMClient{shouldFail: cfg.APIKey == "bad-key"}, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: bus.NewMemEventStore(),
		CORSOrigin: "*",
		MaxBody:    1 << 20,
	})
	handler := srv.Handler()

	createBody := map[string]any{
		"name":          "openai",
		"api_key":       "good-key",
		"default_model": "gpt-4o-mini",
	}
	b, _ := json.Marshal(createBody)
	r := httptest.NewRequest(http.MethodPost, "/api/providers", bytes.NewReader(b))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create provider: got %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list providers: got %d, want %d", w.Code, http.StatusOK)
	}
	var providers []providerInfo
	if err := json.Unmarshal(w.Body.Bytes(), &providers); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	if providers[0].Verified {
		t.Fatal("provider should start unverified")
	}
	if providers[0].DefaultModel != "gpt-4o-mini" {
		t.Fatalf("default_model = %q, want %q", providers[0].DefaultModel, "gpt-4o-mini")
	}

	r = httptest.NewRequest(http.MethodPost, "/api/providers/openai/test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("test provider: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var testResult providerTestResult
	if err := json.Unmarshal(w.Body.Bytes(), &testResult); err != nil {
		t.Fatalf("unmarshal test result: %v", err)
	}
	if !testResult.Success {
		t.Fatalf("expected provider test success; error=%q", testResult.Error)
	}

	updateBody := map[string]any{"api_key": "bad-key"}
	b, _ = json.Marshal(updateBody)
	r = httptest.NewRequest(http.MethodPut, "/api/providers/openai", bytes.NewReader(b))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update provider: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/api/providers/openai/test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("test provider (bad key): got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &testResult); err != nil {
		t.Fatalf("unmarshal failed test result: %v", err)
	}
	if testResult.Success {
		t.Fatal("expected provider test failure with bad key")
	}
	if testResult.Error == "" {
		t.Fatal("expected error message for failed provider test")
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/providers/openai", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete provider: got %d, want %d", w.Code, http.StatusNoContent)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/providers/openai/test", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("test deleted provider: got %d, want %d", w.Code, http.StatusNotFound)
	}
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

func TestAuthAndSettingsPersistAcrossServerRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "petalflow.db")

	srv := testPersistentServer(t, statePath)
	handler := srv.Handler()

	setupReq := authSetupRequest{Username: "admin", Password: "secret"}
	setupBody, _ := json.Marshal(setupReq)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(setupBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: got %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	settings := AppSettings{
		OnboardingComplete: true,
		OnboardingStep:     2,
		Preferences: UserPreferences{
			Theme:               "light",
			DefaultWorkflowMode: "graph",
			AutoSaveIntervalMs:  1500,
			OutputFormat:        "json",
		},
	}
	settingsBody, _ := json.Marshal(settings)
	r = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(settingsBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update settings: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	restarted := testPersistentServer(t, statePath)
	restartedHandler := restarted.Handler()

	r = httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w = httptest.NewRecorder()
	restartedHandler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	var status authStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !status.SetupComplete {
		t.Fatal("setup_complete = false, want true after restart")
	}

	loginReq := authLoginRequest{Username: "admin", Password: "secret"}
	loginBody, _ := json.Marshal(loginReq)
	r = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	w = httptest.NewRecorder()
	restartedHandler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login after restart: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w = httptest.NewRecorder()
	restartedHandler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get settings after restart: got %d, want %d", w.Code, http.StatusOK)
	}
	var got AppSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if got.OnboardingComplete != settings.OnboardingComplete {
		t.Fatalf("onboarding_complete = %v, want %v", got.OnboardingComplete, settings.OnboardingComplete)
	}
	if got.OnboardingStep != settings.OnboardingStep {
		t.Fatalf("onboarding_step = %d, want %d", got.OnboardingStep, settings.OnboardingStep)
	}
	if got.Preferences.Theme != settings.Preferences.Theme {
		t.Fatalf("theme = %q, want %q", got.Preferences.Theme, settings.Preferences.Theme)
	}
	if got.Preferences.DefaultWorkflowMode != settings.Preferences.DefaultWorkflowMode {
		t.Fatalf("default_workflow_mode = %q, want %q", got.Preferences.DefaultWorkflowMode, settings.Preferences.DefaultWorkflowMode)
	}
	if got.Preferences.AutoSaveIntervalMs != settings.Preferences.AutoSaveIntervalMs {
		t.Fatalf("auto_save_interval_ms = %d, want %d", got.Preferences.AutoSaveIntervalMs, settings.Preferences.AutoSaveIntervalMs)
	}
	if got.Preferences.OutputFormat != settings.Preferences.OutputFormat {
		t.Fatalf("output_format = %q, want %q", got.Preferences.OutputFormat, settings.Preferences.OutputFormat)
	}
}

func TestUpdateSettings_SeedsOnboardingSamples(t *testing.T) {
	srv := testServer()
	handler := srv.Handler()

	settings := AppSettings{
		OnboardingComplete: true,
		Preferences:        UserPreferences{},
	}
	settingsBody, _ := json.Marshal(settings)

	r := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(settingsBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update settings: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list workflows: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var workflows []WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &workflows); err != nil {
		t.Fatalf("unmarshal workflows: %v", err)
	}
	if len(workflows) != 3 {
		t.Fatalf("workflow count = %d, want 3", len(workflows))
	}

	wantIDs := map[string]struct{}{
		"sample_research_brief":  {},
		"sample_meeting_actions": {},
		"sample_release_notes":   {},
	}
	for _, wf := range workflows {
		if wf.SchemaKind != "agent_workflow" {
			t.Fatalf("workflow %q kind = %q, want %q", wf.ID, wf.SchemaKind, "agent_workflow")
		}
		if wf.Compiled == nil {
			t.Fatalf("workflow %q compiled graph is nil", wf.ID)
		}
		delete(wantIDs, wf.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("missing expected sample workflow IDs: %v", wantIDs)
	}

	// Re-sending onboarding_complete=true should not create duplicates.
	r = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(settingsBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("second update settings: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("second list workflows: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	workflows = nil
	if err := json.Unmarshal(w.Body.Bytes(), &workflows); err != nil {
		t.Fatalf("second unmarshal workflows: %v", err)
	}
	if len(workflows) != 3 {
		t.Fatalf("workflow count after second completion = %d, want 3", len(workflows))
	}
}

func TestUpdateSettings_DoesNotSeedSamplesWhenWorkflowsExist(t *testing.T) {
	srv := testServer()
	handler := srv.Handler()

	graphBody := validGraphJSON("existing-workflow")
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(graphBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create existing workflow: got %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	settings := AppSettings{
		OnboardingComplete: true,
		Preferences:        UserPreferences{},
	}
	settingsBody, _ := json.Marshal(settings)
	r = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(settingsBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update settings: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list workflows: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var workflows []WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &workflows); err != nil {
		t.Fatalf("unmarshal workflows: %v", err)
	}
	if len(workflows) != 1 {
		t.Fatalf("workflow count = %d, want 1", len(workflows))
	}
	if workflows[0].ID != "existing-workflow" {
		t.Fatalf("workflow ID = %q, want %q", workflows[0].ID, "existing-workflow")
	}
}

// Suppress unused import warnings — bus.EventStore is used via bus.NewMemEventStore.
var _ = context.Background

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

// handleHealth returns a simple health check response.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleNodeTypes returns all registered node types.
func (s *Server) handleNodeTypes(w http.ResponseWriter, _ *http.Request) {
	types := registry.Global().All()
	writeJSON(w, http.StatusOK, types)
}

// handleListProviders returns configured LLM providers.
func (s *Server) handleListProviders(w http.ResponseWriter, _ *http.Request) {
	s.providersMu.RLock()
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]providerInfo, 0, len(names))
	for _, name := range names {
		result = append(result, toProviderInfo(name, s.providers[name], s.providerMeta[name]))
	}
	s.providersMu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}

type providerInfo struct {
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
	BaseURL      string `json:"base_url,omitempty"`
	Verified     bool   `json:"verified"`
	LatencyMS    int64  `json:"latency_ms,omitempty"`
}

type providerCreateRequest struct {
	Name           string `json:"name"`
	APIKey         string `json:"api_key"`
	DefaultModel   string `json:"default_model"`
	BaseURL        string `json:"base_url,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
}

type providerUpdateRequest struct {
	APIKey         *string `json:"api_key,omitempty"`
	DefaultModel   *string `json:"default_model,omitempty"`
	BaseURL        *string `json:"base_url,omitempty"`
	OrganizationID *string `json:"organization_id,omitempty"`
	ProjectID      *string `json:"project_id,omitempty"`
}

type providerTestResult struct {
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

func toProviderInfo(name string, cfg hydrate.ProviderConfig, meta providerMetadata) providerInfo {
	return providerInfo{
		Name:         name,
		DefaultModel: meta.DefaultModel,
		BaseURL:      cfg.BaseURL,
		Verified:     meta.Verified,
		LatencyMS:    meta.LatencyMS,
	}
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (s *Server) snapshotProviders() hydrate.ProviderMap {
	s.providersMu.RLock()
	defer s.providersMu.RUnlock()
	out := make(hydrate.ProviderMap, len(s.providers))
	for name, cfg := range s.providers {
		out[name] = cfg
	}
	return out
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req providerCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	name := normalizeProviderName(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "provider name is required")
		return
	}

	s.providersMu.Lock()
	if _, exists := s.providers[name]; exists {
		s.providersMu.Unlock()
		writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("provider %q already exists", name))
		return
	}
	prevProviders := cloneProviderMap(s.providers)
	prevMeta := cloneProviderMetaMap(s.providerMeta)
	s.providers[name] = hydrate.ProviderConfig{
		APIKey:  req.APIKey,
		BaseURL: req.BaseURL,
	}
	s.providerMeta[name] = providerMetadata{
		DefaultModel:   req.DefaultModel,
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
	}
	cfg := s.providers[name]
	meta := s.providerMeta[name]
	s.providersMu.Unlock()
	if err := s.persistState(); err != nil {
		s.providersMu.Lock()
		s.providers = prevProviders
		s.providerMeta = prevMeta
		s.providersMu.Unlock()
		s.logger.Error("server: persist providers failed after create", "provider", name, "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist provider")
		return
	}

	writeJSON(w, http.StatusCreated, toProviderInfo(name, cfg, meta))
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	name := normalizeProviderName(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "provider name is required")
		return
	}

	var req providerUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	s.providersMu.Lock()
	cfg, exists := s.providers[name]
	if !exists {
		s.providersMu.Unlock()
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", name))
		return
	}
	prevProviders := cloneProviderMap(s.providers)
	prevMeta := cloneProviderMetaMap(s.providerMeta)
	meta := s.providerMeta[name]

	changed := false
	if req.APIKey != nil {
		cfg.APIKey = *req.APIKey
		changed = true
	}
	if req.BaseURL != nil {
		cfg.BaseURL = *req.BaseURL
		changed = true
	}
	if req.DefaultModel != nil {
		meta.DefaultModel = *req.DefaultModel
		changed = true
	}
	if req.OrganizationID != nil {
		meta.OrganizationID = *req.OrganizationID
		changed = true
	}
	if req.ProjectID != nil {
		meta.ProjectID = *req.ProjectID
		changed = true
	}
	if changed {
		meta.Verified = false
		meta.LatencyMS = 0
	}

	s.providers[name] = cfg
	s.providerMeta[name] = meta
	s.providersMu.Unlock()
	if err := s.persistState(); err != nil {
		s.providersMu.Lock()
		s.providers = prevProviders
		s.providerMeta = prevMeta
		s.providersMu.Unlock()
		s.logger.Error("server: persist providers failed after update", "provider", name, "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist provider")
		return
	}

	writeJSON(w, http.StatusOK, toProviderInfo(name, cfg, meta))
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := normalizeProviderName(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "provider name is required")
		return
	}

	s.providersMu.Lock()
	if _, exists := s.providers[name]; !exists {
		s.providersMu.Unlock()
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", name))
		return
	}
	prevProviders := cloneProviderMap(s.providers)
	prevMeta := cloneProviderMetaMap(s.providerMeta)
	delete(s.providers, name)
	delete(s.providerMeta, name)
	s.providersMu.Unlock()
	if err := s.persistState(); err != nil {
		s.providersMu.Lock()
		s.providers = prevProviders
		s.providerMeta = prevMeta
		s.providersMu.Unlock()
		s.logger.Error("server: persist providers failed after delete", "provider", name, "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist provider")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	name := normalizeProviderName(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "provider name is required")
		return
	}

	s.providersMu.RLock()
	cfg, exists := s.providers[name]
	meta := s.providerMeta[name]
	s.providersMu.RUnlock()
	if !exists {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", name))
		return
	}

	start := time.Now()
	result := providerTestResult{}

	switch {
	case s.clientFactory == nil:
		result.Error = "provider client factory is not configured"
	default:
		client, err := s.clientFactory(name, cfg)
		if err != nil {
			result.Error = err.Error()
			break
		}
		if strings.TrimSpace(meta.DefaultModel) == "" {
			result.Success = true
			break
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		maxTokens := 1
		_, err = client.Complete(ctx, core.LLMRequest{
			Model:     meta.DefaultModel,
			InputText: "ping",
			MaxTokens: &maxTokens,
		})
		cancel()
		if err != nil {
			result.Error = err.Error()
			break
		}
		result.Success = true
	}

	result.LatencyMS = time.Since(start).Milliseconds()

	s.providersMu.Lock()
	prevMeta := cloneProviderMetaMap(s.providerMeta)
	if currentMeta, ok := s.providerMeta[name]; ok {
		currentMeta.Verified = result.Success
		currentMeta.LatencyMS = result.LatencyMS
		s.providerMeta[name] = currentMeta
	}
	s.providersMu.Unlock()
	if err := s.persistState(); err != nil {
		s.providersMu.Lock()
		s.providerMeta = prevMeta
		s.providersMu.Unlock()
		s.logger.Error("server: persist providers failed after test", "provider", name, "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist provider test status")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleListRuns returns run history in reverse chronological order.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
	writeJSON(w, http.StatusOK, s.listRuns(workflowID))
}

// handleGetRun returns a single run by ID.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "run_id is required")
		return
	}
	run, ok := s.getRun(runID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("run %q not found", runID))
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleListWorkflows returns all workflows.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// handleGetWorkflow returns a single workflow by ID.
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// createWorkflowRequest is the JSON body for the unified POST /api/workflows.
type createWorkflowRequest struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Definition  map[string]any `json:"definition"`
}

// handleCreateWorkflow creates a workflow from the unified REST endpoint.
// Accepts { name, kind, definition, description?, tags? } and stores
// without requiring full compilation (compile happens on run).
func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return
	}

	var req createWorkflowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	kind := loader.SchemaKind(req.Kind)
	if kind != loader.SchemaKindAgent && kind != loader.SchemaKindGraph {
		writeError(w, http.StatusBadRequest, "INVALID_KIND",
			fmt.Sprintf("kind must be %q or %q", loader.SchemaKindAgent, loader.SchemaKindGraph))
		return
	}

	defBytes, err := json.Marshal(req.Definition)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid definition: "+err.Error())
		return
	}

	now := time.Now()
	id := uuid.New().String()
	name := req.Name
	if name == "" {
		name = id
	}

	rec := WorkflowRecord{
		ID:          id,
		SchemaKind:  kind,
		Name:        name,
		Description: req.Description,
		Tags:        req.Tags,
		Source:      json.RawMessage(defBytes),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Attempt compilation but don't fail if the definition is empty/incomplete.
	switch kind {
	case loader.SchemaKindAgent:
		if gd, err := compileAgentWorkflowDefinition(defBytes, rec.ID, rec.Name); err == nil {
			rec.Compiled = gd
		}
	case loader.SchemaKindGraph:
		var gd graph.GraphDefinition
		if err := json.Unmarshal(defBytes, &gd); err == nil {
			rec.Compiled = &gd
		}
	}

	if err := s.store.Create(r.Context(), rec); err != nil {
		if errors.Is(err, ErrWorkflowExists) {
			writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("workflow %q already exists", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rec)
}

// handleCreateAgentWorkflow creates a workflow from an agent schema body.
func (s *Server) handleCreateAgentWorkflow(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return
	}

	wf, err := agent.LoadFromBytes(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	diags := agent.Validate(wf)
	if graph.HasErrors(diags) {
		details := diagMessages(diags)
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "agent workflow validation failed", details...)
		return
	}

	gd, err := agent.Compile(wf)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "COMPILE_ERROR", err.Error())
		return
	}

	gdDiags := gd.Validate()
	if graph.HasErrors(gdDiags) {
		details := diagMessages(gdDiags)
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "compiled graph validation failed", details...)
		return
	}

	now := time.Now()
	id := wf.ID
	if id == "" {
		id = uuid.New().String()
	}

	rec := WorkflowRecord{
		ID:         id,
		SchemaKind: loader.SchemaKindAgent,
		Name:       wf.Name,
		Source:     json.RawMessage(body),
		Compiled:   gd,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.store.Create(r.Context(), rec); err != nil {
		if errors.Is(err, ErrWorkflowExists) {
			writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("workflow %q already exists", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rec)
}

// handleCreateGraphWorkflow creates a workflow from a graph schema body.
func (s *Server) handleCreateGraphWorkflow(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return
	}

	var gd graph.GraphDefinition
	if err := json.Unmarshal(body, &gd); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	diags := gd.Validate()
	if graph.HasErrors(diags) {
		details := diagMessages(diags)
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "graph validation failed", details...)
		return
	}

	now := time.Now()
	id := gd.ID
	if id == "" {
		id = uuid.New().String()
	}

	rec := WorkflowRecord{
		ID:         id,
		SchemaKind: loader.SchemaKindGraph,
		Name:       id,
		Source:     json.RawMessage(body),
		Compiled:   &gd,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.store.Create(r.Context(), rec); err != nil {
		if errors.Is(err, ErrWorkflowExists) {
			writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("workflow %q already exists", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rec)
}

// updateWorkflowRequest is the JSON body for PUT /api/workflows/{id}.
type updateWorkflowRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Definition  map[string]any `json:"definition,omitempty"`
}

// handleUpdateWorkflow updates an existing workflow.
func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rec, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", id))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return
	}

	// Try the structured update format { name?, definition?, description?, tags? }.
	var req updateWorkflowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	if req.Name != nil {
		rec.Name = *req.Name
	}
	if req.Description != nil {
		rec.Description = *req.Description
	}
	if req.Tags != nil {
		rec.Tags = req.Tags
	}

	// If a definition was provided, update source and attempt re-compilation.
	if req.Definition != nil {
		defBytes, err := json.Marshal(req.Definition)
		if err != nil {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid definition: "+err.Error())
			return
		}
		rec.Source = json.RawMessage(defBytes)

		switch rec.SchemaKind {
		case loader.SchemaKindAgent:
			if gd, err := compileAgentWorkflowDefinition(defBytes, rec.ID, rec.Name); err == nil {
				rec.Compiled = gd
			}
		case loader.SchemaKindGraph:
			var gd graph.GraphDefinition
			if err := json.Unmarshal(defBytes, &gd); err == nil {
				rec.Compiled = &gd
			}
		}
	}

	rec.UpdatedAt = time.Now()
	if err := s.store.Update(r.Context(), rec); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

// handleDeleteWorkflow deletes a workflow by ID.
func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RunRequest is the JSON body for POST /api/workflows/{id}/run.
type RunRequest struct {
	// Legacy singular key; kept for backward compatibility.
	Input map[string]any `json:"input,omitempty"`
	// UI/API v1 key.
	Inputs  map[string]any `json:"inputs,omitempty"`
	Trace   *bool          `json:"trace,omitempty"`
	DryRun  bool           `json:"dry_run,omitempty"`
	Options RunReqOptions  `json:"options,omitempty"`
}

// RunReqOptions holds optional run configuration.
type RunReqOptions struct {
	Timeout string `json:"timeout,omitempty"`
	Stream  bool   `json:"stream,omitempty"`
}

// RunResponse is the JSON response for a completed run.
type RunResponse struct {
	// Legacy field name for workflow ID.
	ID string `json:"id,omitempty"`
	// Preferred field name for workflow ID.
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	Status     string `json:"status"`

	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	DurationMs  int64     `json:"duration_ms,omitempty"`

	Inputs map[string]any `json:"inputs,omitempty"`
	// Legacy field (full envelope) retained for compatibility.
	Output EnvelopeJSON `json:"output,omitempty"`
	// Preferred output shape for UI run detail/completion.
	Outputs map[string]any `json:"outputs,omitempty"`

	Error *runErrorResponse `json:"error,omitempty"`

	TokensIn  int `json:"tokens_in,omitempty"`
	TokensOut int `json:"tokens_out,omitempty"`
}

type runErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// handleRunWorkflow executes a workflow.
func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rec, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", id))
		return
	}
	if rec.Compiled == nil {
		writeError(w, http.StatusBadRequest, "NOT_COMPILED", "workflow has no compiled graph")
		return
	}

	// Parse request body (optional)
	var req RunRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
			return
		}
	}

	// Build timeout
	timeout := 5 * time.Minute
	if req.Options.Timeout != "" {
		d, err := time.ParseDuration(req.Options.Timeout)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_TIMEOUT", err.Error())
			return
		}
		timeout = d
	}

	// Hydrate graph
	providers := s.snapshotProviders()
	factory := hydrate.NewLiveNodeFactory(providers, s.clientFactory,
		hydrate.WithToolRegistry(core.NewToolRegistry()),
	)
	execGraph, err := hydrate.HydrateGraph(rec.Compiled, providers, factory)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "HYDRATE_ERROR", err.Error())
		return
	}

	inputs := req.Input
	if req.Inputs != nil {
		inputs = req.Inputs
	}
	// Build envelope
	env := EnvelopeFromJSON(inputs)

	if req.DryRun {
		if req.Options.Stream {
			writeError(w, http.StatusBadRequest, "INVALID_OPTIONS", "dry_run cannot be combined with streaming")
			return
		}
		s.handleRunDryRun(w, id, inputs)
		return
	}

	// Handle streaming vs non-streaming
	if req.Options.Stream {
		s.handleRunStreaming(w, r, id, execGraph, env, timeout)
		return
	}

	s.handleRunSync(w, r, id, execGraph, env, inputs, timeout)
}

// handleRunSync executes a workflow synchronously and returns the result.
func (s *Server) handleRunSync(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	execGraph *graph.BasicGraph,
	env *core.Envelope,
	inputs map[string]any,
	timeout time.Duration,
) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()

	if s.bus != nil {
		opts.EventBus = s.bus
	}

	// Attach store subscriber for event persistence
	if s.eventStore != nil && s.bus != nil {
		sub := bus.NewStoreSubscriber(s.eventStore, s.logger)
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, sub.Handle)
	}

	startedAt := time.Now()
	result, err := rt.Run(ctx, execGraph, env, opts)
	completedAt := time.Now()

	if err != nil {
		status := http.StatusInternalServerError
		code := "RUNTIME_ERROR"
		if ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
			code = "TIMEOUT"
		}
		runID := env.Trace.RunID
		if result != nil && strings.TrimSpace(result.Trace.RunID) != "" {
			runID = result.Trace.RunID
		}
		s.putRun(RunResponse{
			ID:          id,
			WorkflowID:  id,
			RunID:       runID,
			Status:      "failed",
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
			Inputs:      cloneAnyMap(inputs),
			Output:      EnvelopeToJSON(result),
			Outputs: func() map[string]any {
				if result == nil {
					return nil
				}
				return cloneAnyMap(result.Vars)
			}(),
			Error: &runErrorResponse{
				Code:    code,
				Message: err.Error(),
			},
		})
		writeError(w, status, code, err.Error())
		return
	}

	runID := ""
	if result != nil {
		runID = result.Trace.RunID
	}
	outputs := map[string]any(nil)
	if result != nil {
		outputs = cloneAnyMap(result.Vars)
	}

	resp := RunResponse{
		ID:          id,
		WorkflowID:  id,
		RunID:       runID,
		Status:      "completed",
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
		Inputs:      cloneAnyMap(inputs),
		Output:      EnvelopeToJSON(result),
		Outputs:     outputs,
	}
	s.putRun(resp)

	writeJSON(w, http.StatusOK, resp)
}

// handleRunDryRun validates hydration and persists a successful no-op run.
func (s *Server) handleRunDryRun(w http.ResponseWriter, workflowID string, inputs map[string]any) {
	startedAt := time.Now()
	completedAt := startedAt

	resp := RunResponse{
		ID:          workflowID,
		WorkflowID:  workflowID,
		RunID:       uuid.New().String(),
		Status:      "completed",
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMs:  0,
		Inputs:      cloneAnyMap(inputs),
		Output:      EnvelopeToJSON(EnvelopeFromJSON(inputs)),
		Outputs:     cloneAnyMap(inputs),
	}
	s.putRun(resp)

	writeJSON(w, http.StatusOK, resp)
}

// handleRunStreaming executes a workflow and streams events via SSE.
func (s *Server) handleRunStreaming(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	execGraph *graph.BasicGraph,
	env *core.Envelope,
	timeout time.Duration,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "STREAMING_ERROR", "streaming not supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	runID := uuid.New().String()

	// Subscribe to bus for this run
	var sub bus.Subscription
	if s.bus != nil {
		sub = s.bus.Subscribe(runID)
		defer sub.Close()
	}

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	if s.bus != nil {
		opts.EventBus = s.bus
	}

	// Attach store subscriber
	if s.eventStore != nil {
		storeSub := bus.NewStoreSubscriber(s.eventStore, s.logger)
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, storeSub.Handle)
	}

	// Set run ID on envelope
	env.Trace.RunID = runID

	// Run in goroutine
	doneCh := make(chan error, 1)
	go func() {
		_, err := rt.Run(ctx, execGraph, env, opts)
		doneCh <- err
	}()

	// Stream events
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	writeSSE := func(event string, data any) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	// Send initial event
	writeSSE("run.started", map[string]string{"run_id": runID, "workflow_id": id})

	if sub != nil {
		for {
			select {
			case evt, ok := <-sub.Events():
				if !ok {
					return
				}
				writeSSE(string(evt.Kind), evt)
				if evt.Kind == runtime.EventRunFinished {
					return
				}
			case err := <-doneCh:
				if err != nil {
					writeSSE("run.error", map[string]string{"error": err.Error()})
				}
				// Drain remaining events briefly
				drainTimer := time.NewTimer(100 * time.Millisecond)
				for {
					select {
					case evt, ok := <-sub.Events():
						if !ok {
							drainTimer.Stop()
							return
						}
						writeSSE(string(evt.Kind), evt)
						if evt.Kind == runtime.EventRunFinished {
							drainTimer.Stop()
							return
						}
					case <-drainTimer.C:
						return
					}
				}
			case <-heartbeat.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				flusher.Flush()
			case <-ctx.Done():
				writeSSE("run.error", map[string]string{"error": "timeout"})
				return
			}
		}
	} else {
		// No bus — just wait for completion
		err := <-doneCh
		if err != nil {
			writeSSE("run.error", map[string]string{"error": err.Error()})
		} else {
			writeSSE("run.finished", map[string]string{"run_id": runID, "status": "completed"})
		}
	}
}

// handleRunEvents serves SSE events for a run from the event store.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")

	if s.eventStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event store not configured")
		return
	}

	events, err := s.eventStore.List(r.Context(), runID, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusOK, events)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for _, evt := range events {
		jsonData, _ := json.Marshal(evt)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Kind, jsonData)
	}
	flusher.Flush()
}

type runTraceResponse struct {
	RunID       string `json:"run_id"`
	WorkflowID  string `json:"workflow_id"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DurationMs  int64  `json:"duration_ms"`
	Status      string `json:"status"`
	Spans       []any  `json:"spans"`
}

// handleRunTrace returns a minimal run trace envelope for the trace viewer.
func (s *Server) handleRunTrace(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "run_id is required")
		return
	}
	run, ok := s.getRun(runID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("run %q not found", runID))
		return
	}

	startedAt := run.StartedAt.UTC().Format(time.RFC3339)
	completedAt := run.CompletedAt.UTC().Format(time.RFC3339)
	if run.CompletedAt.IsZero() {
		completedAt = startedAt
	}

	status := run.Status
	switch status {
	case "completed", "failed", "cancelled":
	default:
		status = "completed"
	}

	writeJSON(w, http.StatusOK, runTraceResponse{
		RunID:       run.RunID,
		WorkflowID:  run.WorkflowID,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMs:  run.DurationMs,
		Status:      status,
		Spans:       []any{},
	})
}

// --- helpers ---

type runSummaryResponse struct {
	RunID       string    `json:"run_id"`
	WorkflowID  string    `json:"workflow_id"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	DurationMs  int64     `json:"duration_ms,omitempty"`
}

func (s *Server) putRun(run RunResponse) {
	if strings.TrimSpace(run.RunID) == "" {
		return
	}
	s.runsMu.Lock()
	defer s.runsMu.Unlock()

	if _, exists := s.runs[run.RunID]; !exists {
		s.runOrder = append(s.runOrder, run.RunID)
	}
	s.runs[run.RunID] = cloneRunResponse(run)
}

func (s *Server) getRun(runID string) (RunResponse, bool) {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()

	run, ok := s.runs[runID]
	if !ok {
		return RunResponse{}, false
	}
	return cloneRunResponse(run), true
}

func (s *Server) listRuns(workflowID string) []runSummaryResponse {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()

	runs := make([]runSummaryResponse, 0, len(s.runOrder))
	for i := len(s.runOrder) - 1; i >= 0; i-- {
		runID := s.runOrder[i]
		run, ok := s.runs[runID]
		if !ok {
			continue
		}
		if workflowID != "" && run.WorkflowID != workflowID {
			continue
		}
		runs = append(runs, runSummaryResponse{
			RunID:       run.RunID,
			WorkflowID:  run.WorkflowID,
			Status:      run.Status,
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
			DurationMs:  run.DurationMs,
		})
	}
	return runs
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRunResponse(in RunResponse) RunResponse {
	out := in
	var traceCopy *TraceJSON
	if in.Output.Trace != nil {
		t := *in.Output.Trace
		traceCopy = &t
	}
	out.Inputs = cloneAnyMap(in.Inputs)
	out.Output = EnvelopeJSON{
		Vars:      cloneAnyMap(in.Output.Vars),
		Messages:  append([]MessageJSON(nil), in.Output.Messages...),
		Artifacts: append([]ArtifactJSON(nil), in.Output.Artifacts...),
		Trace:     traceCopy,
	}
	out.Outputs = cloneAnyMap(in.Outputs)
	if in.Error != nil {
		errCopy := *in.Error
		out.Error = &errCopy
	}
	return out
}

// diagMessages extracts error messages from diagnostics.
func diagMessages(diags []graph.Diagnostic) []string {
	errs := graph.Errors(diags)
	msgs := make([]string, 0, len(errs))
	for _, d := range errs {
		msgs = append(msgs, d.Message)
	}
	return msgs
}

// isMaxBytesError checks if the error is from http.MaxBytesReader.
func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

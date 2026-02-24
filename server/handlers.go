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
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/nodes"
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

	gdDiags := gd.ValidateWithRegistry(registry.Global())
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

	diags := gd.ValidateWithRegistry(registry.Global())
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

	// Re-compile based on schema kind
	switch rec.SchemaKind {
	case loader.SchemaKindAgent:
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
		rec.Source = json.RawMessage(body)
		rec.Compiled = gd
		rec.Name = wf.Name

	case loader.SchemaKindGraph:
		var gd graph.GraphDefinition
		if err := json.Unmarshal(body, &gd); err != nil {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
			return
		}
		diags := gd.ValidateWithRegistry(registry.Global())
		if graph.HasErrors(diags) {
			details := diagMessages(diags)
			writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "graph validation failed", details...)
			return
		}
		rec.Source = json.RawMessage(body)
		rec.Compiled = &gd

	default:
		writeError(w, http.StatusBadRequest, "UNKNOWN_KIND", fmt.Sprintf("unknown schema kind %q", rec.SchemaKind))
		return
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
	Input   map[string]any `json:"input,omitempty"`
	Options RunReqOptions  `json:"options,omitempty"`
}

// RunReqOptions holds optional run configuration.
type RunReqOptions struct {
	Timeout string              `json:"timeout,omitempty"`
	Stream  bool                `json:"stream,omitempty"`
	Human   *RunReqHumanOptions `json:"human,omitempty"`
}

// RunReqHumanOptions controls how daemon run requests handle human node prompts.
type RunReqHumanOptions struct {
	// Mode controls handling strategy:
	// - "strict": fail at runtime when a human node requests input
	// - "auto_approve": auto-approve requests
	// - "auto_reject": auto-reject requests
	Mode string `json:"mode,omitempty"`

	// Optional response overrides for auto modes.
	Choice      string `json:"choice,omitempty"`
	Notes       string `json:"notes,omitempty"`
	RespondedBy string `json:"responded_by,omitempty"`
	Delay       string `json:"delay,omitempty"`
}

// RunResponse is the JSON response for a completed run.
type RunResponse struct {
	ID          string       `json:"id"`
	RunID       string       `json:"run_id"`
	Status      string       `json:"status"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	DurationMs  int64        `json:"duration_ms"`
	Output      EnvelopeJSON `json:"output"`
}

type RunHistoryResponse struct {
	ID          string     `json:"id,omitempty"`
	RunID       string     `json:"run_id"`
	WorkflowID  string     `json:"workflow_id,omitempty"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationMs  int64      `json:"duration_ms,omitempty"`
}

type RunExportResponse struct {
	Run    RunHistoryResponse `json:"run"`
	Events []runtime.Event    `json:"events"`
}

type runIDLister interface {
	RunIDs(ctx context.Context) ([]string, error)
}

// handleRunWorkflow executes a workflow.
func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Parse request body (optional)
	var req RunRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
			return
		}
	}

	plan, err := s.planWorkflowRun(r.Context(), id, req)
	if err != nil {
		writeRunAPIError(w, err)
		return
	}

	// Handle streaming vs non-streaming
	if req.Options.Stream {
		s.handleRunStreaming(w, r, id, plan.execGraph, plan.env, plan.timeout)
		return
	}

	s.handleRunSync(w, r, id, plan.execGraph, plan.env, plan.timeout)
}

type strictRunHumanHandler struct{}

func (strictRunHumanHandler) Request(_ context.Context, req *nodes.HumanRequest) (*nodes.HumanResponse, error) {
	return nil, fmt.Errorf(
		"human request %q (%s) requires external handling; set options.human.mode to auto_approve or auto_reject",
		req.ID,
		req.Type,
	)
}

func buildRunHumanHandler(cfg *RunReqHumanOptions) (nodes.HumanHandler, error) {
	mode := "strict"
	if cfg != nil && strings.TrimSpace(cfg.Mode) != "" {
		mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	}

	switch mode {
	case "strict":
		return strictRunHumanHandler{}, nil
	case "auto_approve", "auto_reject":
		var handler *nodes.AutoApproveHandler
		if mode == "auto_approve" {
			handler = nodes.NewAutoApproveHandler()
		} else {
			handler = nodes.NewAutoRejectHandler()
		}
		if cfg != nil {
			if strings.TrimSpace(cfg.Choice) != "" {
				handler.Choice = cfg.Choice
			}
			if strings.TrimSpace(cfg.Notes) != "" {
				handler.Notes = cfg.Notes
			}
			if strings.TrimSpace(cfg.RespondedBy) != "" {
				handler.RespondedBy = cfg.RespondedBy
			}
			if strings.TrimSpace(cfg.Delay) != "" {
				d, err := time.ParseDuration(cfg.Delay)
				if err != nil {
					return nil, fmt.Errorf("options.human.delay: %w", err)
				}
				handler.Delay = d
			}
		}
		return handler, nil
	default:
		return nil, fmt.Errorf("options.human.mode must be one of: strict, auto_approve, auto_reject")
	}
}

// handleRunSync executes a workflow synchronously and returns the result.
func (s *Server) handleRunSync(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	execGraph *graph.BasicGraph,
	env *core.Envelope,
	timeout time.Duration,
) {
	resp, err := s.executeWorkflowRunSync(r.Context(), id, &workflowRunPlan{
		execGraph: execGraph,
		env:       env,
		timeout:   timeout,
	}, workflowRunMetadataDecorator(id))
	if err != nil {
		writeRunAPIError(w, err)
		return
	}
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
	writer, ok := newSSEWriter(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "STREAMING_ERROR", "streaming not supported")
		return
	}

	writer.startResponse()

	runID := uuid.New().String()
	sub := s.subscribeRun(runID)
	if sub != nil {
		defer sub.Close()
	}

	doneCh := s.startStreamingRuntime(execGraph, env, runID, timeout, workflowRunMetadataDecorator(id))
	writer.writeEvent("run.started", map[string]string{"run_id": runID, "workflow_id": id})

	if sub == nil {
		s.streamWithoutSubscription(writer, doneCh, runID)
		return
	}
	s.streamWithSubscription(r.Context(), writer, sub, doneCh, runID)
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	return &sseWriter{
		w:       w,
		flusher: flusher,
	}, true
}

func (s *sseWriter) startResponse() {
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.WriteHeader(http.StatusOK)
	s.flusher.Flush()
}

func (s *sseWriter) writeEvent(event string, data any) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, jsonData)
	s.flusher.Flush()
}

func (s *sseWriter) writeHeartbeat() {
	fmt.Fprintf(s.w, ": heartbeat\n\n")
	s.flusher.Flush()
}

func (s *Server) subscribeRun(runID string) bus.Subscription {
	if s.bus == nil {
		return nil
	}
	return s.bus.Subscribe(runID)
}

func (s *Server) startStreamingRuntime(
	execGraph *graph.BasicGraph,
	env *core.Envelope,
	runID string,
	timeout time.Duration,
	extraDecorator runtime.EventEmitterDecorator,
) <-chan error {
	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.EventEmitterDecorator = combineEmitDecorators(s.emitDecorator, extraDecorator)
	if s.bus != nil {
		opts.EventBus = s.bus
	}
	if s.runtimeEvents != nil {
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, s.runtimeEvents)
	}

	// Attach store subscriber.
	if s.eventStore != nil {
		storeSub := bus.NewStoreSubscriber(s.eventStore, s.logger)
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, storeSub.Handle)
	}

	// Set run ID on envelope before runtime execution.
	env.Trace.RunID = runID

	doneCh := make(chan error, 1)
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		_, err := rt.Run(runCtx, execGraph, env, opts)
		doneCh <- err
	}()
	return doneCh
}

// handleListRuns returns persisted run summaries from the event store.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event store not configured")
		return
	}

	runIDStore, ok := s.eventStore.(runIDLister)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event store does not support run listing")
		return
	}

	runIDs, err := runIDStore.RunIDs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	workflowFilter := strings.TrimSpace(r.URL.Query().Get("workflow_id"))

	runs := make([]RunHistoryResponse, 0, len(runIDs))
	for _, runID := range runIDs {
		events, err := s.eventStore.List(r.Context(), runID, 0, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
			return
		}
		summary, ok := summarizeRunEvents(runID, events)
		if !ok {
			continue
		}

		if statusFilter != "" && strings.ToLower(summary.Status) != statusFilter {
			continue
		}
		if workflowFilter != "" && summary.WorkflowID != workflowFilter {
			continue
		}

		runs = append(runs, summary)
	}

	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})

	writeJSON(w, http.StatusOK, runs)
}

// handleGetRun returns a run summary by run ID.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event store not configured")
		return
	}

	runID := strings.TrimSpace(r.PathValue("run_id"))
	events, err := s.eventStore.List(r.Context(), runID, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	summary, ok := summarizeRunEvents(runID, events)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("run %q not found", runID))
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// handleExportRun exports run summary and all persisted events.
func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "event store not configured")
		return
	}

	runID := strings.TrimSpace(r.PathValue("run_id"))
	events, err := s.eventStore.List(r.Context(), runID, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	summary, ok := summarizeRunEvents(runID, events)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("run %q not found", runID))
		return
	}

	writeJSON(w, http.StatusOK, RunExportResponse{
		Run:    summary,
		Events: events,
	})
}

func (s *Server) streamWithoutSubscription(writer *sseWriter, doneCh <-chan error, runID string) {
	err := <-doneCh
	if err != nil {
		writer.writeEvent("run.error", map[string]string{"error": err.Error()})
		return
	}
	writer.writeEvent("run.finished", map[string]string{"run_id": runID, "status": "completed"})
}

func (s *Server) streamWithSubscription(
	requestCtx context.Context,
	writer *sseWriter,
	sub bus.Subscription,
	doneCh <-chan error,
	runID string,
) {
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case evt, ok := <-sub.Events():
			if !ok {
				return
			}
			writer.writeEvent(string(evt.Kind), evt)
			if evt.Kind == runtime.EventRunFinished {
				return
			}
		case err := <-doneCh:
			s.handleStreamingCompletionWithDrain(writer, sub, err, runID)
			return
		case <-heartbeat.C:
			writer.writeHeartbeat()
		case <-requestCtx.Done():
			return
		}
	}
}

func (s *Server) handleStreamingCompletionWithDrain(
	writer *sseWriter,
	sub bus.Subscription,
	runErr error,
	runID string,
) {
	if runErr != nil {
		writer.writeEvent("run.error", map[string]string{"error": runErr.Error()})
	}

	sawRunFinished := s.drainSubscriptionEvents(writer, sub)
	// In fallback cases where no bus run events were captured, still close
	// the stream with an explicit completion event.
	if runErr == nil && !sawRunFinished {
		writer.writeEvent("run.finished", map[string]string{"run_id": runID, "status": "completed"})
	}
}

func (s *Server) drainSubscriptionEvents(writer *sseWriter, sub bus.Subscription) bool {
	drainTimer := time.NewTimer(100 * time.Millisecond)
	defer drainTimer.Stop()

	for {
		select {
		case evt, ok := <-sub.Events():
			if !ok {
				return false
			}
			writer.writeEvent(string(evt.Kind), evt)
			if evt.Kind == runtime.EventRunFinished {
				return true
			}
		case <-drainTimer.C:
			return false
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

func summarizeRunEvents(runID string, events []runtime.Event) (RunHistoryResponse, bool) {
	if len(events) == 0 {
		return RunHistoryResponse{}, false
	}

	summary := RunHistoryResponse{
		RunID:     runID,
		Status:    "running",
		StartedAt: events[0].Time.UTC(),
	}

	for _, event := range events {
		if event.Kind == runtime.EventRunStarted {
			if summary.StartedAt.IsZero() || event.Time.Before(summary.StartedAt) {
				summary.StartedAt = event.Time.UTC()
			}
			if summary.WorkflowID == "" {
				summary.WorkflowID = workflowIDFromPayload(event.Payload)
			}
		}

		if event.Kind == runtime.EventRunFinished {
			completedAt := event.Time.UTC()
			summary.CompletedAt = &completedAt
			summary.Status = "completed"

			if rawStatus, ok := event.Payload["status"].(string); ok && strings.TrimSpace(rawStatus) != "" {
				summary.Status = strings.TrimSpace(rawStatus)
			}

			if summary.WorkflowID == "" {
				summary.WorkflowID = workflowIDFromPayload(event.Payload)
			}

			if event.Elapsed > 0 {
				summary.DurationMs = event.Elapsed.Milliseconds()
			}
		}
	}

	if summary.CompletedAt != nil && summary.DurationMs == 0 {
		if delta := summary.CompletedAt.Sub(summary.StartedAt); delta > 0 {
			summary.DurationMs = delta.Milliseconds()
		}
	}

	summary.ID = summary.WorkflowID
	return summary, true
}

func workflowIDFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}

	workflowID, ok := payload["workflow_id"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(workflowID)
}

// --- helpers ---

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

func writeRunAPIError(w http.ResponseWriter, err error) {
	var runErr *runAPIError
	if errors.As(err, &runErr) {
		writeError(w, runErr.Status, runErr.Code, runErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "RUNTIME_ERROR", err.Error())
}

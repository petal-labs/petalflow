package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

type runAPIError struct {
	Status  int
	Code    string
	Message string
}

func (e *runAPIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type workflowRunPlan struct {
	execGraph       *graph.BasicGraph
	env             *core.Envelope
	timeout         time.Duration
	workflowVersion string
	resume          bool
	resumeToken     string
	idempotencyKey  string
	tenantID        string
	// workflowPolicy is the workflow-scoped tool policy override;
	// nil means the tenant default policy applies.
	workflowPolicy *tool.ToolPolicy
}

// governedPolicyEvaluator resolves the effective tool policy for runs
// and resolves a tool's safety classification from the tool store when a
// policy uses classification-based approval gates.
type governedPolicyEvaluator struct {
	effective tool.EffectiveToolPolicy
	store     tool.Store
	cache     map[string]tool.SafetyClassification
}

// classification resolves and caches the classification for a base tool
// name. Builtins resolve from the static registry; unresolved names
// classify as dangerous so a broken store or unregistered reference
// cannot widen access through the gate.
func (e *governedPolicyEvaluator) classification(base string) tool.SafetyClassification {
	if class, cached := e.cache[base]; cached {
		return class
	}
	if builtin, ok := tool.BuiltinRegistration(base); ok {
		class := builtin.EffectiveRegistrationSafety()
		e.storeClassification(base, class)
		return class
	}
	class := tool.SafetyClassification{
		Level: tool.SideEffectSideEffect,
		Notes: "unresolved classification",
	}
	if e.store != nil {
		if reg, found, err := e.store.Get(context.Background(), base); err == nil && found {
			class = reg.EffectiveRegistrationSafety()
		}
	}
	e.storeClassification(base, class)
	return class
}

func (e *governedPolicyEvaluator) storeClassification(base string, class tool.SafetyClassification) {
	if e.cache == nil {
		e.cache = make(map[string]tool.SafetyClassification)
	}
	e.cache[base] = class
}

// EvaluatePolicy evaluates name rules; classification-based approval
// gates resolve the tool's classification from the tool store.
func (e *governedPolicyEvaluator) EvaluatePolicy(toolName string) tool.PolicyDecision {
	if len(e.effective.RequireApprovalClasses) == 0 {
		return tool.FromEffectivePolicy(e.effective).EvaluatePolicy(toolName)
	}
	base, ref := tool.ReferenceNames(toolName)
	return tool.EvaluateClassifiedReferences(e.effective, e.classification(base), ref, base)
}

// EvaluatePolicyClassified evaluates with a caller-provided classification
// for the referenced tool when the caller knows it (e.g. the daemon
// service carries its registration); the base-name classification still
// resolves from the store.
func (e *governedPolicyEvaluator) EvaluatePolicyClassified(toolName string, class tool.SafetyClassification) tool.PolicyDecision {
	if len(e.effective.RequireApprovalClasses) == 0 {
		return tool.FromEffectivePolicy(e.effective).EvaluatePolicy(toolName)
	}
	base, ref := tool.ReferenceNames(toolName)
	refClass := class
	if base != ref {
		refClass = e.classification(base)
	}
	return tool.EvaluateClassifiedReferences(e.effective, refClass, ref, base)
}

// runPolicyEvaluator resolves the effective tool policy for a run and
// returns the evaluator installed into the run context. The workflow
// record (via the plan) already carries the workflow-level override; the
// tenant policy comes from the store override or the configured default.
//
// Governance is opt-in per policy source: when nothing is configured —
// no stored tenant override, no configured default, no workflow policy —
// the evaluator is nil and the run context stays ungoverned (parallel to
// how server authentication is opt-in for embedded library use). As soon
// as any policy source exists, the effective policy applies including
// its secure-by-default rules.
func (s *Server) runPolicyEvaluator(ctx context.Context, plan *workflowRunPlan) (tool.PolicyEvaluator, error) {
	tenantPolicy, _, err := s.effectiveTenantPolicy(ctx, plan.tenantID)
	if err != nil {
		return nil, err
	}
	if tenantPolicy == nil && plan.workflowPolicy == nil && s.defaultTenantPolicy == nil {
		return nil, nil
	}
	effective := tool.ResolveEffectivePolicy(tenantPolicy, plan.workflowPolicy)
	return &governedPolicyEvaluator{
		effective: effective,
		store:     s.toolStore,
	}, nil
}

type scheduledRunMetadata struct {
	ScheduleID  string
	WorkflowID  string
	ScheduledAt time.Time
}

type webhookRunMetadata struct {
	WorkflowID string
	TriggerID  string
	Method     string
}

func (s *Server) planWorkflowRun(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
	if s.planHook != nil {
		return s.planHook(ctx, workflowID, req)
	}
	return s.planWorkflowRunDefault(ctx, workflowID, req)
}

// planWorkflowRunDefault is the default planning path, split out so
// tests can wrap it via planHook without infinite recursion.
func (s *Server) planWorkflowRunDefault(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
	rec, ok, err := s.store.Get(ctx, workflowID)
	if err != nil {
		return nil, &runAPIError{Status: http.StatusInternalServerError, Code: "STORE_ERROR", Message: err.Error()}
	}
	if !ok {
		return nil, &runAPIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: fmt.Sprintf("workflow %q not found", workflowID)}
	}
	if rec.Compiled == nil {
		return nil, &runAPIError{Status: http.StatusBadRequest, Code: "NOT_COMPILED", Message: "workflow has no compiled graph"}
	}

	plan, err := s.planWorkflowRunWithDefinition(ctx, workflowID, rec.Compiled, req)
	if err == nil {
		plan.tenantID = rec.TenantID
		plan.workflowPolicy = rec.Policy
	}
	return plan, err
}

func (s *Server) planWorkflowRunWithDefinition(
	ctx context.Context,
	workflowID string,
	compiled *graph.GraphDefinition,
	req RunRequest,
) (*workflowRunPlan, error) {
	if compiled == nil {
		return nil, &runAPIError{Status: http.StatusBadRequest, Code: "NOT_COMPILED", Message: "workflow has no compiled graph"}
	}

	timeout := 5 * time.Minute
	if req.Options.Timeout != "" {
		d, err := time.ParseDuration(req.Options.Timeout)
		if err != nil {
			return nil, &runAPIError{Status: http.StatusBadRequest, Code: "INVALID_TIMEOUT", Message: err.Error()}
		}
		timeout = d
	}

	humanHandler, err := buildRunHumanHandler(req.Options.Human)
	if err != nil {
		return nil, &runAPIError{Status: http.StatusBadRequest, Code: "INVALID_HUMAN_OPTIONS", Message: err.Error()}
	}

	toolRegistry, err := hydrate.BuildActionToolRegistry(ctx, s.toolStore)
	if err != nil {
		return nil, &runAPIError{Status: http.StatusInternalServerError, Code: "TOOL_REGISTRY_ERROR", Message: err.Error()}
	}

	factory := hydrate.NewLiveNodeFactory(s.providers, s.clientFactory,
		hydrate.WithToolRegistry(toolRegistry),
		hydrate.WithHumanHandler(humanHandler),
	)
	execGraph, err := hydrate.HydrateGraph(compiled, s.providers, factory)
	if err != nil {
		return nil, &runAPIError{Status: http.StatusUnprocessableEntity, Code: "HYDRATE_ERROR", Message: err.Error()}
	}

	return &workflowRunPlan{
		execGraph:       execGraph,
		env:             EnvelopeFromJSON(req.Input),
		timeout:         timeout,
		workflowVersion: compiled.Version,
		idempotencyKey:  req.Options.IdempotencyKey,
	}, nil
}

func (s *Server) executeWorkflowRunSync(
	ctx context.Context,
	workflowID string,
	plan *workflowRunPlan,
	extraDecorator runtime.EventEmitterDecorator,
) (RunResponse, error) {
	select {
	case s.runSlots <- struct{}{}:
		defer func() { <-s.runSlots }()
	default:
		return RunResponse{}, &runAPIError{Status: http.StatusTooManyRequests, Code: "RUN_LIMIT", Message: "too many concurrent runs"}
	}
	runCtx, cancel := context.WithTimeout(ctx, plan.timeout)
	defer cancel()

	evaluator, polErr := s.runPolicyEvaluator(ctx, plan)
	if polErr != nil {
		return RunResponse{}, &runAPIError{Status: http.StatusInternalServerError, Code: "POLICY_ERROR", Message: polErr.Error()}
	}
	if evaluator != nil {
		runCtx = tool.ContextWithPolicyEvaluator(runCtx, evaluator)
	}

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.RunStore = s.runStore
	opts.WorkflowID = workflowID
	opts.TenantID = plan.tenantID
	opts.WorkflowVersion = plan.workflowVersion
	if plan.env.Trace.RunID == "" && s.runStore != nil {
		plan.env.Trace.RunID = uuid.New().String()
	}
	opts.RunID = plan.env.Trace.RunID
	opts.Resume = plan.resume
	opts.ResumeToken = plan.resumeToken
	opts.IdempotencyKey = plan.idempotencyKey
	if s.runStore != nil {
		opts.HumanRequestHandler = s.durableHumanRequestHandler
	}
	opts.EventEmitterDecorator = combineEmitDecorators(s.emitDecorator, extraDecorator)

	if s.bus != nil {
		opts.EventBus = s.bus
	}
	if s.runtimeEvents != nil {
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, s.runtimeEvents)
	}

	if s.eventStore != nil {
		sub := bus.NewStoreSubscriber(s.eventStore, s.logger)
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, sub.Handle)
	}

	startedAt := time.Now().UTC()
	if opts.RunID != "" {
		if !s.reserveActiveRun(opts.RunID, cancel) {
			return RunResponse{}, &runAPIError{
				Status: http.StatusConflict, Code: "RUN_IN_PROGRESS",
				Message: "run is already being executed",
			}
		}
		defer func() {
			s.releaseActiveRun(opts.RunID, cancel)
		}()
	}
	result, err := rt.Run(runCtx, plan.execGraph, plan.env, opts)
	completedAt := time.Now().UTC()

	if err != nil {
		if errors.Is(err, runtime.ErrHumanPending) {
			response := RunResponse{ID: workflowID, RunID: plan.env.Trace.RunID, Status: string(runtime.RunStatusPaused)}
			if s.runStore != nil {
				if record, getErr := s.runStore.Get(context.Background(), plan.env.Trace.RunID); getErr == nil {
					response.StartedAt = record.StartedAt
					response.ResumeToken = record.ResumeToken
					response.Pending = record.PendingAction
				}
			}
			return response, &runAPIError{Status: http.StatusAccepted, Code: "RUN_PAUSED", Message: "run is waiting for human input"}
		}
		if errors.Is(err, runtime.ErrRunAlreadySettled) {
			return RunResponse{}, &runAPIError{Status: http.StatusConflict, Code: "RUN_SETTLED", Message: "run is already settled"}
		}
		if errors.Is(err, runtime.ErrWorkflowVersion) {
			return RunResponse{}, &runAPIError{Status: http.StatusConflict, Code: "WORKFLOW_VERSION_MISMATCH", Message: "run belongs to a different workflow version"}
		}
		if errors.Is(err, runtime.ErrInvalidResumeToken) {
			return RunResponse{}, &runAPIError{Status: http.StatusForbidden, Code: "INVALID_RESUME_TOKEN", Message: "resume token is invalid"}
		}
		if runCtx.Err() == context.DeadlineExceeded {
			return RunResponse{}, &runAPIError{Status: http.StatusGatewayTimeout, Code: "TIMEOUT", Message: err.Error()}
		}
		return RunResponse{}, &runAPIError{Status: http.StatusInternalServerError, Code: "RUNTIME_ERROR", Message: err.Error()}
	}

	runID := ""
	if result != nil {
		runID = result.Trace.RunID
	}

	response := RunResponse{
		ID:          workflowID,
		RunID:       runID,
		Status:      "completed",
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
		Output:      EnvelopeToJSON(result),
	}
	if s.runStore != nil && runID != "" {
		if record, getErr := s.runStore.Get(context.Background(), runID); getErr == nil {
			response.ResumeToken = record.ResumeToken
		}
	}
	return response, nil
}

func (s *Server) reserveActiveRun(runID string, cancel context.CancelFunc) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if _, exists := s.activeRuns[runID]; exists {
		return false
	}
	s.activeRuns[runID] = cancel
	return true
}

func (s *Server) releaseActiveRun(runID string, _ context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	delete(s.activeRuns, runID)
}

func (s *Server) durableHumanRequestHandler(ctx context.Context, value any) (any, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	req, ok := value.(*nodes.HumanRequest)
	if !ok || req == nil {
		return nil, fmt.Errorf("human bridge received %T, want *nodes.HumanRequest", value)
	}
	record, err := s.runStore.Get(ctx, req.EnvelopeRef)
	if err != nil {
		return nil, err
	}
	if record.PendingAction != nil {
		pending := record.PendingAction
		if pending.NodeID != req.NodeID {
			return nil, fmt.Errorf("run already has a pending action for node %q", pending.NodeID)
		}
		if !pending.Completed && pending.Response == nil {
			return nil, &nodes.HumanPendingError{Request: req}
		}
		var response nodes.HumanResponse
		encoded, err := json.Marshal(pending.Response)
		if err != nil {
			return nil, fmt.Errorf("decode pending response: %w", err)
		}
		if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, fmt.Errorf("decode pending response: %w", err)
		}
		response.RequestID = req.ID
		record.PendingAction = nil
		record.Status = runtime.RunStatusRunning
		record.UpdatedAt = time.Now().UTC()
		if err := s.runStore.Update(ctx, record); err != nil {
			return nil, err
		}
		return &response, nil
	}

	record.PendingAction = &runtime.PendingAction{
		ID: req.ID, RunID: req.EnvelopeRef, NodeID: req.NodeID,
		Type: string(req.Type), Prompt: req.Prompt, Data: req.Data,
		Options: req.Options, Schema: req.Schema, CreatedAt: req.CreatedAt,
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.runStore.Update(ctx, record); err != nil {
		return nil, err
	}
	return nil, &nodes.HumanPendingError{Request: req}
}

func (s *Server) runScheduledWorkflow(
	ctx context.Context,
	workflowID string,
	req RunRequest,
	meta scheduledRunMetadata,
) (RunResponse, error) {
	plan, err := s.planWorkflowRun(ctx, workflowID, req)
	if err != nil {
		return RunResponse{}, err
	}

	decorator := scheduleRunMetadataDecorator(meta)
	return s.executeWorkflowRunSync(ctx, workflowID, plan, decorator)
}

func combineEmitDecorators(
	first runtime.EventEmitterDecorator,
	second runtime.EventEmitterDecorator,
) runtime.EventEmitterDecorator {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	default:
		return func(emit runtime.EventEmitter) runtime.EventEmitter {
			return second(first(emit))
		}
	}
}

func scheduleRunMetadataDecorator(meta scheduledRunMetadata) runtime.EventEmitterDecorator {
	return func(next runtime.EventEmitter) runtime.EventEmitter {
		return func(e runtime.Event) {
			if e.Kind == runtime.EventRunStarted || e.Kind == runtime.EventRunFinished {
				if e.Payload == nil {
					e.Payload = map[string]any{}
				}
				e.Payload["trigger"] = "schedule"
				e.Payload["schedule_id"] = meta.ScheduleID
				e.Payload["workflow_id"] = meta.WorkflowID
				e.Payload["scheduled_at"] = meta.ScheduledAt.UTC().Format(time.RFC3339Nano)
			}
			next(e)
		}
	}
}

func webhookRunMetadataDecorator(meta webhookRunMetadata) runtime.EventEmitterDecorator {
	return func(next runtime.EventEmitter) runtime.EventEmitter {
		return func(e runtime.Event) {
			if e.Kind == runtime.EventRunStarted || e.Kind == runtime.EventRunFinished {
				if e.Payload == nil {
					e.Payload = map[string]any{}
				}
				e.Payload["trigger"] = "webhook"
				e.Payload["workflow_id"] = meta.WorkflowID
				e.Payload["webhook_trigger_id"] = meta.TriggerID
				e.Payload["webhook_method"] = meta.Method
			}
			next(e)
		}
	}
}

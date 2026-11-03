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

	toolRegistry, err := hydrate.BuildActionToolRegistry(ctx, s.toolStore, hydrate.WithAuditStore(s.auditStore()))
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
	// Workflow-run audit identity: tool lifecycle events from this run
	// scope to the tenant + workflow with the run actor attributed.
	runCtx = tool.ContextWithAuditContext(runCtx, tool.ToolAuditContext{
		Actor:      "workflow:" + workflowID,
		TenantID:   plan.tenantID,
		WorkflowID: workflowID,
		RunID:      plan.env.Trace.RunID,
	})

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
		// The ledger wrapper keeps durable tool-approval records intact
		// while runtime workers write with progressively older copies.
		governedStore := s.runStore
		if wrapped, ok := newApprovalLedgerStore(s.runStore); ok {
			governedStore = wrapped
		}
		opts.RunStore = governedStore
		opts.HumanRequestHandler = s.durableHumanRequestHandler
		opts.ToolApprovalHandler = s.durableToolApprovalHandler()
		opts.ToolApprovalVerifier = s.toolApprovalVerifier()
	}
	opts.EventEmitterDecorator = combineEmitDecorators(s.emitDecorator, extraDecorator)
	opts.ContentCapture = s.contentCapture

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

// toolApprovalTTL is the default fail-closed decision window for a
// tool-approval request that does not carry its own deadline.
const toolApprovalTTL = 24 * time.Hour

// durableToolApprovalHandler routes tool-approval requests through the
// run store so they pause the run, survive restarts, and land decisions
// in the durable approval-record ledger. Behavior mirrors
// durableHumanRequestHandler: no answered decision for the same node
// re-pauses the run, a decided pending action is consumed exactly once,
// and expired requests fail closed with a ledger record.
func (s *Server) durableToolApprovalHandler() runtime.ToolApprovalHandler {
	return func(ctx context.Context, value any) (any, error) {
		s.pendingMu.Lock()
		defer s.pendingMu.Unlock()

		req, ok := value.(*nodes.ToolApprovalRequest)
		if !ok || req == nil {
			return nil, fmt.Errorf("tool approval bridge received %T, want *nodes.ToolApprovalRequest", value)
		}
		if s.runStore == nil {
			return nil, fmt.Errorf("tool approval requires a durable run store")
		}
		record, err := s.runStore.Get(ctx, req.RunID)
		if err != nil {
			return nil, err
		}

		now := time.Now().UTC()
		req.CreatedAt = req.CreatedAt.Truncate(0).UTC()
		if req.ExpiresAt.IsZero() {
			req.ExpiresAt = req.CreatedAt.Add(toolApprovalTTL)
		}
		req.WorkflowID = record.WorkflowID
		req.TenantID = record.TenantID

		if record.PendingAction != nil && record.PendingAction.Type == runtime.PendingActionTypeToolApproval {
			pending := record.PendingAction
			if pending.NodeID == req.NodeID {
				if pending.Completed || pending.Response != nil {
					// Consume the decision exactly once.
					response, ledger := decodeToolApprovalDecision(pending)
					if ledger != nil {
						record.ApprovalRecords = append(record.ApprovalRecords, *ledger)
					}
					record.PendingAction = nil
					record.Status = runtime.RunStatusRunning
					record.UpdatedAt = now
					if err := s.runStore.Update(ctx, record); err != nil {
						return nil, err
					}
					return response, nil
				}
				// No decision yet (resume during a still-open window).
				if err := s.enforceToolApprovalExpiry(record, pending, now); err != nil {
					return nil, err
				}
				return nil, &nodes.ToolApprovalPendingError{Request: req}
			}
			return nil, fmt.Errorf("run already has a pending action %q for node %q", pending.ID, pending.NodeID)
		}

		if record.PendingAction != nil {
			return nil, fmt.Errorf("run already has a pending action for node %q", record.PendingAction.NodeID)
		}

		// The decision window can expire while the run waits without a
		// stored pending action (paused here twice across restarts).
		if !req.ExpiresAt.After(now) {
			record.ApprovalRecords = append(record.ApprovalRecords, runtime.ApprovalRecord{
				ID: req.ID, RunID: req.RunID, WorkflowID: record.WorkflowID, NodeID: req.NodeID,
				TenantID: record.TenantID, ToolName: req.ToolName, Action: req.Action,
				Reference: req.Reference, PolicySource: req.PolicySource,
				Decision: runtime.ToolApprovalExpired, Note: "decision window expired",
				CreatedAt: req.CreatedAt, DecidedAt: now, ExpiresAt: req.ExpiresAt,
			})
			record.UpdatedAt = now
			s.recordApprovalLifecycleAudit(ctx, record, req.NodeID, req.ToolName, req.Action, runtime.ToolApprovalExpired, "decision window expired")
			if err := s.runStore.Update(ctx, record); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w: approval %q for %q expired at %s",
				nodes.ErrToolApprovalExpired, req.ID, req.ToolName, req.ExpiresAt.Format(time.RFC3339))
		}

		record.PendingAction = &runtime.PendingAction{
			ID:        req.ID,
			RunID:     req.RunID,
			NodeID:    req.NodeID,
			Type:      runtime.PendingActionTypeToolApproval,
			Prompt:    fmt.Sprintf("Approve invocation of tool %q", req.ToolName),
			Data:      req,
			CreatedAt: req.CreatedAt,
		}
		record.UpdatedAt = now
		if err := s.runStore.Update(ctx, record); err != nil {
			return nil, err
		}
		s.recordApprovalLifecycleAudit(ctx, record, req.NodeID, req.ToolName, req.Action, runtime.ToolApprovalPending, "approval requested")
		return nil, &nodes.ToolApprovalPendingError{Request: req}
	}
}

// decodeToolApprovalDecision parses the stored response of a completed
// tool-approval pending action and builds its ledger entry. Unknown
// payloads fail closed.
func decodeToolApprovalDecision(pending *runtime.PendingAction) (*nodes.ToolApprovalResponse, *runtime.ApprovalRecord) {
	encoded, err := json.Marshal(pending.Response)
	if err != nil {
		return &nodes.ToolApprovalResponse{Decision: nodes.ToolApprovalDecisionRejected, Note: "undecodable decision"}, nil
	}
	var response nodes.ToolApprovalResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		response = nodes.ToolApprovalResponse{Decision: nodes.ToolApprovalDecisionRejected, Note: "undecodable decision"}
	}
	if response.Decision != nodes.ToolApprovalDecisionApproved && response.Decision != nodes.ToolApprovalDecisionRejected {
		response = nodes.ToolApprovalResponse{Decision: nodes.ToolApprovalDecisionRejected, Note: "invalid decision"}
	}

	request := decodeToolApprovalRequestData(pending.Data)
	ledger := &runtime.ApprovalRecord{
		ID: pending.ID, RunID: pending.RunID, NodeID: pending.NodeID,
		WorkflowID: request.WorkflowID, TenantID: request.TenantID,
		ToolName: request.ToolName, Action: request.Action, Reference: request.Reference,
		PolicySource: request.PolicySource,
		Decision:     runtime.ToolApprovalRejected,
		DecisionBy:   response.DecisionBy, Note: response.Note,
		CreatedAt: pending.CreatedAt, DecidedAt: response.DecisionAt,
		ExpiresAt: request.ExpiresAt,
	}
	if response.Approved() {
		ledger.Decision = runtime.ToolApprovalApproved
	}
	return &response, ledger
}

// decodeToolApprovalRequestData recovers the originating request from the
// pending action payload. A missing or corrupt payload leaves fields
// empty rather than failing the decision.
func decodeToolApprovalRequestData(data any) nodes.ToolApprovalRequest {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nodes.ToolApprovalRequest{}
	}
	var request nodes.ToolApprovalRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nodes.ToolApprovalRequest{}
	}
	return request
}

// enforceToolApprovalExpiry fails a still-open approval window when the
// pause outlasted its TTL: the ledger records expiry and the invocation
// fails deterministically.
func (s *Server) enforceToolApprovalExpiry(record *runtime.RunRecord, pending *runtime.PendingAction, now time.Time) error {
	request := decodeToolApprovalRequestData(pending.Data)
	if request.ExpiresAt.IsZero() || request.ExpiresAt.After(now) {
		return nil
	}
	record.ApprovalRecords = append(record.ApprovalRecords, runtime.ApprovalRecord{
		ID: pending.ID, RunID: pending.RunID, NodeID: pending.NodeID,
		WorkflowID: record.WorkflowID, TenantID: record.TenantID,
		ToolName: request.ToolName, Action: request.Action, Reference: request.Reference,
		PolicySource: request.PolicySource,
		Decision:     runtime.ToolApprovalExpired, Note: "decision window expired",
		CreatedAt: pending.CreatedAt, DecidedAt: now, ExpiresAt: request.ExpiresAt,
	})
	record.PendingAction = nil
	record.UpdatedAt = now
	if err := s.runStore.Update(context.Background(), record); err != nil {
		return err
	}
	s.recordApprovalLifecycleAudit(context.Background(), record, pending.NodeID, request.ToolName, request.Action, runtime.ToolApprovalExpired, "decision window expired")
	return fmt.Errorf("%w: approval %q for %q expired at %s",
		nodes.ErrToolApprovalExpired, pending.ID, request.ToolName, request.ExpiresAt.Format(time.RFC3339))
}

// recordApprovalLifecycleAudit writes approval lifecycle outcomes
// (pause requested, expiry) into the tool audit trail (best effort).
func (s *Server) recordApprovalLifecycleAudit(ctx context.Context, record *runtime.RunRecord, nodeID, toolName, action string, decision runtime.ToolApprovalDecision, reason string) {
	if s.audit == nil {
		return
	}
	actor := "system"
	s.audit.Record(ctx, tool.ToolAuditRecord{
		TenantID:   record.TenantID,
		WorkflowID: record.WorkflowID,
		RunID:      record.ID,
		NodeID:     nodeID,
		Actor:      actor,
		Tool:       toolName,
		Action:     action,
		Kind:       tool.ToolAuditApproval,
		Decision:   string(decision),
		Reason:     reason,
	})
}

// toolApprovalVerifier validates and single-use-claims an approval grant
// against the run's durable approval ledger for Guardian-declared checks.
// An empty approvalID claims the tool's unconsumed approval by tool
// name.
func (s *Server) toolApprovalVerifier() runtime.ToolApprovalVerifier {
	return func(ctx context.Context, runID, approvalID, toolName string) error {
		if s.runStore == nil {
			return fmt.Errorf("approval grants require a durable run store")
		}
		s.pendingMu.Lock()
		defer s.pendingMu.Unlock()

		record, err := s.runStore.Get(ctx, runID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		findEntry := func() *runtime.ApprovalRecord {
			if approvalID == "" {
				for i := range record.ApprovalRecords {
					entry := &record.ApprovalRecords[i]
					if entry.Executed || entry.Decision != runtime.ToolApprovalApproved {
						continue
					}
					if toolName != "" && entry.ToolName != toolName && entry.Reference != toolName {
						continue
					}
					return entry
				}
				return nil
			}
			for i := range record.ApprovalRecords {
				if record.ApprovalRecords[i].ID == approvalID {
					return &record.ApprovalRecords[i]
				}
			}
			return nil
		}

		entry := findEntry()
		if entry == nil {
			if approvalID == "" {
				return fmt.Errorf("no unconsumed approved approval for tool %q in run %q", toolName, runID)
			}
			return fmt.Errorf("approval %q not found in run %q", approvalID, runID)
		}
		if approvalID != "" && entry.ID != approvalID {
			return fmt.Errorf("approval %q not found in run %q", approvalID, runID)
		}
		if entry.Decision != runtime.ToolApprovalApproved {
			if approvalID == "" {
				return fmt.Errorf("no unconsumed approved approval for tool %q in run %q", toolName, runID)
			}
			return fmt.Errorf("approval %q is not approved (decision %q)", entry.ID, entry.Decision)
		}
		if entry.Executed {
			if approvalID == "" {
				return fmt.Errorf("no unconsumed approved approval for tool %q in run %q", toolName, runID)
			}
			return fmt.Errorf("approval %q was already consumed", entry.ID)
		}
		if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
			return fmt.Errorf("approval %q expired at %s", entry.ID, entry.ExpiresAt.Format(time.RFC3339))
		}
		if toolName != "" && entry.ToolName != "" && entry.ToolName != toolName && entry.Reference != toolName {
			return fmt.Errorf("approval %q was issued for tool %q, not %q", entry.ID, entry.ToolName, toolName)
		}
		entry.Executed = true
		record.UpdatedAt = now
		if err := s.runStore.Update(ctx, record); err != nil {
			return err
		}
		return nil
	}
}

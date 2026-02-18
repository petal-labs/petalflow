package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
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
	execGraph *graph.BasicGraph
	env       *core.Envelope
	timeout   time.Duration
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

	return s.planWorkflowRunWithDefinition(ctx, workflowID, rec.Compiled, req)
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
		execGraph: execGraph,
		env:       EnvelopeFromJSON(req.Input),
		timeout:   timeout,
	}, nil
}

func (s *Server) executeWorkflowRunSync(
	ctx context.Context,
	workflowID string,
	plan *workflowRunPlan,
	extraDecorator runtime.EventEmitterDecorator,
) (RunResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, plan.timeout)
	defer cancel()

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.EventEmitterDecorator = combineEmitDecorators(s.emitDecorator, extraDecorator)

	if s.bus != nil {
		opts.EventBus = s.bus
	}
	if s.runtimeEvents != nil {
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, s.runtimeEvents)
	}

	if s.eventStore != nil && s.bus != nil {
		sub := bus.NewStoreSubscriber(s.eventStore, s.logger)
		opts.EventHandler = runtime.MultiEventHandler(opts.EventHandler, sub.Handle)
	}

	startedAt := time.Now().UTC()
	result, err := rt.Run(runCtx, plan.execGraph, plan.env, opts)
	completedAt := time.Now().UTC()

	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return RunResponse{}, &runAPIError{Status: http.StatusGatewayTimeout, Code: "TIMEOUT", Message: err.Error()}
		}
		return RunResponse{}, &runAPIError{Status: http.StatusInternalServerError, Code: "RUNTIME_ERROR", Message: err.Error()}
	}

	runID := ""
	if result != nil {
		runID = result.Trace.RunID
	}

	return RunResponse{
		ID:          workflowID,
		RunID:       runID,
		Status:      "completed",
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
		Output:      EnvelopeToJSON(result),
	}, nil
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

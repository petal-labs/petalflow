package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// PetalTraceExecuteRequest is the replay execution request used by PetalTrace.
type PetalTraceExecuteRequest struct {
	WorkflowID          string            `json:"workflow_id"`
	WorkflowName        string            `json:"workflow_name"`
	Graph               json.RawMessage   `json:"graph"`
	Input               json.RawMessage   `json:"input"`
	Config              json.RawMessage   `json:"config,omitempty"`
	ParentRunID         string            `json:"parent_run_id,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	TriggerSource       string            `json:"trigger_source,omitempty"`
	ModelOverride       string            `json:"model_override,omitempty"`
	ProviderOverride    string            `json:"provider_override,omitempty"`
	TemperatureOverride *float64          `json:"temperature_override,omitempty"`
	MaxTokensOverride   *int              `json:"max_tokens_override,omitempty"`
}

// PetalTraceHybridExecuteRequest adds captured tool outputs to a replay request.
type PetalTraceHybridExecuteRequest struct {
	PetalTraceExecuteRequest
	MockedTools  map[string]json.RawMessage `json:"mocked_tools,omitempty"`
	ToolMockMode string                     `json:"tool_mock_mode,omitempty"`
}

// PetalTraceExecuteResponse is the run identity and terminal state returned to PetalTrace.
type PetalTraceExecuteResponse struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

func (s *Server) handlePetalTraceExecute(w http.ResponseWriter, r *http.Request) {
	var req PetalTraceExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}
	s.executePetalTraceReplay(w, r, req, nil)
}

func (s *Server) handlePetalTraceHybridExecute(w http.ResponseWriter, r *http.Request) {
	var req PetalTraceHybridExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	mode := runtime.ToolMockMode(req.ToolMockMode)
	if mode == "" {
		mode = runtime.ToolMockModeAll
	}
	if mode != runtime.ToolMockModeAll && mode != runtime.ToolMockModeNone {
		writeError(w, http.StatusBadRequest, "INVALID_TOOL_MOCK_MODE", "tool_mock_mode must be all or none")
		return
	}

	mockedTools := make(map[string]map[string]any, len(req.MockedTools))
	for nodeID, rawOutput := range req.MockedTools {
		var output map[string]any
		if err := json.Unmarshal(rawOutput, &output); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_TOOL_MOCK", fmt.Sprintf("mocked_tools[%q] must be a JSON object", nodeID))
			return
		}
		if output == nil {
			writeError(w, http.StatusBadRequest, "INVALID_TOOL_MOCK", fmt.Sprintf("mocked_tools[%q] must be a JSON object", nodeID))
			return
		}
		mockedTools[nodeID] = output
	}

	s.executePetalTraceReplay(w, r, req.PetalTraceExecuteRequest, &runtime.ToolMockSet{
		Mode:    mode,
		Results: mockedTools,
	})
}

func (s *Server) executePetalTraceReplay(
	w http.ResponseWriter,
	r *http.Request,
	req PetalTraceExecuteRequest,
	mocks *runtime.ToolMockSet,
) {
	var definition graph.GraphDefinition
	if len(req.Graph) == 0 || string(req.Graph) == "null" {
		writeError(w, http.StatusBadRequest, "INVALID_GRAPH", "graph is required")
		return
	}
	if err := json.Unmarshal(req.Graph, &definition); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_GRAPH", err.Error())
		return
	}

	workflowID := strings.TrimSpace(req.WorkflowID)
	if workflowID == "" {
		workflowID = strings.TrimSpace(definition.ID)
	}
	if workflowID == "" {
		workflowID = uuid.NewString()
	}
	definition.ID = workflowID
	if req.WorkflowName != "" {
		if definition.Metadata == nil {
			definition.Metadata = make(map[string]string)
		}
		definition.Metadata["name"] = req.WorkflowName
	}
	applyPetalTraceOverrides(&definition, req)

	var input map[string]any
	if len(req.Input) != 0 && string(req.Input) != "null" {
		if err := json.Unmarshal(req.Input, &input); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_INPUT", err.Error())
			return
		}
	}
	var config any
	if len(req.Config) != 0 && string(req.Config) != "null" {
		if err := json.Unmarshal(req.Config, &config); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CONFIG", err.Error())
			return
		}
	}

	if mocks != nil && mocks.Mode == runtime.ToolMockModeAll {
		if err := validateToolMocks(&definition, mocks.Results); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_TOOL_MOCK", err.Error())
			return
		}
	}

	plan, err := s.planWorkflowRunWithDefinition(r.Context(), workflowID, &definition, RunRequest{
		Input: input,
	})
	if err != nil {
		writeRunAPIError(w, err)
		return
	}
	plan.tenantID = s.tenantID(r)
	plan.env.Trace.RunID = uuid.NewString()
	plan.graphDefinition = &definition
	plan.inputs = input
	plan.configSnapshot = config
	plan.captureSnapshots = s.captureSnapshots
	plan.triggerSource = req.TriggerSource
	if plan.triggerSource == "" {
		plan.triggerSource = "petaltrace-replay"
	}
	plan.toolMocks = mocks

	result, err := s.executeWorkflowRunSync(r.Context(), workflowID, plan, petalTraceReplayDecorator(req))
	if err != nil {
		writeRunAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PetalTraceExecuteResponse{
		RunID:  result.RunID,
		Status: result.Status,
	})
}

func applyPetalTraceOverrides(definition *graph.GraphDefinition, req PetalTraceExecuteRequest) {
	for i := range definition.Nodes {
		node := &definition.Nodes[i]
		if node.Type != "llm_prompt" && node.Type != "llm" {
			continue
		}
		if node.Config == nil {
			node.Config = make(map[string]any)
		}
		if req.ModelOverride != "" {
			node.Config["model"] = req.ModelOverride
		}
		if req.ProviderOverride != "" {
			node.Config["provider"] = req.ProviderOverride
		}
		if req.TemperatureOverride != nil {
			node.Config["temperature"] = *req.TemperatureOverride
		}
		if req.MaxTokensOverride != nil {
			node.Config["max_tokens"] = *req.MaxTokensOverride
		}
	}
}

func validateToolMocks(definition *graph.GraphDefinition, mocks map[string]map[string]any) error {
	toolNodes := make(map[string]struct{})
	for _, node := range definition.Nodes {
		if node.Type == "tool" {
			toolNodes[node.ID] = struct{}{}
			if _, ok := mocks[node.ID]; !ok {
				return fmt.Errorf("missing captured result for tool node %q", node.ID)
			}
		}
	}
	for nodeID := range mocks {
		if _, ok := toolNodes[nodeID]; !ok {
			return fmt.Errorf("mocked_tools key %q is not a tool node ID in graph", nodeID)
		}
	}
	return nil
}

func petalTraceReplayDecorator(req PetalTraceExecuteRequest) runtime.EventEmitterDecorator {
	return func(emit runtime.EventEmitter) runtime.EventEmitter {
		return func(event runtime.Event) {
			if event.Kind == runtime.EventRunStarted {
				if req.WorkflowName != "" {
					event = event.WithPayload("workflow_name", req.WorkflowName)
				}
				if req.ParentRunID != "" {
					event = event.WithPayload("parent_run_id", req.ParentRunID)
				}
				event = event.WithPayload("source_kind", "petalflow-replay")
				for key, value := range req.Tags {
					event = event.WithPayload("petalflow.tag."+key, value)
				}
			}
			emit(event)
		}
	}
}

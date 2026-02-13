package server

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

type traceEventResponse struct {
	Timestamp string         `json:"timestamp"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
}

type traceSpanMetadata struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	TokensIn  int     `json:"tokens_in,omitempty"`
	TokensOut int     `json:"tokens_out,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`

	ToolName   string `json:"tool_name,omitempty"`
	ActionName string `json:"action_name,omitempty"`

	Retries int `json:"retries,omitempty"`
}

type traceSpanResponse struct {
	SpanID       string  `json:"span_id"`
	NodeID       string  `json:"node_id"`
	NodeType     string  `json:"node_type"`
	ParentSpanID *string `json:"parent_span_id"`

	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DurationMs  int64  `json:"duration_ms"`
	Status      string `json:"status"`

	Inputs  map[string]any       `json:"inputs"`
	Outputs map[string]any       `json:"outputs"`
	Events  []traceEventResponse `json:"events"`

	Metadata traceSpanMetadata `json:"metadata"`
}

type traceSpanState struct {
	span      traceSpanResponse
	startedAt time.Time
	endedAt   time.Time
}

func buildRunTraceResponse(runID, workflowID string, fallback *RunResponse, events []runtime.Event) runTraceResponse {
	if len(events) == 0 {
		return buildRunTraceFromFallback(runID, workflowID, fallback)
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Seq != events[j].Seq {
			return events[i].Seq < events[j].Seq
		}
		return events[i].Time.Before(events[j].Time)
	})

	startedAt := events[0].Time
	completedAt := events[len(events)-1].Time
	status := "completed"

	if fallback != nil {
		status = normalizeTraceStatus(fallback.Status)
		if workflowID == "" {
			workflowID = firstNonEmptyString(strings.TrimSpace(fallback.WorkflowID), strings.TrimSpace(fallback.ID))
		}
		if !fallback.StartedAt.IsZero() {
			startedAt = fallback.StartedAt
		}
		if !fallback.CompletedAt.IsZero() {
			completedAt = fallback.CompletedAt
		}
	}

	for _, evt := range events {
		switch evt.Kind {
		case runtime.EventRunStarted:
			startedAt = evt.Time
			if workflowID == "" {
				if graphID, ok := evt.Payload["graph"].(string); ok {
					workflowID = strings.TrimSpace(graphID)
				}
			}
		case runtime.EventRunFinished:
			completedAt = evt.Time
			if rawStatus, ok := evt.Payload["status"].(string); ok {
				status = normalizeTraceStatus(rawStatus)
			}
		}
	}

	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	if completedAt.IsZero() || completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	if completedAt.Equal(startedAt) {
		completedAt = startedAt.Add(time.Millisecond)
	}

	spans := buildTraceSpans(events, startedAt, completedAt)
	if len(spans) == 0 {
		fallbackTrace := buildRunTraceFromFallback(runID, workflowID, fallback)
		spans = fallbackTrace.Spans
	}

	duration := completedAt.Sub(startedAt).Milliseconds()
	if duration <= 0 {
		duration = 1
	}

	return runTraceResponse{
		RunID:       runID,
		WorkflowID:  firstNonEmptyString(workflowID, runID),
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		CompletedAt: completedAt.UTC().Format(time.RFC3339),
		DurationMs:  duration,
		Status:      normalizeTraceStatus(status),
		Spans:       spans,
	}
}

func buildRunTraceFromFallback(runID, workflowID string, fallback *RunResponse) runTraceResponse {
	now := time.Now().UTC()
	if fallback == nil {
		return runTraceResponse{
			RunID:       runID,
			WorkflowID:  firstNonEmptyString(workflowID, runID),
			StartedAt:   now.Format(time.RFC3339),
			CompletedAt: now.Add(time.Millisecond).Format(time.RFC3339),
			DurationMs:  1,
			Status:      "completed",
			Spans:       []traceSpanResponse{},
		}
	}

	startedAt := fallback.StartedAt
	completedAt := fallback.CompletedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	if completedAt.IsZero() || completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	if completedAt.Equal(startedAt) {
		completedAt = startedAt.Add(time.Millisecond)
	}

	outputs := cloneAnyMap(fallback.Outputs)
	if outputs == nil {
		outputs = cloneAnyMap(fallback.Output.Vars)
	}
	if outputs == nil {
		outputs = map[string]any{}
	}
	if fallback.Error != nil {
		outputs["error"] = fallback.Error.Message
	}

	nodeID := firstNonEmptyString(strings.TrimSpace(fallback.WorkflowID), strings.TrimSpace(fallback.ID), "run")
	span := traceSpanResponse{
		SpanID:       "run_span_1",
		NodeID:       nodeID,
		NodeType:     "run",
		ParentSpanID: nil,
		StartedAt:    startedAt.UTC().Format(time.RFC3339),
		CompletedAt:  completedAt.UTC().Format(time.RFC3339),
		DurationMs:   completedAt.Sub(startedAt).Milliseconds(),
		Status: func() string {
			if fallback.Error != nil || normalizeTraceStatus(fallback.Status) == "failed" {
				return "error"
			}
			return "ok"
		}(),
		Inputs:   cloneAnyMap(fallback.Inputs),
		Outputs:  outputs,
		Events:   []traceEventResponse{},
		Metadata: traceSpanMetadata{},
	}
	if span.DurationMs < 0 {
		span.DurationMs = 0
	}
	if span.Inputs == nil {
		span.Inputs = map[string]any{}
	}

	return runTraceResponse{
		RunID:       runID,
		WorkflowID:  firstNonEmptyString(workflowID, fallback.WorkflowID, fallback.ID, runID),
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		CompletedAt: completedAt.UTC().Format(time.RFC3339),
		DurationMs: func() int64 {
			d := completedAt.Sub(startedAt).Milliseconds()
			if d <= 0 {
				return 1
			}
			return d
		}(),
		Status: normalizeTraceStatus(fallback.Status),
		Spans:  []traceSpanResponse{span},
	}
}

func buildTraceSpans(events []runtime.Event, runStart, runEnd time.Time) []traceSpanResponse {
	states := make([]*traceSpanState, 0)
	openByNode := map[string][]*traceSpanState{}
	byNode := map[string][]*traceSpanState{}
	nodeSeq := map[string]int{}

	newState := func(nodeID, nodeType string, at time.Time) *traceSpanState {
		cleanID := sanitizeTraceID(nodeID)
		if cleanID == "" {
			cleanID = "node"
		}
		nodeSeq[cleanID]++

		state := &traceSpanState{
			span: traceSpanResponse{
				SpanID:       fmt.Sprintf("%s_span_%d", cleanID, nodeSeq[cleanID]),
				NodeID:       firstNonEmptyString(strings.TrimSpace(nodeID), "unknown_node"),
				NodeType:     firstNonEmptyString(strings.TrimSpace(nodeType), "unknown"),
				ParentSpanID: nil,
				Status:       "ok",
				Inputs:       map[string]any{},
				Outputs:      map[string]any{},
				Events:       []traceEventResponse{},
				Metadata:     traceSpanMetadata{},
			},
			startedAt: at,
			endedAt:   at,
		}
		states = append(states, state)
		byNode[state.span.NodeID] = append(byNode[state.span.NodeID], state)
		return state
	}

	latestState := func(nodeID, nodeType string, at time.Time, create bool) *traceSpanState {
		if nodeID == "" {
			return nil
		}
		if open := openByNode[nodeID]; len(open) > 0 {
			return open[len(open)-1]
		}
		if nodeStates := byNode[nodeID]; len(nodeStates) > 0 {
			return nodeStates[len(nodeStates)-1]
		}
		if !create {
			return nil
		}
		return newState(nodeID, nodeType, at)
	}

	popOpen := func(nodeID, nodeType string, at time.Time) *traceSpanState {
		open := openByNode[nodeID]
		if len(open) == 0 {
			return latestState(nodeID, nodeType, at, true)
		}
		state := open[len(open)-1]
		openByNode[nodeID] = open[:len(open)-1]
		return state
	}

	appendTraceEvent := func(state *traceSpanState, evt runtime.Event) {
		if state == nil {
			return
		}
		payload := cloneAnyMap(evt.Payload)
		if payload == nil {
			payload = map[string]any{}
		}
		state.span.Events = append(state.span.Events, traceEventResponse{
			Timestamp: evt.Time.UTC().Format(time.RFC3339Nano),
			Type:      traceEventType(evt.Kind),
			Data:      payload,
		})
	}

	for _, evt := range events {
		nodeID := strings.TrimSpace(evt.NodeID)
		nodeType := strings.TrimSpace(string(evt.NodeKind))

		switch evt.Kind {
		case runtime.EventNodeStarted:
			state := newState(nodeID, nodeType, evt.Time)
			openByNode[state.span.NodeID] = append(openByNode[state.span.NodeID], state)
			appendTraceEvent(state, evt)
		case runtime.EventNodeFinished:
			state := popOpen(nodeID, nodeType, evt.Time)
			state.endedAt = evt.Time
			appendTraceEvent(state, evt)
		case runtime.EventNodeFailed:
			state := popOpen(nodeID, nodeType, evt.Time)
			state.endedAt = evt.Time
			state.span.Status = "error"
			if errText, ok := evt.Payload["error"].(string); ok && strings.TrimSpace(errText) != "" {
				state.span.Outputs["error"] = errText
			}
			appendTraceEvent(state, evt)
		case runtime.EventNodeOutputFinal:
			state := latestState(nodeID, nodeType, evt.Time, true)
			if txt, ok := evt.Payload["text"].(string); ok && txt != "" {
				state.span.Outputs["text"] = txt
			} else if len(evt.Payload) > 0 {
				state.span.Outputs["output"] = cloneAnyMap(evt.Payload)
			}
			appendTraceEvent(state, evt)
		case runtime.EventNodeOutputDelta:
			state := latestState(nodeID, nodeType, evt.Time, true)
			if delta, ok := evt.Payload["delta"].(string); ok && delta != "" {
				prev, _ := state.span.Outputs["stream"].(string)
				state.span.Outputs["stream"] = prev + delta
			}
			appendTraceEvent(state, evt)
		case runtime.EventToolCall:
			state := latestState(nodeID, nodeType, evt.Time, true)
			if toolName, ok := evt.Payload["tool_name"].(string); ok {
				state.span.Metadata.ToolName = toolName
			}
			if actionName, ok := evt.Payload["action_name"].(string); ok {
				state.span.Metadata.ActionName = actionName
			}
			if args, ok := evt.Payload["arguments"]; ok {
				state.span.Inputs["tool_arguments"] = args
			}
			appendTraceEvent(state, evt)
		case runtime.EventToolResult:
			state := latestState(nodeID, nodeType, evt.Time, true)
			if toolName, ok := evt.Payload["tool_name"].(string); ok {
				state.span.Metadata.ToolName = toolName
			}
			if actionName, ok := evt.Payload["action_name"].(string); ok {
				state.span.Metadata.ActionName = actionName
			}
			state.span.Outputs["tool_result"] = cloneAnyMap(evt.Payload)
			appendTraceEvent(state, evt)
		default:
			state := latestState(nodeID, nodeType, evt.Time, false)
			appendTraceEvent(state, evt)
		}
	}

	for _, open := range openByNode {
		for _, state := range open {
			if runEnd.After(state.endedAt) {
				state.endedAt = runEnd
			}
		}
	}

	sort.SliceStable(states, func(i, j int) bool {
		if !states[i].startedAt.Equal(states[j].startedAt) {
			return states[i].startedAt.Before(states[j].startedAt)
		}
		if states[i].span.NodeID != states[j].span.NodeID {
			return states[i].span.NodeID < states[j].span.NodeID
		}
		return states[i].span.SpanID < states[j].span.SpanID
	})

	out := make([]traceSpanResponse, 0, len(states))
	for _, state := range states {
		if state.startedAt.IsZero() {
			state.startedAt = runStart
		}
		if state.endedAt.IsZero() || state.endedAt.Before(state.startedAt) {
			state.endedAt = state.startedAt
		}
		state.span.StartedAt = state.startedAt.UTC().Format(time.RFC3339)
		state.span.CompletedAt = state.endedAt.UTC().Format(time.RFC3339)
		state.span.DurationMs = state.endedAt.Sub(state.startedAt).Milliseconds()
		if state.span.DurationMs < 0 {
			state.span.DurationMs = 0
		}
		if state.span.Inputs == nil {
			state.span.Inputs = map[string]any{}
		}
		if state.span.Outputs == nil {
			state.span.Outputs = map[string]any{}
		}
		out = append(out, state.span)
	}

	return out
}

func sanitizeTraceID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ".", "_", ":", "_")
	return replacer.Replace(trimmed)
}

func normalizeTraceStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return "completed"
	}
}

func traceEventType(kind runtime.EventKind) string {
	switch kind {
	case runtime.EventToolCall:
		return "tool_call"
	case runtime.EventToolResult:
		return "tool_result"
	case runtime.EventNodeFailed:
		return "error"
	case runtime.EventStepPaused:
		return "review_requested"
	case runtime.EventStepResumed:
		return "review_completed"
	case runtime.EventNodeStarted:
		return "llm_request"
	default:
		return "llm_response"
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

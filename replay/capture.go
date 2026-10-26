package replay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// Capture records a run into a redacted Recording. It implements
// runtime.EventHandler (event stream) and runtime.NodeRecorder (per-node
// envelope state); wire both into RunOptions:
//
//	capture := replay.NewCapture(replay.DefaultPolicy())
//	opts.EventHandler = capture.Handle
//	opts.NodeRecorder = capture
//	// ... run ...
//	recording := capture.Finish()
//	replay.Save(recording, "run.json")
//
// Capture is safe for concurrent use (parallel node execution). The capture
// is purely observational and does not alter execution semantics: redaction
// happens as content is captured, so sensitive content is never retained.
type Capture struct {
	policy RedactionPolicy

	mu      sync.Mutex
	rec     *Recording
	current map[string]*NodeRecord
}

// NewCapture starts a capture with the given policy. The zero-value
// RedactionPolicy is the default-deny policy.
func NewCapture(policy RedactionPolicy) *Capture {
	return &Capture{
		policy:  policy,
		current: make(map[string]*NodeRecord),
		rec:     &Recording{Version: FormatVersion, Nodes: []NodeRecord{}},
	}
}

// Handle implements runtime.EventHandler. It folds run, node, LLM, and tool
// events into the recording.
func (c *Capture) Handle(e runtime.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch e.Kind {
	case runtime.EventRunStarted:
		c.rec.RunID = e.RunID
		c.rec.StartedAt = e.Time
		if name, ok := e.Payload["graph"].(string); ok {
			c.rec.GraphName = name
		}
		if trigger, ok := e.Payload["trigger"].(string); ok {
			c.rec.Trigger = trigger
		}
		if id, ok := e.Payload["workflow_id"].(string); ok {
			c.rec.WorkflowID = id
		}
		if version, ok := e.Payload["workflow_version"].(string); ok {
			c.rec.WorkflowVersion = version
		}
		if def, ok := e.Payload["graph_definition"]; ok {
			// Workflow structure, not run content: kept as-is.
			c.rec.GraphDefinition = def
		}
		if inputs, ok := e.Payload["inputs"]; ok {
			// Run content: redacted like other outputs.
			c.rec.Inputs = c.policy.redactValue(inputs, "output", c.policy.IncludeOutputs)
		}

	case runtime.EventNodeStarted:
		record := &NodeRecord{
			NodeID:   e.NodeID,
			NodeKind: string(e.NodeKind),
		}
		c.rec.Nodes = append(c.rec.Nodes, *record)
		c.current[e.NodeID] = &c.rec.Nodes[len(c.rec.Nodes)-1]

	case runtime.EventNodeFinished:
		if record := c.current[e.NodeID]; record != nil {
			record.ElapsedMS = e.Elapsed.Milliseconds()
		}

	case runtime.EventNodeFailed:
		if record := c.current[e.NodeID]; record != nil {
			record.ElapsedMS = e.Elapsed.Milliseconds()
			record.Error = eventString(e, "error")
		}

	case runtime.EventLLMCall:
		record := c.current[e.NodeID]
		if record == nil {
			break
		}
		call := LLMCallRecord{
			NodeID:    e.NodeID,
			Model:     eventString(e, "model"),
			System:    eventString(e, "system_prompt"),
			InputText: eventString(e, "input_text"),
		}
		if raw, ok := e.Payload["messages"].(string); ok && raw != "" {
			var messages []core.LLMMessage
			if err := json.Unmarshal([]byte(raw), &messages); err == nil {
				for _, m := range messages {
					call.Messages = append(call.Messages, LLMMessageRef{
						Role:    m.Role,
						Name:    m.Name,
						Content: m.Content,
					})
				}
			}
		}
		c.policy.applyLLM(&call)
		record.LLM = append(record.LLM, call)

	case runtime.EventLLMResponse:
		record := c.current[e.NodeID]
		if record == nil {
			break
		}
		// The response completes the most recent unanswered call.
		for i := len(record.LLM) - 1; i >= 0; i-- {
			if !callAnswered(&record.LLM[i]) {
				call := &record.LLM[i]
				call.Completion = eventString(e, "completion")
				call.StopReason = eventString(e, "stop_reason")
				call.Error = eventString(e, "error")
				if latency := eventLatency(e, "latency_ms"); latency > 0 {
					call.LatencyMS = latency
				}
				call.Usage = UsageRecord{
					InputTokens:         eventInt(e, "input_tokens"),
					OutputTokens:        eventInt(e, "output_tokens"),
					TotalTokens:         eventInt(e, "total_tokens"),
					CacheReadTokens:     eventInt(e, "cache_read_tokens"),
					CacheCreationTokens: eventInt(e, "cache_creation_tokens"),
				}
				c.policy.applyLLM(call)
				break
			}
		}

	case runtime.EventToolCall:
		record := c.current[e.NodeID]
		if record == nil {
			break
		}
		call := ToolCallRecord{
			NodeID:   e.NodeID,
			ToolName: eventString(e, "tool_name"),
		}
		if args, ok := e.Payload["arguments"].(map[string]any); ok {
			call.Args = args
		}
		c.policy.applyTool(&call)
		record.Tools = append(record.Tools, call)

	case runtime.EventToolResult:
		record := c.current[e.NodeID]
		if record == nil {
			break
		}
		for i := len(record.Tools) - 1; i >= 0; i-- {
			if record.Tools[i].Result == nil && !record.Tools[i].IsError {
				tool := &record.Tools[i]
				if name := eventString(e, "tool_name"); name != "" && tool.ToolName != name {
					continue
				}
				tool.IsError = eventBool(e, "is_error")
				c.policy.applyTool(tool)
				break
			}
		}

	case runtime.EventRunFinished:
		c.rec.FinishedAt = e.Time
		if status, ok := e.Payload["status"].(string); ok {
			c.rec.Status = status
		} else {
			c.rec.Status = "succeeded"
		}
		if errStr, ok := e.Payload["error"].(string); ok {
			c.rec.FinalError = errStr
		}
	}
}

// RecordNode implements runtime.NodeRecorder. It snapshots the node's input
// and output envelopes (cloned, then redacted per policy).
func (c *Capture) RecordNode(nodeID string, kind core.NodeKind, input, output *core.Envelope, runErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record := c.current[nodeID]
	if record == nil {
		// Node execution without a preceding started event (e.g. a capture
		// attached mid-run): record it anyway so envelopes are not lost.
		c.rec.Nodes = append(c.rec.Nodes, NodeRecord{NodeID: nodeID, NodeKind: string(kind)})
		record = &c.rec.Nodes[len(c.rec.Nodes)-1]
		c.current[nodeID] = record
	}

	record.Input = c.snapshotEnvelope(input)
	if runErr != nil && record.Error == "" {
		record.Error = runErr.Error()
	}
	if output != nil {
		record.Output = c.snapshotEnvelope(output)
	}
}

// Finish finalizes the capture and returns the recording. The Capture
// remains usable for a subsequent run only if reused intentionally; typical
// callers capture one run per Capture.
func (c *Capture) Finish() *Recording {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rec.FinishedAt.IsZero() {
		c.rec.FinishedAt = time.Now()
	}
	if c.rec.Status == "" {
		c.rec.Status = "unknown"
	}
	return c.rec
}

// snapshotEnvelope clones the envelope and reduces it to the captured
// state, applying redaction.
func (c *Capture) snapshotEnvelope(env *core.Envelope) *EnvelopeState {
	if env == nil {
		return nil
	}

	clone := env.Clone()
	state := &EnvelopeState{
		Input: clone.Input,
	}
	if len(clone.Vars) > 0 {
		state.Vars = clone.Vars
	}

	for _, artifact := range clone.Artifacts {
		state.Artifacts = append(state.Artifacts, ArtifactState{
			ID:       artifact.ID,
			Type:     artifact.Type,
			MimeType: artifact.MimeType,
			URI:      artifact.URI,
			Text:     artifact.Text,
			Bytes:    artifact.Bytes,
			Meta:     artifact.Meta,
		})
	}

	for _, message := range clone.Messages {
		state.Messages = append(state.Messages, MessageState{
			Role:    message.Role,
			Name:    message.Name,
			Content: message.Content,
			Meta:    message.Meta,
		})
	}

	c.policy.applyEnvelope(state)
	return state
}

// callAnswered reports whether the LLM call already has a response.
func callAnswered(call *LLMCallRecord) bool {
	return call.Completion != "" || call.StopReason != "" || call.Error != "" ||
		call.Usage.TotalTokens != 0 || call.Usage.InputTokens != 0
}

// eventLatency extracts a millisecond duration payload field.
func eventLatency(e runtime.Event, key string) int64 {
	switch v := e.Payload[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// eventString extracts a string payload field.
func eventString(e runtime.Event, key string) string {
	s, _ := e.Payload[key].(string)
	return s
}

// eventInt extracts an integer payload field (events may carry int, int64,
// or JSON-decoded float64).
func eventInt(e runtime.Event, key string) int {
	switch v := e.Payload[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// eventBool extracts a bool payload field.
func eventBool(e runtime.Event, key string) bool {
	b, _ := e.Payload[key].(bool)
	return b
}

// Save writes the recording to path with restrictive permissions (recordings
// may contain sensitive content even when redacted). The recording must
// pass Validate.
func Save(rec *Recording, path string) error {
	if err := rec.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("replay: encode recording: %w", err)
	}

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("replay: create directory: %w", err)
		}
	}
	// 0600: recordings are treated as sensitive artifacts by default.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("replay: write recording: %w", err)
	}
	return nil
}

// Load reads and validates a recording from path. Recordings with an
// incompatible schema version are rejected.
func Load(path string) (*Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("replay: read recording: %w", err)
	}

	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("replay: decode recording: %w", err)
	}
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Summarize returns a compact human-readable summary of the recording for
// reports and logs. It contains no content, only structure and metrics.
func Summarize(rec *Recording) string {
	if rec == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run %s (%s)", rec.RunID, rec.Status)
	if rec.GraphName != "" {
		fmt.Fprintf(&b, " graph=%s", rec.GraphName)
	}
	fmt.Fprintf(&b, " nodes=%d", len(rec.Nodes))
	tokens, cost := 0, 0.0
	for _, node := range rec.Nodes {
		for _, call := range node.LLM {
			tokens += call.Usage.TotalTokens
			cost += call.Usage.CostUSD
		}
	}
	if tokens > 0 {
		fmt.Fprintf(&b, " tokens=%d", tokens)
	}
	if cost > 0 {
		fmt.Fprintf(&b, " cost_usd=%.4f", cost)
	}
	return b.String()
}

package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/petal-labs/petalflow/budget"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
)

// LLMNodeConfig configures an LLMNode.
type LLMNodeConfig struct {
	// Model is the model identifier (e.g., "gpt-4", "claude-3-opus").
	Model string

	// System is the system prompt for the LLM.
	System string

	// PromptTemplate is a Go text/template for constructing the user prompt.
	// Variables from the envelope can be accessed via {{.varname}}.
	// If empty, InputVars are concatenated with newlines.
	PromptTemplate string

	// InputVars specifies which envelope variables to include in the prompt.
	InputVars []string

	// OutputKey is the envelope variable name to store the LLM output.
	OutputKey string

	// JSONSchema enables structured output with the specified schema.
	// The LLM will be instructed to output valid JSON matching this schema.
	JSONSchema map[string]any

	// Temperature controls randomness (0.0 = deterministic, 1.0 = creative).
	Temperature *float64

	// MaxTokens limits the output length.
	MaxTokens *int

	// RetryPolicy configures retry behavior for transient failures.
	RetryPolicy core.RetryPolicy

	// Timeout is the maximum time to wait for the LLM response.
	Timeout time.Duration

	// Budget sets resource limits for the LLM call.
	Budget *core.Budget

	// RecordMessages appends the conversation to envelope.Messages.
	RecordMessages bool
}

// LLMNode executes an LLM call as a workflow step.
type LLMNode struct {
	core.BaseNode
	config LLMNodeConfig
	client core.LLMClient

	// schema is the compiled JSONSchema (nil if none configured). schemaErr
	// holds a compile error to surface at run time, since NewLLMNode has no
	// error return.
	schema    *jsonschema.Schema
	schemaErr error
}

// NewLLMNode creates a new LLM node with the given configuration.
func NewLLMNode(id string, client core.LLMClient, config LLMNodeConfig) *LLMNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = core.DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}

	node := &LLMNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindLLM),
		config:   config,
		client:   client,
	}
	if config.JSONSchema != nil {
		node.schema, node.schemaErr = compileJSONSchema(config.JSONSchema)
	}
	return node
}

// Run executes the LLM call and stores the result in the envelope.
func (n *LLMNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	emit := runtime.EmitterFromContext(ctx)

	// Apply timeout
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	// Build the prompt
	prompt, err := n.buildPrompt(env)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// If the client supports streaming, use the streaming path
	if streamClient, ok := n.client.(core.StreamingLLMClient); ok {
		return n.runStreaming(ctx, env, streamClient, emit, prompt)
	}
	return n.runSync(ctx, env, emit, prompt)
}

// runSync executes a synchronous (non-streaming) LLM call.
// buildLLMRequest constructs the request from node config, attaching
// node and run identity in Meta so client-side layers (capability
// preflight, routing, budgets) can attribute their errors and events.
func (n *LLMNode) buildLLMRequest(env *core.Envelope, prompt string) core.LLMRequest {
	req := core.LLMRequest{
		Model:      n.config.Model,
		System:     n.config.System,
		InputText:  prompt,
		JSONSchema: n.config.JSONSchema,
	}
	if n.config.Temperature != nil {
		req.Temperature = n.config.Temperature
	}
	if n.config.MaxTokens != nil {
		req.MaxTokens = n.config.MaxTokens
	}
	req.Meta = map[string]any{
		"node_id": n.ID(),
		"run_id":  env.Trace.RunID,
	}
	return req
}

func (n *LLMNode) runSync(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, prompt string) (*core.Envelope, error) {
	// Build the LLM request
	req := n.buildLLMRequest(env, prompt)

	// Budget enforcement: the run ledger (from context) and the node
	// budget (from config) are re-checked before every dispatch attempt
	// and recorded after — retries cannot bypass a ceiling that a parallel
	// branch drained mid-run.
	checkBudgetSpend, recordSpend, err := n.prepareBudget(ctx, env, emit, req)
	if err != nil {
		return nil, err
	}

	// Execute with retries
	var resp core.LLMResponse
	var lastErr error

	for attempt := 1; attempt <= n.config.RetryPolicy.MaxAttempts; attempt++ {
		if err := checkBudgetSpend(); err != nil {
			return nil, err
		}
		resp, lastErr = n.client.Complete(ctx, req)
		if lastErr == nil {
			break
		}

		// Check if context is done
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Wait before retry (except on last attempt)
		if attempt < n.config.RetryPolicy.MaxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(n.config.RetryPolicy.Backoff * time.Duration(attempt)):
			}
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("LLM call failed after %d attempts: %w", n.config.RetryPolicy.MaxAttempts, lastErr)
	}

	// Record actual spend after a successful dispatch.
	if recordSpend != nil {
		recordSpend(resp.Usage)
	}

	// Per-call budget limits (input/output caps) still apply post-call.
	if n.config.Budget != nil {
		if err := n.checkBudget(resp.Usage); err != nil {
			return nil, err
		}
	}

	// Emit node.output.final event
	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("text", resp.Text))

	// Store output in envelope, honoring structured output.
	output, err := n.finalizeOutput(resp.Text, resp.JSON)
	if err != nil {
		return nil, err
	}
	env.SetVar(n.config.OutputKey, output)

	// Record token usage
	env.SetVar(n.config.OutputKey+"_usage", core.TokenUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.TotalTokens,
		CostUSD:      resp.Usage.CostUSD,
	})

	// Record messages if configured
	if n.config.RecordMessages {
		env.AppendMessage(core.Message{
			Role:    "user",
			Content: prompt,
			Name:    n.ID(),
		})
		env.AppendMessage(core.Message{
			Role:    "assistant",
			Content: resp.Text,
			Name:    n.ID(),
			Meta: map[string]any{
				"model":    resp.Model,
				"provider": resp.Provider,
			},
		})
	}

	return env, nil
}

// runStreaming executes a streaming LLM call, emitting delta events for each chunk.
func (n *LLMNode) runStreaming(ctx context.Context, env *core.Envelope, streamClient core.StreamingLLMClient, emit runtime.EventEmitter, prompt string) (*core.Envelope, error) {
	// Build the LLM request
	req := n.buildLLMRequest(env, prompt)

	// Budget enforcement (mirrors the sync path).
	checkBudgetSpend, recordSpend, err := n.prepareBudget(ctx, env, emit, req)
	if err != nil {
		return nil, err
	}
	if err := checkBudgetSpend(); err != nil {
		return nil, err
	}

	// Start streaming
	ch, err := streamClient.CompleteStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("streaming LLM call failed: %w", err)
	}

	// Read chunks, accumulate text, emit delta events
	var accumulated strings.Builder
	var usage core.LLMTokenUsage

	streaming := true
	for streaming {
		// Select on ctx.Done() so a client that stalls (never sends, never
		// closes the channel) cannot hang the node past its timeout/cancel.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				streaming = false
				break
			}

			// Handle chunk errors
			if chunk.Error != nil {
				return nil, fmt.Errorf("streaming error: %w", chunk.Error)
			}

			if chunk.Done {
				// Capture usage from the final chunk
				if chunk.Usage != nil {
					usage = *chunk.Usage
				}
				streaming = false
				break
			}

			// Accumulate text
			accumulated.WriteString(chunk.Delta)

			// Emit delta event
			emit(runtime.NewEvent(runtime.EventNodeOutputDelta, env.Trace.RunID).
				WithNode(n.ID(), n.Kind()).
				WithPayload("delta", chunk.Delta).
				WithPayload("index", chunk.Index))
		}
	}

	text := accumulated.String()

	// Record actual spend after a successful dispatch.
	if recordSpend != nil {
		recordSpend(usage)
	}

	// Per-call budget limits (input/output caps) still apply post-call.
	if n.config.Budget != nil {
		if err := n.checkBudget(usage); err != nil {
			return nil, err
		}
	}

	// Emit node.output.final event
	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("text", text))

	// Store output in envelope, honoring structured output.
	output, err := n.finalizeOutput(text, nil)
	if err != nil {
		return nil, err
	}
	env.SetVar(n.config.OutputKey, output)

	// Record token usage
	env.SetVar(n.config.OutputKey+"_usage", core.TokenUsage(usage))

	// Record messages if configured
	if n.config.RecordMessages {
		env.AppendMessage(core.Message{
			Role:    "user",
			Content: prompt,
			Name:    n.ID(),
		})
		env.AppendMessage(core.Message{
			Role:    "assistant",
			Content: text,
			Name:    n.ID(),
		})
	}

	return env, nil
}

// buildPrompt constructs the prompt from envelope variables.
func (n *LLMNode) buildPrompt(env *core.Envelope) (string, error) {
	// If a template is provided, use it
	if n.config.PromptTemplate != "" {
		return n.executeTemplate(env)
	}

	// Otherwise, concatenate input variables
	var parts []string
	for _, varName := range n.config.InputVars {
		if val, ok := env.GetVar(varName); ok {
			parts = append(parts, toString(val))
		}
	}

	return strings.Join(parts, "\n"), nil
}

// executeTemplate executes the prompt template with envelope variables.
func (n *LLMNode) executeTemplate(env *core.Envelope) (string, error) {
	tmpl, err := template.New("prompt").Parse(n.config.PromptTemplate)
	if err != nil {
		return "", fmt.Errorf("invalid prompt template: %w", err)
	}

	// Create template data from vars
	data := make(map[string]any)
	if env.Vars != nil {
		for k, v := range env.Vars {
			data[k] = v
		}
	}
	// Also add input if present
	if env.Input != nil {
		data["input"] = env.Input
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// prepareBudget resolves budget enforcement for one LLM call: the run
// ledger (from context, if any) and the node budget (from config, if any)
// are preflighted against the estimated prompt size, and the returned
// function records actual spend after a successful dispatch.
//
// Cost estimates are unavailable at the node layer when the model's price
// is unknown (#206): the token gate always applies, the cost gate applies
// when actual usage carries a cost.
func (n *LLMNode) prepareBudget(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, req core.LLMRequest) (func() error, func(core.LLMTokenUsage), error) {
	estimate := memory.EstimateTokens(req.System) + memory.EstimateTokens(req.InputText)

	// Node-scoped ledger from the configured budget.
	var nodeLedger *budget.Ledger
	if b := n.config.Budget; b != nil {
		ceiling := budget.Ceiling{MaxTotalTokens: b.MaxTotalTokens, MaxCostUSD: b.MaxCostUSD}
		ledger, err := budget.NewLedger(ceiling)
		if err != nil {
			return nil, nil, fmt.Errorf("node %s: %w", n.ID(), err)
		}
		nodeLedger = ledger
	}

	runLedger := budget.LedgerFromContext(ctx)

	// Pre-dispatch checks: reject before the provider request is spent.
	type scopedLedger struct {
		scope  string
		ledger *budget.Ledger
	}
	for _, sl := range []scopedLedger{
		{scope: "run", ledger: runLedger},
		{scope: "node:" + n.ID(), ledger: nodeLedger},
	} {
		if sl.ledger == nil {
			continue
		}
		if err := sl.ledger.Check(sl.scope, estimate, 0); err != nil {
			emit(n.budgetExceededEvent(env, err))
			return nil, nil, fmt.Errorf("node %s: %w", n.ID(), err)
		}
	}

	// Self-recording clients (fallback chains) account for spend per
	// target attempt themselves; the node would double-count.
	selfRecording := false
	if recorder, ok := n.client.(budget.SelfRecorder); ok {
		selfRecording = recorder.RecordsOwnSpend()
	}

	check := func() error {
		for _, sl := range []scopedLedger{
			{scope: "run", ledger: runLedger},
			{scope: "node:" + n.ID(), ledger: nodeLedger},
		} {
			if sl.ledger == nil {
				continue
			}
			if err := sl.ledger.Check(sl.scope, estimate, 0); err != nil {
				emit(n.budgetExceededEvent(env, err))
				return fmt.Errorf("node %s: %w", n.ID(), err)
			}
		}
		return nil
	}

	record := func(usage core.LLMTokenUsage) {
		cost := usage.CostUSD
		if nodeLedger != nil {
			_ = nodeLedger.Record(usage.TotalTokens, cost)
		}
		if runLedger != nil && !selfRecording {
			_ = runLedger.Record(usage.TotalTokens, cost)
		}
	}
	return check, record, nil
}

// budgetExceededEvent renders a budget rejection observably: the gate that
// tripped, the ceiling, and the spent/projected values.
func (n *LLMNode) budgetExceededEvent(env *core.Envelope, err error) runtime.Event {
	event := runtime.NewEvent(runtime.EventBudgetExceeded, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("error", err.Error())

	var exceeded *budget.BudgetExceededError
	if errors.As(err, &exceeded) {
		event = event.
			WithPayload("scope", exceeded.Scope).
			WithPayload("gate", exceeded.Gate).
			WithPayload("ceiling", exceeded.Ceiling).
			WithPayload("projected", exceeded.Projected).
			WithPayload("spent_cost_usd", exceeded.Spent.CostUSD).
			WithPayload("spent_tokens", exceeded.Spent.TotalTokens).
			WithPayload("estimated_tokens", exceeded.Estimate.TotalTokens).
			WithPayload("estimated_cost_usd", exceeded.Estimate.CostUSD)
	}
	return event
}

// checkBudget verifies the response is within budget limits.
func (n *LLMNode) checkBudget(usage core.LLMTokenUsage) error {
	b := n.config.Budget

	if b.MaxInputTokens > 0 && usage.InputTokens > b.MaxInputTokens {
		return fmt.Errorf("input tokens %d exceeds budget %d", usage.InputTokens, b.MaxInputTokens)
	}
	if b.MaxOutputTokens > 0 && usage.OutputTokens > b.MaxOutputTokens {
		return fmt.Errorf("output tokens %d exceeds budget %d", usage.OutputTokens, b.MaxOutputTokens)
	}
	if b.MaxTotalTokens > 0 && usage.TotalTokens > b.MaxTotalTokens {
		return fmt.Errorf("total tokens %d exceeds budget %d", usage.TotalTokens, b.MaxTotalTokens)
	}
	if b.MaxCostUSD > 0 && usage.CostUSD > b.MaxCostUSD {
		return fmt.Errorf("cost $%.4f exceeds budget $%.4f", usage.CostUSD, b.MaxCostUSD)
	}

	return nil
}

// finalizeOutput returns the value to store under the node's OutputKey. When a
// JSONSchema is configured, structured output is required: the value must be a
// valid JSON object, otherwise the node returns an error rather than silently
// storing raw text. preParsed is the provider's already-parsed JSON, if any.
func (n *LLMNode) finalizeOutput(text string, preParsed map[string]any) (any, error) {
	if n.config.JSONSchema == nil {
		return text, nil
	}
	if n.schemaErr != nil {
		return nil, fmt.Errorf("structured output for node %q: invalid JSONSchema: %w", n.ID(), n.schemaErr)
	}

	obj := preParsed
	if obj == nil {
		parsed, err := parseJSONObject(text)
		if err != nil {
			return nil, fmt.Errorf("structured output for node %q: model did not return a valid JSON object: %w", n.ID(), err)
		}
		obj = parsed
	}

	if n.schema != nil {
		instance, err := schemaInstance(text, obj)
		if err != nil {
			return nil, fmt.Errorf("structured output for node %q: %w", n.ID(), err)
		}
		if err := n.schema.Validate(instance); err != nil {
			return nil, fmt.Errorf("structured output for node %q: output does not conform to schema: %w", n.ID(), err)
		}
	}

	return obj, nil
}

// parseJSONObject parses s into a JSON object, rejecting empty or non-object output.
func parseJSONObject(s string) (map[string]any, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, fmt.Errorf("output was empty")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// compileJSONSchema compiles a JSON Schema (as a decoded map) for validation.
func compileJSONSchema(schema map[string]any) (*jsonschema.Schema, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const loc = "petalflow://llm-node/schema"
	if err := c.AddResource(loc, doc); err != nil {
		return nil, err
	}
	return c.Compile(loc)
}

// schemaInstance decodes the model output into the value form the validator
// expects (json.Number for numbers), preferring the raw text and falling back
// to re-marshaling the parsed object.
func schemaInstance(text string, obj map[string]any) (any, error) {
	src := strings.TrimSpace(text)
	if src == "" {
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		src = string(b)
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(src))
}

// Config returns the node's configuration.
func (n *LLMNode) Config() LLMNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var _ core.Node = (*LLMNode)(nil)

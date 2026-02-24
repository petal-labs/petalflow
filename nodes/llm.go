package nodes

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"
	"text/template"
	"time"

	"github.com/petal-labs/petalflow/core"
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

	return &LLMNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindLLM),
		config:   config,
		client:   client,
	}
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
func (n *LLMNode) runSync(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, prompt string) (*core.Envelope, error) {
	// Build the LLM request
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

	// Execute with retries
	var resp core.LLMResponse
	var lastErr error

	for attempt := 1; attempt <= n.config.RetryPolicy.MaxAttempts; attempt++ {
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

	// Check budget if configured
	if n.config.Budget != nil {
		if err := n.checkBudget(resp.Usage); err != nil {
			return nil, err
		}
	}

	// Emit node.output.final event
	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("text", resp.Text))

	// Store output in envelope
	if n.config.JSONSchema != nil && resp.JSON != nil {
		env.SetVar(n.config.OutputKey, resp.JSON)
	} else {
		env.SetVar(n.config.OutputKey, resp.Text)
	}

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

	// Start streaming
	ch, err := streamClient.CompleteStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("streaming LLM call failed: %w", err)
	}

	// Read chunks, accumulate text, emit delta events
	var accumulated strings.Builder
	var usage core.LLMTokenUsage

	for chunk := range ch {
		// Handle chunk errors
		if chunk.Error != nil {
			return nil, fmt.Errorf("streaming error: %w", chunk.Error)
		}

		if chunk.Done {
			// Capture usage from the final chunk
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
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

	text := accumulated.String()

	// Check budget if configured
	if n.config.Budget != nil {
		if err := n.checkBudget(usage); err != nil {
			return nil, err
		}
	}

	// Emit node.output.final event
	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("text", text))

	// Store output in envelope
	env.SetVar(n.config.OutputKey, text)

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
	data, funcs := llmTemplateContext(env)

	tmpl, err := template.New("prompt").Funcs(funcs).Parse(n.config.PromptTemplate)
	if err != nil {
		return "", fmt.Errorf("invalid prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

func llmTemplateContext(env *core.Envelope) (map[string]any, template.FuncMap) {
	varsView := map[string]any{}
	if env != nil && env.Vars != nil {
		varsView = maps.Clone(env.Vars)
	}
	tasksView := llmTemplateTasksView(varsView)
	inputView := llmTemplateInputView(env, varsView)

	data := maps.Clone(varsView)
	if data == nil {
		data = map[string]any{}
	}
	data["input"] = inputView
	data["vars"] = varsView
	data["tasks"] = tasksView
	if env != nil {
		data["trace"] = env.Trace
	}

	funcs := template.FuncMap{
		"input": func() any {
			return inputView
		},
		"vars": func() map[string]any {
			return varsView
		},
		"tasks": func() map[string]map[string]any {
			return tasksView
		},
	}

	return data, funcs
}

func llmTemplateInputView(env *core.Envelope, vars map[string]any) any {
	if env != nil {
		if inputMap, ok := env.Input.(map[string]any); ok {
			return inputMap
		}
		if env.Input != nil {
			return env.Input
		}
	}
	return vars
}

func llmTemplateTasksView(vars map[string]any) map[string]map[string]any {
	tasks := make(map[string]map[string]any)
	for key, value := range vars {
		if !strings.HasSuffix(key, "_output") {
			continue
		}

		base := strings.TrimSuffix(key, "_output")
		parts := strings.SplitN(base, "__", 2)
		if len(parts) != 2 {
			continue
		}
		taskID := strings.TrimSpace(parts[0])
		if taskID == "" {
			continue
		}

		entry, ok := tasks[taskID]
		if !ok {
			entry = map[string]any{}
			tasks[taskID] = entry
		}
		entry["output"] = value
	}
	return tasks
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

// Config returns the node's configuration.
func (n *LLMNode) Config() LLMNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var _ core.Node = (*LLMNode)(nil)

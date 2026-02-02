package petalflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"
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
	RetryPolicy RetryPolicy

	// Timeout is the maximum time to wait for the LLM response.
	Timeout time.Duration

	// Budget sets resource limits for the LLM call.
	Budget *Budget

	// RecordMessages appends the conversation to envelope.Messages.
	RecordMessages bool
}

// LLMNode executes an LLM call as a workflow step.
type LLMNode struct {
	BaseNode
	config LLMNodeConfig
	client LLMClient
}

// NewLLMNode creates a new LLM node with the given configuration.
func NewLLMNode(id string, client LLMClient, config LLMNodeConfig) *LLMNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}

	return &LLMNode{
		BaseNode: NewBaseNode(id, NodeKindLLM),
		config:   config,
		client:   client,
	}
}

// Run executes the LLM call and stores the result in the envelope.
func (n *LLMNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
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

	// Build the LLM request
	req := LLMRequest{
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
	var resp LLMResponse
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

	// Store output in envelope
	if n.config.JSONSchema != nil && resp.JSON != nil {
		env.SetVar(n.config.OutputKey, resp.JSON)
	} else {
		env.SetVar(n.config.OutputKey, resp.Text)
	}

	// Record token usage
	env.SetVar(n.config.OutputKey+"_usage", TokenUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.TotalTokens,
		CostUSD:      resp.Usage.CostUSD,
	})

	// Record messages if configured
	if n.config.RecordMessages {
		env.AppendMessage(Message{
			Role:    "user",
			Content: prompt,
			Name:    n.id,
		})
		env.AppendMessage(Message{
			Role:    "assistant",
			Content: resp.Text,
			Name:    n.id,
			Meta: map[string]any{
				"model":    resp.Model,
				"provider": resp.Provider,
			},
		})
	}

	return env, nil
}

// buildPrompt constructs the prompt from envelope variables.
func (n *LLMNode) buildPrompt(env *Envelope) (string, error) {
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
func (n *LLMNode) executeTemplate(env *Envelope) (string, error) {
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

// checkBudget verifies the response is within budget limits.
func (n *LLMNode) checkBudget(usage LLMTokenUsage) error {
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

// toString converts a value to string for prompt building.
func toString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		// Try JSON for complex types
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", v)
	}
}

// Config returns the node's configuration.
func (n *LLMNode) Config() LLMNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var _ Node = (*LLMNode)(nil)

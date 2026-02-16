//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/core"
	"github.com/petal-labs/iris/providers/anthropic"
	"github.com/petal-labs/iris/providers/openai"
	iristools "github.com/petal-labs/iris/tools"
)

func TestIrisProvider_OpenAI_Chat(t *testing.T) {
	skipIfNoAPIKey(t)

	provider := openai.New(getAPIKey(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, &iriscore.ChatRequest{
		Model: openai.ModelGPT4oMini,
		Messages: []iriscore.Message{
			{Role: iriscore.RoleUser, Content: "Reply with one short greeting."},
		},
	})
	if err != nil {
		t.Fatalf("provider.Chat: %v", err)
	}

	if resp == nil {
		t.Fatal("provider.Chat returned nil response")
	}
	if resp.Output == "" {
		t.Fatal("expected non-empty output")
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero token usage")
	}

	t.Logf("OpenAI chat output: %s", resp.Output)
}

func TestIrisProvider_Anthropic_ChatOptional(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping optional Anthropic integration test")
	}

	provider := anthropic.New(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, &iriscore.ChatRequest{
		Model: anthropic.ModelClaudeHaiku45,
		Messages: []iriscore.Message{
			{Role: iriscore.RoleUser, Content: "Reply with one short greeting."},
		},
	})
	if err != nil {
		t.Fatalf("provider.Chat: %v", err)
	}

	if resp == nil {
		t.Fatal("provider.Chat returned nil response")
	}
	if resp.Output == "" {
		t.Fatal("expected non-empty output")
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero token usage")
	}

	t.Logf("Anthropic chat output: %s", resp.Output)
}

func TestIrisProvider_OpenAI_Stream(t *testing.T) {
	skipIfNoAPIKey(t)

	provider := openai.New(getAPIKey(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := provider.StreamChat(ctx, &iriscore.ChatRequest{
		Model: openai.ModelGPT4oMini,
		Messages: []iriscore.Message{
			{Role: iriscore.RoleUser, Content: "Reply with one short greeting."},
		},
	})
	if err != nil {
		t.Fatalf("provider.StreamChat: %v", err)
	}

	resp, err := iriscore.DrainStream(ctx, stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp == nil {
		t.Fatal("DrainStream returned nil response")
	}
	if resp.Output == "" {
		t.Fatal("expected non-empty streaming output")
	}

	t.Logf("OpenAI stream output: %s", resp.Output)
}

func TestIrisProvider_Anthropic_StreamOptional(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping optional Anthropic integration test")
	}

	provider := anthropic.New(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := provider.StreamChat(ctx, &iriscore.ChatRequest{
		Model: anthropic.ModelClaudeHaiku45,
		Messages: []iriscore.Message{
			{Role: iriscore.RoleUser, Content: "Reply with one short greeting."},
		},
	})
	if err != nil {
		t.Fatalf("provider.StreamChat: %v", err)
	}

	resp, err := iriscore.DrainStream(ctx, stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp == nil {
		t.Fatal("DrainStream returned nil response")
	}
	if resp.Output == "" {
		t.Fatal("expected non-empty streaming output")
	}

	t.Logf("Anthropic stream output: %s", resp.Output)
}

func TestIrisProvider_OpenAI_ToolCallingRoundTrip(t *testing.T) {
	skipIfNoAPIKey(t)

	provider := openai.New(getAPIKey(t))
	tool := openAITestWeatherTool{}

	var firstResp *iriscore.ChatResponse
	prompts := []string{
		`Call the get_weather tool exactly once with city "Boston". Do not answer in natural language before the tool call.`,
		`Use the get_weather tool for city "Boston" and return only the tool call.`,
		`Call get_weather with {"city":"Boston"} and nothing else.`,
	}

	for i, prompt := range prompts {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := provider.Chat(ctx, &iriscore.ChatRequest{
			Model: openai.ModelGPT4oMini,
			Messages: []iriscore.Message{
				{Role: iriscore.RoleUser, Content: prompt},
			},
			Tools: []iriscore.Tool{tool},
		})
		cancel()
		if err != nil {
			t.Fatalf("provider.Chat tool call attempt %d: %v", i+1, err)
		}
		firstResp = resp
		if firstResp != nil && firstResp.HasToolCalls() {
			break
		}
	}

	if firstResp == nil || !firstResp.HasToolCalls() {
		lastOutput := ""
		if firstResp != nil {
			lastOutput = firstResp.Output
		}
		t.Fatalf("expected a tool call after %d attempts; last output: %q", len(prompts), lastOutput)
	}

	call := firstResp.FirstToolCall()
	if call == nil {
		t.Fatal("expected first tool call, got nil")
	}
	if call.Name != tool.Name() {
		t.Fatalf("tool call name = %q, want %q", call.Name, tool.Name())
	}
	if len(call.Arguments) == 0 {
		t.Fatal("tool call arguments should not be empty")
	}

	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatalf("tool call arguments should be valid JSON: %v (raw: %s)", err, string(call.Arguments))
	}
	city, _ := args["city"].(string)
	city = strings.TrimSpace(city)
	if city == "" {
		t.Fatalf("tool call arguments missing city: %s", string(call.Arguments))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	finalResp, err := provider.Chat(ctx, &iriscore.ChatRequest{
		Model: openai.ModelGPT4oMini,
		Messages: []iriscore.Message{
			{Role: iriscore.RoleUser, Content: "What is the weather in Boston?"},
			{Role: iriscore.RoleAssistant, ToolCalls: []iriscore.ToolCall{*call}},
			{
				Role: iriscore.RoleTool,
				ToolResults: []iriscore.ToolResult{
					{
						CallID: call.ID,
						Content: map[string]any{
							"city":          city,
							"condition":     "sunny",
							"temperature_c": 22,
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("provider.Chat final response: %v", err)
	}
	if finalResp == nil {
		t.Fatal("provider.Chat final response is nil")
	}
	if strings.TrimSpace(finalResp.Output) == "" {
		t.Fatal("expected non-empty final output after tool result")
	}

	output := strings.ToLower(finalResp.Output)
	if !strings.Contains(output, "sun") && !strings.Contains(output, "22") && !strings.Contains(output, "boston") {
		t.Fatalf("final output does not appear to use tool result: %q", finalResp.Output)
	}

	t.Logf("OpenAI tool-calling final output: %s", finalResp.Output)
}

type openAITestWeatherTool struct{}

func (openAITestWeatherTool) Name() string {
	return "get_weather"
}

func (openAITestWeatherTool) Description() string {
	return "Get the current weather for a city."
}

func (openAITestWeatherTool) Schema() iristools.ToolSchema {
	return iristools.ToolSchema{
		JSONSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"city": {"type": "string", "description": "City name"}
			},
			"required": ["city"],
			"additionalProperties": false
		}`),
	}
}

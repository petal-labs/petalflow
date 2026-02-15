//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/core"
	"github.com/petal-labs/iris/providers/anthropic"
	"github.com/petal-labs/iris/providers/openai"
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

//go:build integration

// Package integration contains integration tests that call real external APIs.
// These tests are excluded from normal `go test ./...` runs and require:
//
//	go test -tags=integration ./tests/integration/... -v -count=1
package integration

import (
	"context"
	"os"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
)

// isCI returns true when running inside a CI environment.
func isCI() bool {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "CIRCLECI", "TRAVIS"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// skipOrFailOnMissingKey fatals in CI (secrets should always be present)
// and skips locally (developer may not have the key).
func skipOrFailOnMissingKey(t *testing.T, keyName string) {
	t.Helper()
	if isCI() {
		t.Fatalf("required secret %s is not set in CI", keyName)
	}
	t.Skipf("%s not set, skipping integration test", keyName)
}

// skipIfNoAPIKey skips or fatals if OPENAI_API_KEY is not available.
func skipIfNoAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") == "" {
		skipOrFailOnMissingKey(t, "OPENAI_API_KEY")
	}
}

// getAPIKey returns the OPENAI_API_KEY or fatals.
func getAPIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY is not set")
	}
	return key
}

// newOpenAIClient returns a core.LLMClient backed by the OpenAI provider.
// The returned client implements both LLMClient and StreamingLLMClient.
func newOpenAIClient(t *testing.T) core.LLMClient {
	t.Helper()
	client, err := llmprovider.NewClient("openai", hydrate.ProviderConfig{
		APIKey: getAPIKey(t),
	})
	if err != nil {
		t.Fatalf("creating OpenAI client: %v", err)
	}
	return client
}

// nonStreamingClient wraps an LLMClient to hide the StreamingLLMClient
// interface, forcing LLMNode to use the synchronous Complete path.
type nonStreamingClient struct {
	inner core.LLMClient
}

func (c *nonStreamingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return c.inner.Complete(ctx, req)
}

// newNonStreamingClient returns an LLMClient that does NOT implement
// StreamingLLMClient, ensuring LLMNode uses the sync path.
func newNonStreamingClient(t *testing.T) core.LLMClient {
	t.Helper()
	return &nonStreamingClient{inner: newOpenAIClient(t)}
}

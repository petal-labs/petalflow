//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	iriscore "github.com/petal-labs/iris/core"
	"github.com/petal-labs/iris/providers/openai"
)

func TestEmbedding_SingleInput(t *testing.T) {
	skipIfNoAPIKey(t)

	provider := openai.New(getAPIKey(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.CreateEmbeddings(ctx, &iriscore.EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: []iriscore.EmbeddingInput{
			{Text: "Hello, world!"},
		},
	})
	if err != nil {
		t.Fatalf("CreateEmbeddings: %v", err)
	}

	if len(resp.Vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(resp.Vectors))
	}

	vec := resp.Vectors[0]
	if len(vec.Vector) != 1536 {
		t.Fatalf("expected 1536 dimensions, got %d", len(vec.Vector))
	}

	if resp.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero total tokens")
	}

	t.Logf("Embedding: %d dimensions, usage: prompt=%d total=%d",
		len(vec.Vector), resp.Usage.PromptTokens, resp.Usage.TotalTokens)
}

func TestEmbedding_BatchInput(t *testing.T) {
	skipIfNoAPIKey(t)

	provider := openai.New(getAPIKey(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inputs := []iriscore.EmbeddingInput{
		{Text: "The quick brown fox jumps over the lazy dog"},
		{Text: "Machine learning is a subset of artificial intelligence"},
		{Text: "Go is a statically typed programming language"},
	}

	resp, err := provider.CreateEmbeddings(ctx, &iriscore.EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: inputs,
	})
	if err != nil {
		t.Fatalf("CreateEmbeddings: %v", err)
	}

	if len(resp.Vectors) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(resp.Vectors))
	}

	for i, vec := range resp.Vectors {
		if len(vec.Vector) != 1536 {
			t.Errorf("vector[%d]: expected 1536 dimensions, got %d", i, len(vec.Vector))
		}
	}

	if resp.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero total tokens")
	}

	t.Logf("Batch embedding: %d vectors, usage: prompt=%d total=%d",
		len(resp.Vectors), resp.Usage.PromptTokens, resp.Usage.TotalTokens)
}

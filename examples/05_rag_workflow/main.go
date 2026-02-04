// Example: RAG Workflow
//
// This example demonstrates a Retrieval-Augmented Generation (RAG) pattern.
// It shows:
// - ToolNode for document retrieval
// - TransformNode for context preparation
// - LLMNode for answer generation
// - The full RAG pipeline: Query -> Retrieve -> Generate
//
// Uses a mock retriever (no vector database required).
// For real use, replace the mock tool with your retrieval system.
//
// Prerequisites for LLM part:
// - Ollama running locally: ollama pull llama3.2
//
// Run: go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/petalflow"
	"github.com/petal-labs/petalflow/irisadapter"
)

// MockRetriever simulates document retrieval.
// In production, this would query a vector database like Qdrant or Pinecone.
type MockRetriever struct {
	documents []Document
}

type Document struct {
	ID      string
	Content string
	Score   float64
}

func NewMockRetriever() *MockRetriever {
	return &MockRetriever{
		documents: []Document{
			{ID: "doc1", Content: "PetalFlow is a Go workflow runtime for AI agents. It uses directed graphs to chain LLM calls, tools, and data transformations.", Score: 0.95},
			{ID: "doc2", Content: "The Envelope is PetalFlow's core data structure. It carries variables, messages, and artifacts between nodes in a workflow.", Score: 0.88},
			{ID: "doc3", Content: "LLMNode executes LLM calls with configurable prompts, retry policies, and timeouts. Use irisadapter to connect Iris providers.", Score: 0.82},
			{ID: "doc4", Content: "RuleRouter and LLMRouter enable conditional branching in workflows. Rules can match envelope variables or use LLM classification.", Score: 0.75},
			{ID: "doc5", Content: "FilterNode prunes collections using operations like top-N, threshold, and deduplication. Supports dot notation for nested fields.", Score: 0.70},
		},
	}
}

func (r *MockRetriever) Name() string { return "retriever" }

func (r *MockRetriever) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, _ := args["query"].(string)
	topK := 3
	if k, ok := args["top_k"].(int); ok {
		topK = k
	}

	// Simple keyword matching (a real system would use embeddings)
	var results []Document
	queryLower := strings.ToLower(query)
	for _, doc := range r.documents {
		if strings.Contains(strings.ToLower(doc.Content), queryLower) ||
			strings.Contains(queryLower, "petalflow") {
			results = append(results, doc)
		}
	}

	// Limit to topK
	if len(results) > topK {
		results = results[:topK]
	}

	// Convert to serializable format
	docs := make([]map[string]any, len(results))
	for i, doc := range results {
		docs[i] = map[string]any{
			"id":      doc.ID,
			"content": doc.Content,
			"score":   doc.Score,
		}
	}

	return map[string]any{
		"documents": docs,
		"count":     len(docs),
	}, nil
}

func main() {
	// Create LLM client
	provider := ollama.New(ollama.WithBaseURL("http://localhost:11434"))
	client := irisadapter.NewProviderAdapter(provider)

	// Create graph
	g := petalflow.NewGraph("rag-workflow")

	// Step 1: Retrieval Tool
	// Uses ToolNode to fetch relevant documents.
	retriever := NewMockRetriever()
	retrieveNode := petalflow.NewToolNode("retrieve", retriever, petalflow.ToolNodeConfig{
		ArgsTemplate: map[string]string{
			"query": "query",
		},
		StaticArgs: map[string]any{
			"top_k": 3,
		},
		OutputKey: "retrieval_result",
		Timeout:   10 * time.Second,
	})

	// Step 2: Context Preparation
	// Transforms retrieved documents into a prompt-ready format.
	prepareContext := petalflow.NewTransformNode("prepare", petalflow.TransformNodeConfig{
		Transform: petalflow.TransformTemplate,
		Template: `Context from documentation:
{{range $i, $doc := (index . "retrieval_result").documents}}
[Document {{$i | printf "%d"}}] {{index $doc "content"}}
{{end}}

Question: {{.query}}`,
		OutputVar: "context_prompt",
	})

	// Step 3: LLM Generation
	// Generates an answer using the retrieved context.
	generateNode := petalflow.NewLLMNode("generate", client, petalflow.LLMNodeConfig{
		Model: "llama3.2",
		System: `You are a helpful assistant answering questions about PetalFlow.
Use ONLY the provided context to answer. If the context doesn't contain the answer, say so.
Be concise and direct.`,
		PromptTemplate: "{{.context_prompt}}",
		OutputKey:      "answer",
		Timeout:        30 * time.Second,
		RecordMessages: true,
	})

	// Add nodes
	g.AddNode(retrieveNode)
	g.AddNode(prepareContext)
	g.AddNode(generateNode)

	// Connect: retrieve -> prepare -> generate
	g.AddEdge("retrieve", "prepare")
	g.AddEdge("prepare", "generate")

	// Set entry
	g.SetEntry("retrieve")

	// Run with a sample query
	query := "What is the Envelope in PetalFlow?"
	fmt.Printf("Question: %s\n\n", query)

	env := petalflow.NewEnvelope().WithVar("query", query)

	runtime := petalflow.NewRuntime()
	result, err := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
	if err != nil {
		fmt.Printf("Workflow failed: %v\n", err)
		os.Exit(1)
	}

	// Show retrieved documents
	if retrieval, ok := result.GetVar("retrieval_result"); ok {
		if r, ok := retrieval.(map[string]any); ok {
			fmt.Printf("Retrieved %v documents:\n", r["count"])
			if docs, ok := r["documents"].([]map[string]any); ok {
				for i, doc := range docs {
					fmt.Printf("  %d. [%.2f] %s...\n", i+1, doc["score"], truncate(doc["content"].(string), 60))
				}
			}
		}
	}

	// Show the answer
	fmt.Printf("\nAnswer:\n%s\n", result.GetVarString("answer"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

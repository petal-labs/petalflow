package nodes

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/petal-labs/petalflow/budget"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// countingClient fails count times then succeeds; records dispatched calls.
type countingClient struct {
	failuresRemaining int
	calls             int32
}

func (c *countingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	if atomic.AddInt32(&c.calls, 1) <= int32(c.failuresRemaining) {
		return core.LLMResponse{}, errors.New("transient provider error")
	}
	return core.LLMResponse{
		Text:  "ok",
		Usage: core.LLMTokenUsage{InputTokens: 400, OutputTokens: 100, TotalTokens: 500},
	}, nil
}

func budgetNode(client core.LLMClient, maxTotalTokens int) *LLMNode {
	return NewLLMNode("answer", client, LLMNodeConfig{
		Model:          "test-model",
		PromptTemplate: "{{.input}}",
		OutputKey:      "answer_output",
		RetryPolicy:    core.RetryPolicy{MaxAttempts: 5},
		Budget:         &core.Budget{MaxTotalTokens: maxTotalTokens},
	})
}

func TestBudgetNodeLedgerCannotBeBypassedByRetries(t *testing.T) {
	// A parallel branch drains the run ledger while this node is retrying:
	// the per-attempt budget check must stop the retry loop instead of
	// dispatching past the ceiling.
	ledger, _ := budget.NewLedger(budget.Ceiling{MaxTotalTokens: 1000})
	ctx := budget.ContextWithLedger(context.Background(), ledger)

	primary := &countingClient{failuresRemaining: 3}
	draining := &drainingClient{ledger: ledger, drain: 1000}
	node := NewLLMNode("answer", draining, LLMNodeConfig{
		Model:          "test-model",
		PromptTemplate: "{{.input}}",
		OutputKey:      "answer_output",
		RetryPolicy:    core.RetryPolicy{MaxAttempts: 5},
		Budget:         &core.Budget{MaxTotalTokens: 1000},
	})
	_ = primary

	_, err := node.Run(ctx, core.NewEnvelope().WithInput("q"))
	if err == nil {
		t.Fatal("expected budget-exceeded failure mid-retry")
	}
	if !errors.Is(err, budget.ErrBudgetExceeded) {
		t.Errorf("error should wrap budget.ErrBudgetExceeded: %v", err)
	}
	if atomic.LoadInt32(&draining.calls) != 1 {
		t.Errorf("dispatches = %d, want 1 (retry stopped by budget check)", draining.calls)
	}
}

// drainingClient fails every call and drains the run ledger on the first
// attempt, simulating a parallel branch consuming budget mid-retry.
type drainingClient struct {
	ledger *budget.Ledger
	drain  int
	calls  int32
}

func (c *drainingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	if atomic.AddInt32(&c.calls, 1) == 1 {
		_ = c.ledger.Record(c.drain, 0)
	}
	return core.LLMResponse{}, errors.New("transient provider error")
}

func TestBudgetRunLedgerSharedAcrossNodes(t *testing.T) {
	// Two LLM nodes in sequence sharing one run ledger of 600 tokens: the
	// first node records 500 actual tokens, and the second node's prompt
	// (~130 estimated tokens) then predicts a breach pre-dispatch.
	client := &countingClient{}

	ledger, _ := budget.NewLedger(budget.Ceiling{MaxTotalTokens: 600})
	runLedgerCtx := budget.ContextWithLedger(context.Background(), ledger)

	longTemplate := "Summarize the following in detail: " + strings.Repeat("x", 520) + " {{.input}}"

	first := NewLLMNode("first", client, LLMNodeConfig{
		Model: "test-model", PromptTemplate: "{{.input}}", OutputKey: "out1",
	})
	second := NewLLMNode("second", client, LLMNodeConfig{
		Model: "test-model", PromptTemplate: longTemplate, OutputKey: "out2",
	})

	g, err := graph.NewGraphBuilder("budget-run").AddNode(first).Edge(second).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	opts := runtime.DefaultRunOptions()
	opts.BudgetLedger = ledger
	_, runErr := runtime.NewRuntime().Run(runLedgerCtx, g, core.NewEnvelope().WithInput("q"), opts)
	if runErr == nil {
		t.Fatal("expected the run to fail when the run budget is exhausted")
	}
	if !strings.Contains(runErr.Error(), "budget exceeded") {
		t.Errorf("error should name the budget: %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "second") {
		t.Errorf("error should name the breaching node: %v", runErr)
	}

	// The ledger recorded the first node's actual spend exactly once.
	if spent := ledger.Spent(); spent.TotalTokens != 500 {
		t.Errorf("ledger spent = %d, want 500", spent.TotalTokens)
	}
}

func TestBudgetExceededEventEmitted(t *testing.T) {
	client := &countingClient{failuresRemaining: 0}
	node := budgetNode(client, 10) // tiny ceiling: any call breaches

	var events []runtime.Event
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) })

	_, err := node.Run(ctx, core.NewEnvelope().WithInput("a long prompt that estimates past the ceiling"))
	if err == nil {
		t.Fatal("expected budget rejection")
	}

	var sawExceeded bool
	for _, e := range events {
		if e.Kind == runtime.EventBudgetExceeded {
			sawExceeded = true
			if e.Payload["gate"] != "total_tokens" || e.Payload["scope"] != "node:answer" {
				t.Errorf("event payload = %+v", e.Payload)
			}
			if _, has := e.Payload["ceiling"]; !has {
				t.Error("event should carry the ceiling")
			}
		}
	}
	if !sawExceeded {
		t.Error("budget.exceeded event missing")
	}
	if atomic.LoadInt32(&client.calls) != 0 {
		t.Error("rejection must happen before dispatch")
	}
}

func TestBudgetRunLedgerNodeEventAttribution(t *testing.T) {
	// Run-scoped ledger: the event attributes to the node and scope "run".
	ledger, _ := budget.NewLedger(budget.Ceiling{MaxTotalTokens: 5})
	ctx := budget.ContextWithLedger(
		runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) {}),
		ledger,
	)

	client := &countingClient{}
	node := NewLLMNode("answer", client, LLMNodeConfig{
		Model: "test-model", PromptTemplate: "{{.input}}", OutputKey: "out",
	})

	_, err := node.Run(ctx, core.NewEnvelope().WithInput("prompt longer than five tokens"))
	if err == nil {
		t.Fatal("expected run-budget rejection")
	}
	if !errors.Is(err, budget.ErrBudgetExceeded) {
		t.Errorf("error should wrap sentinel: %v", err)
	}
	if atomic.LoadInt32(&client.calls) != 0 {
		t.Error("no dispatch expected")
	}
}

func TestBudgetMalformedNodeBudgetRejected(t *testing.T) {
	node := NewLLMNode("answer", &countingClient{}, LLMNodeConfig{
		Model:  "test-model",
		Budget: &core.Budget{MaxTotalTokens: -5},
	})
	if _, err := node.Run(context.Background(), core.NewEnvelope().WithInput("x")); err == nil {
		t.Fatal("negative ceiling should fail")
	}
}

func TestBudgetUnknownCostModelStillEnforcesTokens(t *testing.T) {
	// A client that reports no cost (cost-unknown model): the token gate
	// enforces; the cost gate stays silent.
	client := &countingClient{}
	ledger, _ := budget.NewLedger(budget.Ceiling{MaxCostUSD: 100, MaxTotalTokens: 200})
	ctx := budget.ContextWithLedger(context.Background(), ledger)

	node := NewLLMNode("answer", client, LLMNodeConfig{
		Model: "test-model", PromptTemplate: "{{.input}}", OutputKey: "out",
	})

	env := core.NewEnvelope().WithInput("q")
	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("first call should pass: %v", err)
	}
	// 500 tokens recorded; second call estimates over the remaining 200... rejected.
	_, err := node.Run(ctx, core.NewEnvelope().WithInput("q"))
	if err == nil || !errors.Is(err, budget.ErrBudgetExceeded) {
		t.Fatalf("second call should breach the token ceiling: %v", err)
	}
}

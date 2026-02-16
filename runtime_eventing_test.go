package petalflow_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// mockLLMClient is a deterministic LLM client for runtime eventing tests.
type mockLLMClient struct {
	response string
}

func (m *mockLLMClient) Complete(_ context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{
		Text:     m.response,
		Model:    req.Model,
		Provider: "mock",
		Usage: core.LLMTokenUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}, nil
}

func newSQLiteEventStore(t *testing.T) bus.EventStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "events.sqlite")
	store, err := bus.NewSQLiteEventStore(bus.SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteEventStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRuntimeEventingPipeline(t *testing.T) {
	// ---------------------------------------------------------------
	// 1. Build a graph: LLM node -> Tool node
	// ---------------------------------------------------------------
	llmClient := &mockLLMClient{response: "lookup:42"}

	llmNode := nodes.NewLLMNode("llm-summarise", llmClient, nodes.LLMNodeConfig{
		Model:     "mock-model",
		System:    "You are a test assistant.",
		InputVars: []string{"query"},
		OutputKey: "llm_result",
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 1,
			Backoff:     0,
		},
		Timeout: 5 * time.Second,
	})

	lookupTool := core.NewFuncTool("lookup", "looks up a value", func(_ context.Context, args map[string]any) (map[string]any, error) {
		return map[string]any{
			"found": true,
			"input": args,
		}, nil
	})

	toolNode := nodes.NewToolNode("tool-lookup", lookupTool, nodes.ToolNodeConfig{
		ToolName:  "lookup",
		OutputKey: "tool_result",
		StaticArgs: map[string]any{
			"key": "integration-test",
		},
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 1,
			Backoff:     0,
		},
		Timeout: 5 * time.Second,
	})

	g, err := graph.NewGraphBuilder("integration-eventing").
		AddNode(llmNode).
		Edge(toolNode).
		Build()
	if err != nil {
		t.Fatalf("graph build: %v", err)
	}

	// ---------------------------------------------------------------
	// 2. Set up MemBus + SQLiteEventStore + StoreSubscriber
	// ---------------------------------------------------------------
	memBus := bus.NewMemBus(bus.MemBusConfig{})
	defer memBus.Close()

	store := newSQLiteEventStore(t)
	storeSub := bus.NewStoreSubscriber(store, nil)

	// Subscribe globally so StoreSubscriber receives every event.
	globalSub := memBus.SubscribeAll()
	defer globalSub.Close()

	// Also collect events via EventHandler for cross-check.
	var handlerMu sync.Mutex
	var handlerEvents []runtime.Event
	handler := func(e runtime.Event) {
		handlerMu.Lock()
		handlerEvents = append(handlerEvents, e)
		handlerMu.Unlock()
	}

	// ---------------------------------------------------------------
	// 3. Run graph via runtime with EventBus in RunOptions
	// ---------------------------------------------------------------
	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.EventBus = memBus
	opts.EventHandler = handler

	env := core.NewEnvelope().WithVar("query", "what is 42?")

	result, err := rt.Run(context.Background(), g, env, opts)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Verify the graph actually executed end to end.
	if _, ok := result.GetVar("llm_result"); !ok {
		t.Error("expected llm_result var in envelope")
	}
	if _, ok := result.GetVar("tool_result"); !ok {
		t.Error("expected tool_result var in envelope")
	}

	// ---------------------------------------------------------------
	// 4. Drain bus subscription and feed into StoreSubscriber
	// ---------------------------------------------------------------
	var busEvents []runtime.Event
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case evt, ok := <-globalSub.Events():
				if !ok {
					return
				}
				busEvents = append(busEvents, evt)
				storeSub.Handle(evt)
			case <-time.After(200 * time.Millisecond):
				return
			}
		}
	}()
	<-drainDone

	// ---------------------------------------------------------------
	// 5. Assertions
	// ---------------------------------------------------------------
	runID := result.Trace.RunID
	if runID == "" {
		t.Fatal("expected non-empty RunID in envelope trace")
	}

	// 5a. All events must have monotonically increasing Seq.
	t.Run("monotonic_seq", func(t *testing.T) {
		if len(busEvents) == 0 {
			t.Fatal("no events received via bus")
		}
		var prevSeq uint64
		for i, e := range busEvents {
			if e.Seq <= prevSeq {
				t.Errorf("event[%d] Seq=%d not > prevSeq=%d (kind=%s)", i, e.Seq, prevSeq, e.Kind)
			}
			prevSeq = e.Seq
		}
	})

	// 5b. Bus subscriber received all events.
	t.Run("bus_subscriber_receives_all", func(t *testing.T) {
		// Minimum expected: run.started, node.started(llm), node.output.final(llm),
		//   node.finished(llm), node.started(tool), tool.call, tool.result,
		//   node.finished(tool), run.finished = 9
		if len(busEvents) < 9 {
			t.Errorf("expected >= 9 bus events, got %d", len(busEvents))
			for i, e := range busEvents {
				t.Logf("  [%d] kind=%s node=%s seq=%d", i, e.Kind, e.NodeID, e.Seq)
			}
		}
	})

	// 5c. Store has all events (via StoreSubscriber).
	t.Run("store_has_all_events", func(t *testing.T) {
		ctx := context.Background()
		stored, err := store.List(ctx, runID, 0, 0)
		if err != nil {
			t.Fatalf("store.List: %v", err)
		}
		if len(stored) != len(busEvents) {
			t.Errorf("store has %d events, bus had %d", len(stored), len(busEvents))
		}

		latestSeq, err := store.LatestSeq(ctx, runID)
		if err != nil {
			t.Fatalf("store.LatestSeq: %v", err)
		}
		if latestSeq == 0 {
			t.Error("expected latestSeq > 0")
		}
	})

	// 5d. Replay with afterSeq works.
	t.Run("replay_after_seq", func(t *testing.T) {
		ctx := context.Background()
		allEvents, err := store.List(ctx, runID, 0, 0)
		if err != nil {
			t.Fatalf("List all: %v", err)
		}
		if len(allEvents) < 3 {
			t.Fatalf("need at least 3 events for afterSeq test, got %d", len(allEvents))
		}

		// Pick a midpoint sequence number and replay from there.
		midIdx := len(allEvents) / 2
		afterSeq := allEvents[midIdx].Seq

		replayed, err := store.List(ctx, runID, afterSeq, 0)
		if err != nil {
			t.Fatalf("List afterSeq=%d: %v", afterSeq, err)
		}

		expectedCount := len(allEvents) - midIdx - 1
		if len(replayed) != expectedCount {
			t.Errorf("replayed %d events after seq %d, want %d", len(replayed), afterSeq, expectedCount)
		}

		// All replayed events must have Seq > afterSeq.
		for _, e := range replayed {
			if e.Seq <= afterSeq {
				t.Errorf("replayed event Seq=%d should be > afterSeq=%d", e.Seq, afterSeq)
			}
		}
	})

	// 5e. Tool events are present.
	t.Run("tool_events_present", func(t *testing.T) {
		var hasToolCall, hasToolResult bool
		for _, e := range busEvents {
			switch e.Kind {
			case runtime.EventToolCall:
				hasToolCall = true
			case runtime.EventToolResult:
				hasToolResult = true
			}
		}
		if !hasToolCall {
			t.Error("missing tool.call event")
		}
		if !hasToolResult {
			t.Error("missing tool.result event")
		}
	})

	// 5f. Event kinds are dot-delimited.
	t.Run("event_kinds_dot_delimited", func(t *testing.T) {
		for _, e := range busEvents {
			kind := string(e.Kind)
			if !strings.Contains(kind, ".") {
				t.Errorf("event kind %q is not dot-delimited", kind)
			}
		}
	})

	// 5g. Handler events match bus events.
	t.Run("handler_matches_bus", func(t *testing.T) {
		handlerMu.Lock()
		hCount := len(handlerEvents)
		handlerMu.Unlock()

		if hCount != len(busEvents) {
			t.Errorf("handler got %d events, bus got %d", hCount, len(busEvents))
		}
	})

	// 5h. Verify event ordering (run.started first, run.finished last).
	t.Run("event_ordering", func(t *testing.T) {
		if len(busEvents) == 0 {
			t.Fatal("no events")
		}
		first := busEvents[0]
		last := busEvents[len(busEvents)-1]
		if first.Kind != runtime.EventRunStarted {
			t.Errorf("first event kind = %s, want run.started", first.Kind)
		}
		if last.Kind != runtime.EventRunFinished {
			t.Errorf("last event kind = %s, want run.finished", last.Kind)
		}
	})
}

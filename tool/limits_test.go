package tool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvocationLimitsValidation(t *testing.T) {
	if err := (InvocationLimits{MaxConcurrentPerTool: -1}).Validate(); err == nil {
		t.Fatal("negative concurrency must be invalid")
	}
	if err := (InvocationLimits{ToolRatePerMinute: map[string]int{"bad": -3}}).Validate(); err == nil {
		t.Fatal("negative tool rate must be invalid")
	}
	if err := (InvocationLimits{MaxConcurrentPerTool: 2, MaxPerMinutePerTool: 10}).Validate(); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}
}

func TestGovernorConcurrencyPerTool(t *testing.T) {
	governor := newInvocationGovernor(&InvocationLimits{MaxConcurrentPerTool: 2})
	ctx := context.Background()

	release1, err := governor.acquire("tool_a", ctx)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	release2, err := governor.acquire("tool_a", ctx)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	_, err = governor.acquire("tool_a", ctx)
	if !errors.Is(err, ErrToolConcurrencyLimit) {
		t.Fatalf("third acquire = %v, want ErrToolConcurrencyLimit", err)
	}
	if got := LimitErrorCode(err); got != ToolErrorCodeConcurrencyLimit {
		t.Fatalf("code = %q, want %q", got, ToolErrorCodeConcurrencyLimit)
	}

	// Other tools are unaffected.
	release3, err := governor.acquire("tool_b", ctx)
	if err != nil {
		t.Fatalf("other tool acquire: %v", err)
	}

	release1()
	release1() // double release must be harmless
	hold, err := governor.acquire("tool_a", ctx)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = hold

	release2()
	release3()
}

func TestGovernorPerScopeSeparation(t *testing.T) {
	governor := newInvocationGovernor(&InvocationLimits{MaxConcurrentPerScope: 1})

	run1 := ContextWithInvocationScope(context.Background(), "run-a")
	run2 := ContextWithInvocationScope(context.Background(), "run-b")

	releaseRunA, err := governor.acquire("tool_a", run1)
	if err != nil {
		t.Fatalf("scope-a acquire: %v", err)
	}
	// Another scope is not blocked by run-a's hold.
	releaseRunB, err := governor.acquire("tool_a", run2)
	if err != nil {
		t.Fatalf("scope-b acquire: %v", err)
	}
	// But the same scope saturates even across tools.
	if _, err := governor.acquire("tool_b", run1); !errors.Is(err, ErrToolConcurrencyLimit) {
		t.Fatalf("same-scope acquire = %v, want ErrToolConcurrencyLimit", err)
	}

	// The default (daemon) scope applies when no scope is attached.
	defaultCtx := context.Background()
	releaseDefault, err := governor.acquire("tool_a", defaultCtx)
	if err != nil {
		t.Fatalf("default scope acquire: %v", err)
	}

	releaseRunA()
	releaseRunB()
	releaseDefault()
}

func TestGovernorRateLimitCountsRetries(t *testing.T) {
	start := time.Now()
	governor := newInvocationGovernor(&InvocationLimits{MaxPerMinutePerTool: 2})
	ctx := context.Background()

	isWindow := func(now time.Time) bool { return now.Sub(start) < time.Minute }
	_ = isWindow

	if release, err := governor.acquire("tool_a", ctx); err != nil {
		t.Fatalf("first attempt: %v", err)
	} else if release != nil {
		release()
	}
	if release, err := governor.acquire("tool_a", ctx); err != nil {
		t.Fatalf("retry attempt counts and is allowed while under the cap: %v", err)
	} else if release != nil {
		release()
	}
	_, err := governor.acquire("tool_a", ctx)
	if !errors.Is(err, ErrToolRateLimited) {
		t.Fatalf("third attempt inside window = %v, want ErrToolRateLimited", err)
	}
	if got := LimitErrorCode(err); got != ToolErrorCodeRateLimited {
		t.Fatalf("code = %q, want %q", got, ToolErrorCodeRateLimited)
	}
}

func TestGovernorConcurrencyPerToolOverrides(t *testing.T) {
	governor := newInvocationGovernor(&InvocationLimits{
		MaxConcurrentPerTool: 1,
		ToolConcurrency:      map[string]int{"generous": 3},
	})
	ctx := context.Background()

	r1, err := governor.acquire("generous", ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	r2, err := governor.acquire("generous", ctx)
	if err != nil {
		t.Fatalf("override should allow more concurrency: %v", err)
	}
	if _, err := governor.acquire("strict", ctx); err != nil {
		t.Fatalf("strict tool first: %v", err)
	}
	if _, err := governor.acquire("strict", ctx); !errors.Is(err, ErrToolConcurrencyLimit) {
		t.Fatalf("strict tool second = %v, want limit", err)
	}
	third, err := governor.acquire("generous", ctx)
	if err != nil {
		t.Fatalf("third within override: %v", err)
	}
	r1()
	r2()
	third()
}

func TestDaemonServiceInvocationLimits(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("gated_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{}}
	if err := store.Upsert(context.Background(), ToolRegistration{
		Name: "gated_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}

	block := make(chan struct{})
	var entered int32
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return blockingAdapter{block: block, entered: &entered}, nil
			},
		},
		InvocationLimits: &InvocationLimits{
			MaxConcurrentPerTool:  1,
			MaxConcurrentPerScope: 2,
		},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := ContextWithInvocationScope(context.Background(), "run-1")
			_, err := service.TestAction(ctx, "gated_tool", "execute", map[string]any{})
			results <- err
		}()
	}
	// One is in flight (blocked adapter), one must be rejected by the
	// concurrency budget while the first is still held.
	selectResults := func() []error {
		// Wait until one invocation is actually blocked in the adapter,
		// so the concurrency budget is guaranteed to be saturated.
		deadline := time.Now().Add(2 * time.Second)
		for atomic.LoadInt32(&entered) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("no invocation reached the adapter")
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(block)
		wg.Wait()
		close(results)
		var errs []error
		for e := range results {
			errs = append(errs, e)
		}
		return errs
	}

	errs := selectResults()
	blockedCount, limitCount := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			blockedCount++
		case errors.Is(e, ErrToolConcurrencyLimit):
			limitCount++
		default:
			t.Fatalf("unexpected invocation error: %v", e)
		}
	}
	if blockedCount < 1 || limitCount < 1 {
		t.Fatalf("expected at least one success and one concurrency rejection, got %d/%d", blockedCount, limitCount)
	}
}

// blockingAdapter blocks invocations until the test releases them.
type blockingAdapter struct {
	block   chan struct{}
	entered *int32
}

func (a blockingAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a.entered != nil {
		atomic.AddInt32(a.entered, 1)
	}
	<-a.block
	return InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}, nil
}

func (blockingAdapter) Close(ctx context.Context) error { return nil }

func TestDaemonServiceRateLimitWiring(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("fluent_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{}}
	if err := store.Upsert(context.Background(), ToolRegistration{
		Name: "fluent_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
			},
		},
		InvocationLimits: &InvocationLimits{MaxPerMinutePerTool: 1},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if _, err := service.TestAction(context.Background(), "fluent_tool", "execute", map[string]any{}); err != nil {
		t.Fatalf("first invocation: %v", err)
	}
	_, err = service.TestAction(context.Background(), "fluent_tool", "execute", map[string]any{})
	if !errors.Is(err, ErrToolRateLimited) {
		t.Fatalf("second invocation = %v, want ErrToolRateLimited", err)
	}
}

func TestGovernorNilIsUnlimited(t *testing.T) {
	var governor *invocationGovernor
	release, err := governor.acquire("any", context.Background())
	if err != nil {
		t.Fatalf("nil governor must be unlimited: %v", err)
	}
	release()
}

func TestInvocationScopeFromContext(t *testing.T) {
	if got := InvocationScopeFromContext(context.Background()); got != "scope:default" {
		t.Fatalf("default scope = %q, want scope:default", got)
	}
	ctx := ContextWithInvocationScope(context.Background(), "run-42")
	if got := InvocationScopeFromContext(ctx); got != "run:run-42" {
		t.Fatalf("scope = %q, want run:run-42", got)
	}
}

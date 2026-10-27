package routing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// scriptedClient fails N times then succeeds; it records every request and
// the model it was dispatched with.
type scriptedClient struct {
	mu                sync.Mutex
	failuresRemaining int
	errMessage        string
	calls             int
	dispatchedModels  []string
}

func (c *scriptedClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	c.mu.Lock()
	c.calls++
	c.dispatchedModels = append(c.dispatchedModels, req.Model)
	fail := c.failuresRemaining > 0
	if fail {
		c.failuresRemaining--
	}
	c.mu.Unlock()

	if fail {
		return core.LLMResponse{}, errors.New(c.errMessage)
	}
	return core.LLMResponse{Text: "answer from " + req.Model, Model: req.Model}, nil
}

func (c *scriptedClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// streamingScriptedClient wraps scriptedClient with streaming support.
type streamingScriptedClient struct {
	*scriptedClient
}

func (c *streamingScriptedClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	c.mu.Lock()
	c.calls++
	c.dispatchedModels = append(c.dispatchedModels, req.Model)
	fail := c.failuresRemaining > 0
	if fail {
		c.failuresRemaining--
	}
	c.mu.Unlock()

	if fail {
		return nil, errors.New(c.errMessage)
	}

	ch := make(chan core.StreamChunk, 1)
	ch <- core.StreamChunk{Delta: "answer", Done: true, Accumulated: "answer from " + req.Model, Model: req.Model}
	close(ch)
	return ch, nil
}

// factoryStub resolves scripted clients by provider name.
type factoryStub struct {
	mu      sync.Mutex
	clients map[string]core.LLMClient
	created []string
	failOn  map[string]bool // providers whose creation fails
}

func newFactoryStub() *factoryStub {
	return &factoryStub{
		clients: make(map[string]core.LLMClient),
		failOn:  make(map[string]bool),
	}
}

func (f *factoryStub) add(provider string, client core.LLMClient) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[provider] = client
}

func (f *factoryStub) ClientFactory() ClientFactory {
	return func(providerName, model string) (core.LLMClient, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failOn[providerName] {
			return nil, errors.New("unresolvable")
		}
		f.created = append(f.created, providerName+"/"+model)
		if client, ok := f.clients[providerName]; ok {
			return client, nil
		}
		client := &scriptedClient{}
		f.clients[providerName] = client
		return client, nil
	}
}

func testRequest() core.LLMRequest {
	return core.LLMRequest{Model: "primary-model", InputText: "question", Meta: map[string]any{
		"node_id": "answer",
		"run_id":  "run-1",
	}}
}

func TestChainPrimarySuccess(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{}
	factory.add("openai", primary)

	chain, err := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())
	if err != nil {
		t.Fatalf("NewChainClient: %v", err)
	}

	var events []runtime.Event
	resp, err := chain.Complete(runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) }), testRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if resp.Meta["routing_selected"] != "openai/gpt-4o" {
		t.Errorf("routing_selected = %v", resp.Meta["routing_selected"])
	}
	if _, has := resp.Meta["routing_attempted"]; has {
		t.Error("no fallbacks should be recorded on the happy path")
	}
	if len(events) != 0 {
		t.Errorf("no routing events expected, got %+v", events)
	}
	if primary.callCount() != 1 {
		t.Errorf("primary calls = %d", primary.callCount())
	}
}

func TestChainFallbackOnFailure(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{failuresRemaining: 1, errMessage: "rate limited"}
	secondary := &scriptedClient{}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, err := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())
	if err != nil {
		t.Fatalf("NewChainClient: %v", err)
	}

	var events []runtime.Event
	resp, err := chain.Complete(runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) }), testRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Fallback decision visible in telemetry.
	if len(events) != 1 || events[0].Kind != runtime.EventRoutingFallback {
		t.Fatalf("events = %+v, want one routing.fallback", events)
	}
	event := events[0]
	if event.Payload["provider"] != "openai" || event.Payload["fallback_to_model"] != "claude-sonnet-4-6" {
		t.Errorf("fallback event = %+v", event.Payload)
	}
	if !strings.Contains(event.Payload["trigger_error"].(string), "rate limited") {
		t.Errorf("trigger_error = %v", event.Payload["trigger_error"])
	}
	if event.RunID != "run-1" || event.NodeID != "answer" {
		t.Errorf("event attribution = run %q node %q", event.RunID, event.NodeID)
	}

	// Fallback decision visible in run metadata (response meta).
	if resp.Meta["routing_selected"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("routing_selected = %v", resp.Meta["routing_selected"])
	}
	attempted, _ := resp.Meta["routing_attempted"].([]string)
	if len(attempted) != 1 || attempted[0] != "openai/gpt-4o" {
		t.Errorf("routing_attempted = %v", attempted)
	}
}

func TestChainModelRewritePerTarget(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{failuresRemaining: 1, errMessage: "down"}
	secondary := &scriptedClient{}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	resp, err := chain.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// The fallback target's model is authoritative for its dispatch.
	if got := secondary.dispatchedModels[0]; got != "claude-sonnet-4-6" {
		t.Errorf("dispatched model = %q, want claude-sonnet-4-6", got)
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Errorf("response model = %q", resp.Model)
	}
}

func TestChainAllTargetsFail(t *testing.T) {
	factory := newFactoryStub()
	factory.add("openai", &scriptedClient{failuresRemaining: 99, errMessage: "down-1"})
	factory.add("anthropic", &scriptedClient{failuresRemaining: 99, errMessage: "down-2"})

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	_, err := chain.Complete(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "down-2") {
		t.Fatalf("expected last target's error, got %v", err)
	}
}

func TestChainCapabilityBlockedTargetsSkipped(t *testing.T) {
	// Both targets are embedding-only models: a tools request is
	// capability-blocked on every target, and no client is ever called.
	factory := newFactoryStub()
	primary := &scriptedClient{}
	factory.add("openai", primary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "text-embedding-3-small"},
		{Provider: "openai", Model: "text-embedding-3-large"},
	}}, factory.ClientFactory())

	// A tools request cannot be served by embedding-only models.
	request := testRequest()
	request.Tools = []core.LLMToolDefinition{{Name: "search"}}

	_, err := chain.Complete(context.Background(), request)
	if err == nil {
		t.Fatal("expected capability failure")
	}
	if !errors.Is(err, ErrNoCapableTarget) {
		t.Errorf("error should wrap ErrNoCapableTarget: %v", err)
	}
	if primary.callCount() != 0 {
		t.Error("capability-blocked targets must never be dispatched")
	}
}

func TestChainCapabilityAwareFallback(t *testing.T) {
	// Primary cannot serve tools; secondary can: the chain skips to it.
	factory := newFactoryStub()
	primary := &scriptedClient{}
	secondary := &scriptedClient{}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "text-embedding-3-small"}, // no tools
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},   // tools
	}}, factory.ClientFactory())

	// A tools request requires the tools capability.
	request := testRequest()
	request.Tools = []core.LLMToolDefinition{{Name: "search"}}

	resp, err := chain.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if primary.callCount() != 0 {
		t.Error("incapable primary must not be dispatched")
	}
	if resp.Meta["routing_selected"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("routing_selected = %v", resp.Meta["routing_selected"])
	}
}

func TestChainRoutesAroundDegradedProvider(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{failuresRemaining: 1, errMessage: "outage"}
	secondary := &scriptedClient{}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	now := time.Now()
	tracker := NewHealthTracker(HealthConfig{})
	tracker.Now = func() time.Time { return now }

	chain, _ := NewChainClient(ChainConfig{
		Targets: []ModelTarget{
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		},
		Health: tracker,
	}, factory.ClientFactory())

	// First call fails over and marks the primary degraded.
	if _, err := chain.Complete(context.Background(), testRequest()); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call routes around the degraded primary without attempting it.
	resp, err := chain.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if resp.Meta["routing_selected"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("degraded primary not routed around: %v", resp.Meta["routing_selected"])
	}
	if primary.callCount() != 1 {
		t.Errorf("primary calls = %d, want 1 (cooldown respected)", primary.callCount())
	}

	// Bounded period: after the cooldown expires, the primary is probed
	// again and recovers.
	now = now.Add(defaultBaseCooldown + time.Second)
	if _, err := chain.Complete(context.Background(), testRequest()); err != nil {
		t.Fatalf("post-cooldown call: %v", err)
	}
	if primary.callCount() != 2 {
		t.Errorf("primary calls = %d, want 2 (recovery probe)", primary.callCount())
	}
}

func TestChainAllTargetsInCooldownFails(t *testing.T) {
	factory := newFactoryStub()
	factory.add("openai", &scriptedClient{})
	factory.add("anthropic", &scriptedClient{})

	now := time.Now()
	tracker := NewHealthTracker(HealthConfig{})
	tracker.Now = func() time.Time { return now }

	chain, _ := NewChainClient(ChainConfig{
		Targets: []ModelTarget{
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		},
		Health: tracker,
	}, factory.ClientFactory())

	// Degrade everything out of band.
	tracker.RecordFailure("openai", "gpt-4o")
	tracker.RecordFailure("anthropic", "claude-sonnet-4-6")

	_, err := chain.Complete(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("expected cooldown failure, got %v", err)
	}

	// After cooldowns expire, routing recovers.
	now = now.Add(defaultBaseCooldown + time.Second)
	if _, err := chain.Complete(context.Background(), testRequest()); err != nil {
		t.Fatalf("recovery after cooldown: %v", err)
	}
}

func TestChainRetriesWithinTarget(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{failuresRemaining: 2, errMessage: "transient"}
	factory.add("openai", primary)
	factory.add("anthropic", &scriptedClient{})

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o", MaxAttempts: 3},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	resp, err := chain.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if primary.callCount() != 3 {
		t.Errorf("primary calls = %d, want 3 (retries within target)", primary.callCount())
	}
	if resp.Meta["routing_selected"] != "openai/gpt-4o" {
		t.Errorf("routing_selected = %v", resp.Meta["routing_selected"])
	}
}

func TestChainCanceledContextDoesNotFallback(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{failuresRemaining: 99, errMessage: "down"}
	secondary := &scriptedClient{}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := chain.Complete(ctx, testRequest())
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if secondary.callCount() != 0 {
		t.Error("cancellation must not trigger fallback")
	}
}

func TestChainMalformedConfig(t *testing.T) {
	factory := newFactoryStub()
	primary := &scriptedClient{}
	factory.add("openai", primary)
	factory.failOn["broken"] = true

	chain, err := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o", MaxAttempts: -3},
		{Provider: "", Model: "no-provider"},
		{Provider: "broken", Model: "m"},
	}}, factory.ClientFactory())
	if err != nil {
		t.Fatalf("NewChainClient: %v", err)
	}

	// Warnings surfaced, chain still builds with the resolvable target.
	if len(factory.created) == 0 {
		t.Error("resolvable target should be created")
	}
	_ = chain
	if _, err := chain.Complete(context.Background(), testRequest()); err != nil {
		t.Errorf("remaining target should work: %v", err)
	}
}

func TestChainEmptyConfigRejected(t *testing.T) {
	factory := newFactoryStub()

	if _, err := NewChainClient(ChainConfig{}, factory.ClientFactory()); !errors.Is(err, ErrNoTargets) {
		t.Errorf("empty chain should be ErrNoTargets, got %v", err)
	}

	// All targets unresolvable: construction error, not a silent chain.
	factory.failOn["openai"] = true
	if _, err := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
	}}, factory.ClientFactory()); !errors.Is(err, ErrNoTargets) {
		t.Errorf("fully unresolvable chain should be ErrNoTargets, got %v", err)
	}
}

func TestChainStreaming(t *testing.T) {
	factory := newFactoryStub()
	primary := &streamingScriptedClient{scriptedClient: &scriptedClient{failuresRemaining: 1, errMessage: "stream down"}}
	secondary := &streamingScriptedClient{scriptedClient: &scriptedClient{}}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	stream, err := chain.CompleteStream(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}

	var final core.StreamChunk
	for chunk := range stream {
		final = chunk
	}
	if !final.Done {
		t.Fatal("stream did not terminate")
	}
	if final.Meta["routing_selected"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("routing_selected = %v", final.Meta["routing_selected"])
	}
}

func TestChainStreamingCapabilityGate(t *testing.T) {
	// A target without streaming support is skipped for streaming requests.
	factory := newFactoryStub()
	primary := &scriptedClient{} // no CompleteStream method
	secondary := &streamingScriptedClient{scriptedClient: &scriptedClient{}}
	factory.add("openai", primary)
	factory.add("anthropic", secondary)

	chain, _ := NewChainClient(ChainConfig{Targets: []ModelTarget{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}}, factory.ClientFactory())

	stream, err := chain.CompleteStream(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	for chunk := range stream {
		_ = chunk
	}
	if primary.callCount() != 0 {
		t.Error("non-streaming target must not be dispatched for streaming")
	}
}

// Compile-time check that the test helpers satisfy their interfaces.
var (
	_ core.LLMClient          = (*scriptedClient)(nil)
	_ core.StreamingLLMClient = (*streamingScriptedClient)(nil)
)

// Keep the capability import referenced (used in ChainConfig docs example).
var _ = capability.CapabilityTools

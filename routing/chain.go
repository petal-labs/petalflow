// Package routing provides health-aware, capability-aware model routing:
// ordered fallback chains across provider/model pairs, health tracking that
// routes around degraded providers for a bounded period, and routing
// decisions surfaced as run events and response metadata.
//
// The chain client implements core.LLMClient, so it composes with node
// construction, instrumentation, and capture layers.
package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// ClientFactory creates a client for one provider/model pair. Implemented
// by callers with provider configuration (e.g. wrapping llmprovider.NewClient).
type ClientFactory func(providerName, model string) (core.LLMClient, error)

// ErrNoCapableTarget reports that no model in the chain satisfies the
// request's capability requirements.
var ErrNoCapableTarget = errors.New("no model in the fallback chain satisfies the required capabilities")

// ErrNoTargets reports an empty fallback chain (a configuration error
// surfaced at construction, not at request time).
var ErrNoTargets = errors.New("fallback chain has no targets")

// ChainConfig configures a fallback chain client.
type ChainConfig struct {
	// Targets is the ordered chain: primary first, then fallbacks.
	// Required: at least one target.
	Targets []ModelTarget

	// Registry resolves capability metadata (nil uses a fresh default
	// registry with the bundled snapshot).
	Registry *capability.Registry

	// Health tracks degraded models across requests (nil creates a private
	// tracker with default cooldowns).
	Health *HealthTracker

	// MalformedWarnings collects non-fatal configuration problems found at
	// construction (clamped retry counts, skipped targets). surfaced to
	// the caller for reporting; the chain still builds.
	MalformedWarnings []string
}

// ChainClient routes requests through an ordered fallback chain with
// health-aware, capability-aware target selection. Every fallback is
// emitted as a routing.fallback event and recorded on the response meta.
type ChainClient struct {
	health   *HealthTracker
	registry *capability.Registry
	targets  []chainTarget
}

type chainTarget struct {
	config ModelTarget
	client core.LLMClient
}

// NewChainClient resolves every target through the factory and returns a
// routing client. Targets whose client cannot be created are skipped with
// a warning in the config; an empty or fully-unresolvable chain is a
// construction error.
func NewChainClient(cfg ChainConfig, factory ClientFactory) (*ChainClient, error) {
	if len(cfg.Targets) == 0 {
		return nil, ErrNoTargets
	}

	health := cfg.Health
	if health == nil {
		health = NewHealthTracker(HealthConfig{})
	}
	registry := cfg.Registry
	if registry == nil {
		registry = capability.NewRegistry()
	}

	client := &ChainClient{health: health, registry: registry}

	for i, target := range cfg.Targets {
		if strings.TrimSpace(target.Provider) == "" || strings.TrimSpace(target.Model) == "" {
			cfg.MalformedWarnings = append(cfg.MalformedWarnings,
				fmt.Sprintf("targets[%d]: missing provider or model, target skipped", i))
			continue
		}
		if target.MaxAttempts < 0 {
			cfg.MalformedWarnings = append(cfg.MalformedWarnings,
				fmt.Sprintf("targets[%d] (%s/%s): negative max_attempts clamped to 1", i, target.Provider, target.Model))
			target.MaxAttempts = 1
		}

		inner, err := factory(target.Provider, target.Model)
		if err != nil {
			cfg.MalformedWarnings = append(cfg.MalformedWarnings,
				fmt.Sprintf("targets[%d] (%s/%s): client creation failed, target skipped: %v",
					i, target.Provider, target.Model, err))
			continue
		}
		client.targets = append(client.targets, chainTarget{config: target, client: inner})
	}

	if len(client.targets) == 0 {
		return nil, fmt.Errorf("%w: all %d targets failed to resolve", ErrNoTargets, len(cfg.Targets))
	}
	return client, nil
}

// Complete routes the request through the chain: the first healthy,
// capability-sufficient target that succeeds wins.
func (c *ChainClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	emit := runtime.EmitterFromContext(ctx)
	runID := requestRunID(req)

	required := capability.RequirementsFromRequest(req)
	nodeHint := requestNodeID(req)

	var lastErr error
	var lastUnsupported error
	attempted := make([]string, 0, len(c.targets))

	for i := range c.targets {
		target := &c.targets[i]
		label := target.config.Provider + "/" + target.config.Model

		// Capability check first: an incapable target is skipped without
		// counting as a health failure.
		if err := c.capabilityValidate(target, nodeHint, required); err != nil {
			lastUnsupported = err
			attempted = append(attempted, label+" (capability)")
			continue
		}

		// Health check: a degraded target is routed around.
		if !c.health.Available(target.config.Provider, target.config.Model) {
			lastErr = fmt.Errorf("target %s in cooldown", label)
			attempted = append(attempted, label+" (cooldown)")
			continue
		}

		attempts := target.config.MaxAttempts
		if attempts <= 0 {
			attempts = 1
		}
		for attempt := 1; attempt <= attempts; attempt++ {
			if ctx.Err() != nil {
				return core.LLMResponse{}, ctx.Err()
			}

			// The target's model is authoritative when dispatching to it.
			dispatched := req
			dispatched.Model = target.config.Model

			resp, err := target.client.Complete(ctx, dispatched)
			if err == nil {
				c.health.RecordSuccess(target.config.Provider, target.config.Model)
				return c.withRoutingMeta(resp, target, attempted), nil
			}

			// Caller cancellation is not a fallback trigger.
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return core.LLMResponse{}, ctx.Err()
			}

			c.health.RecordFailure(target.config.Provider, target.config.Model)
			lastErr = err
			attempted = append(attempted, label)

			// More targets remain: emit the fallback decision.
			if next := c.nextTargetIndex(i); next >= 0 {
				emit(c.fallbackEvent(runID, nodeHint, target, &c.targets[next], lastErr, attempt).
					WithAttempt(attempt))
			}
		}
	}

	// Every target failed: prefer the capability explanation when the chain
	// was capability-blocked and never dispatched.
	if len(attempted) == 0 || lastErr == nil {
		if lastUnsupported != nil {
			return core.LLMResponse{}, fmt.Errorf("%w: %v", ErrNoCapableTarget, lastUnsupported)
		}
	}
	return core.LLMResponse{}, lastErr
}

// nextTargetIndex returns the index of the next target after i, or -1.
func (c *ChainClient) nextTargetIndex(after int) int {
	if after+1 < len(c.targets) {
		return after + 1
	}
	return -1
}

// capabilityValidate wraps the registry check. Requests requiring nothing
// pass for every model, including unknown ones.
func (c *ChainClient) capabilityValidate(target *chainTarget, nodeHint string, required capability.Set) error {
	return c.registry.Validate(target.config.Provider, target.config.Model, nodeHint, required)
}

// fallbackEvent builds the routing.fallback event, attributed to the node
// that issued the request when known.
func (c *ChainClient) fallbackEvent(runID, nodeHint string, from, to *chainTarget, trigger error, attempt int) runtime.Event {
	event := runtime.NewEvent(runtime.EventRoutingFallback, runID)
	if nodeHint != "" {
		event = event.WithNode(nodeHint, core.NodeKindLLM)
	}
	event = event.
		WithPayload("provider", from.config.Provider).
		WithPayload("model", from.config.Model).
		WithPayload("trigger_error", trigger.Error()).
		WithPayload("fallback_to_provider", to.config.Provider).
		WithPayload("fallback_to_model", to.config.Model).
		WithPayload("attempt", attempt)
	if from.config.Model != "" {
		event = event.WithPayload("target_index", c.indexOf(from))
	}
	return event
}

// indexOf locates a target's position in the chain (-1 if absent).
func (c *ChainClient) indexOf(target *chainTarget) int {
	for i := range c.targets {
		if &c.targets[i] == target {
			return i
		}
	}
	return -1
}

// withRoutingMeta attaches the routing outcome to the response metadata so
// run records show which model actually answered.
func (c *ChainClient) withRoutingMeta(resp core.LLMResponse, selected *chainTarget, attempted []string) core.LLMResponse {
	meta := resp.Meta
	if meta == nil {
		meta = make(map[string]any, 2)
	} else {
		// Copy: the inner client's map must not be mutated.
		copied := make(map[string]any, len(meta)+2)
		for k, v := range meta {
			copied[k] = v
		}
		meta = copied
	}
	meta["routing_selected"] = selected.config.Provider + "/" + selected.config.Model
	if len(attempted) > 0 {
		meta["routing_attempted"] = attempted
	}
	resp.Meta = meta
	return resp
}

// requestRunID recovers the run ID from request metadata (populated by the
// LLM node).
func requestRunID(req core.LLMRequest) string {
	if req.Meta == nil {
		return ""
	}
	if id, ok := req.Meta["run_id"].(string); ok {
		return id
	}
	return ""
}

// requestNodeID recovers the originating node ID from request metadata.
func requestNodeID(req core.LLMRequest) string {
	if req.Meta == nil {
		return ""
	}
	if id, ok := req.Meta["node_id"].(string); ok {
		return id
	}
	return ""
}

// CompleteStream routes a streaming request through the chain: only
// targets that support streaming (capability-checked) are considered.
func (c *ChainClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	emit := runtime.EmitterFromContext(ctx)
	runID := requestRunID(req)
	nodeHint := requestNodeID(req)

	required := capability.RequirementsFromRequest(req)
	required.Add(capability.CapabilityStreaming)

	var lastErr error
	for i := range c.targets {
		target := &c.targets[i]
		label := target.config.Provider + "/" + target.config.Model

		if err := c.capabilityValidate(target, nodeHint, required); err != nil {
			lastErr = err
			continue
		}
		streaming, ok := target.client.(core.StreamingLLMClient)
		if !ok {
			lastErr = fmt.Errorf("target %s does not support streaming", label)
			continue
		}
		if !c.health.Available(target.config.Provider, target.config.Model) {
			lastErr = fmt.Errorf("target %s in cooldown", label)
			continue
		}

		dispatched := req
		dispatched.Model = target.config.Model

		stream, err := streaming.CompleteStream(ctx, dispatched)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, ctx.Err()
			}
			c.health.RecordFailure(target.config.Provider, target.config.Model)
			lastErr = err
			if next := c.nextTargetIndex(i); next >= 0 {
				emit(c.fallbackEvent(runID, nodeHint, target, &c.targets[next], err, 1))
			}
			continue
		}
		c.health.RecordSuccess(target.config.Provider, target.config.Model)
		// Routing metadata rides on the final chunk via a wrapper stream.
		return c.wrapStream(stream, target), nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no streaming-capable target", ErrNoCapableTarget)
	}
	return nil, lastErr
}

// wrapStream forwards chunks while tagging the terminal chunk with routing
// metadata.
func (c *ChainClient) wrapStream(stream <-chan core.StreamChunk, selected *chainTarget) <-chan core.StreamChunk {
	out := make(chan core.StreamChunk, 1)
	go func() {
		defer close(out)
		for chunk := range stream {
			if chunk.Done {
				chunk.Meta = chunkMetaWithRouting(chunk.Meta, selected.config)
			}
			out <- chunk
		}
	}()
	return out
}

// chunkMetaWithRouting merges routing info into chunk metadata.
func chunkMetaWithRouting(meta map[string]any, config ModelTarget) map[string]any {
	if meta == nil {
		meta = make(map[string]any, 1)
	}
	meta["routing_selected"] = config.Provider + "/" + config.Model
	return meta
}

// Compile-time interface checks.
var (
	_ core.LLMClient          = (*ChainClient)(nil)
	_ core.StreamingLLMClient = (*ChainClient)(nil)
)

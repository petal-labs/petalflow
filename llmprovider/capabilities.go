package llmprovider

import (
	"context"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// NewCapabilityRegistry builds a capability registry for a provider from
// its configuration: the bundled static snapshot plus the provider's
// per-model capability overrides and price overrides. Overrides win;
// malformed entries are skipped and returned as warnings (never silently
// dropped).
func NewCapabilityRegistry(providerName string, cfg hydrate.ProviderConfig) (*capability.Registry, []string) {
	registry := capability.NewRegistry()
	warnings := registry.ApplyConfig(providerName, cfg.Capabilities)

	if len(cfg.Costs) > 0 {
		costs := make(map[string]capability.Price, len(cfg.Costs))
		for model, cost := range cfg.Costs {
			costs[model] = capability.Price{
				InputPerMillion:  cost.InputPerMillion,
				OutputPerMillion: cost.OutputPerMillion,
			}
		}
		warnings = append(warnings, registry.ApplyCostConfig(providerName, costs)...)
	}
	return registry, warnings
}

// NewCostEstimator builds a CostEstimator resolving prices from the
// provider's bundled snapshot and configured cost overrides. Wire it into
// LLMEventContext.Estimator so llm.call events carry preflight estimates
// and llm.response events carry computed or unknown cost accounting.
func NewCostEstimator(providerName string, cfg hydrate.ProviderConfig) CostEstimator {
	registry, _ := NewCapabilityRegistry(providerName, cfg)
	return CostEstimator{Registry: registry}
}

// CapabilityAwareClient wraps a core.LLMClient and preflights every
// request: capabilities implied by the request shape (tools, structured
// output, reasoning, multimodal input) are validated against the model's
// registered capabilities before any provider request is dispatched, so an
// unsupported combination fails with a diagnosable error instead of an
// expensive provider call.
type CapabilityAwareClient struct {
	inner    core.LLMClient
	registry *capability.Registry
	provider string
}

// NewCapabilityAwareClient wraps a client with capability preflight.
func NewCapabilityAwareClient(inner core.LLMClient, registry *capability.Registry, providerName string) *CapabilityAwareClient {
	return &CapabilityAwareClient{inner: inner, registry: registry, provider: providerName}
}

// Complete preflights the request, then delegates to the wrapped client.
func (c *CapabilityAwareClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	if err := c.preflight(req); err != nil {
		return core.LLMResponse{}, err
	}
	return c.inner.Complete(ctx, req)
}

// StreamingClient marks clients that support streaming; the wrapper checks
// the streaming capability against the registry for such clients.
type StreamingClient interface {
	core.LLMClient
	CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error)
}

// CapabilityAwareStreamingClient wraps a streaming client with preflight,
// including the streaming capability check.
type CapabilityAwareStreamingClient struct {
	*CapabilityAwareClient
	inner StreamingClient
}

// NewCapabilityAwareStreamingClient wraps a streaming client with preflight.
func NewCapabilityAwareStreamingClient(inner StreamingClient, registry *capability.Registry, providerName string) *CapabilityAwareStreamingClient {
	return &CapabilityAwareStreamingClient{
		CapabilityAwareClient: NewCapabilityAwareClient(inner, registry, providerName),
		inner:                 inner,
	}
}

// CompleteStream preflights the request (including the streaming
// capability), then delegates to the wrapped client.
func (c *CapabilityAwareStreamingClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	if err := c.preflightStreaming(req); err != nil {
		return nil, err
	}
	return c.inner.CompleteStream(ctx, req)
}

// preflight validates the capabilities implied by the request shape.
func (c *CapabilityAwareClient) preflight(req core.LLMRequest) error {
	return c.registry.Validate(c.provider, req.Model, requestNodeHint(req), capability.RequirementsFromRequest(req))
}

// preflightStreaming additionally requires the streaming capability.
func (c *CapabilityAwareStreamingClient) preflightStreaming(req core.LLMRequest) error {
	required := capability.RequirementsFromRequest(req)
	required.Add(capability.CapabilityStreaming)
	return c.registry.Validate(c.provider, req.Model, requestNodeHint(req), required)
}

// requestNodeHint recovers the originating node ID from request metadata,
// when a node recorded it, so preflight errors name the node.
func requestNodeHint(req core.LLMRequest) string {
	if req.Meta == nil {
		return ""
	}
	if id, ok := req.Meta["node_id"].(string); ok {
		return id
	}
	return ""
}

// Compile-time interface checks.
var (
	_ core.LLMClient          = (*CapabilityAwareClient)(nil)
	_ core.StreamingLLMClient = (*CapabilityAwareStreamingClient)(nil)
)

# Capability-Aware Routing and Fallback

Part of the capability-aware routing, fallback, and budget controls work
([#174](https://github.com/petal-labs/petalflow/issues/174)). This document
covers ordered fallback chains and health-aware routing (#205), building on
capability discovery (#204, see `docs/capabilities.md`). Cost metadata and
budget ceilings are #206/#207.

## Overview

The `routing` package routes model calls through ordered fallback chains:
primary model first, then fallbacks. Target selection is:

1. **Capability-aware**: targets that cannot serve the request's implied
   capabilities (from its shape, per `docs/capabilities.md`) are skipped
   without being dispatched.
2. **Health-aware**: targets in cooldown — recently failing models — are
   routed around for a bounded period.
3. **Ordered**: the first healthy, capable target that succeeds wins;
   retries within a target are bounded by its `max_attempts` (default 1).

A failing target increments its failure streak; cooldowns grow
exponentially (base 30s, cap 5m by default) and expire — recovery is by
probe, never permanent blacklisting. Caller cancellation never triggers
fallback.

## Construction

```go
chain, err := routing.NewChainClient(routing.ChainConfig{
    Targets: []routing.ModelTarget{
        {Provider: "anthropic", Model: "claude-sonnet-4-6", MaxAttempts: 2},
        {Provider: "openai", Model: "gpt-4o"},
    },
    Registry: registry, // shared capability registry (nil = bundled snapshot)
    Health:   tracker,  // shared health tracker (nil = private, default cooldowns)
}, clientFactory)       // routing.ClientFactory: func(provider, model) (core.LLMClient, error)
```

The chain implements `core.LLMClient` (and `core.StreamingLLMClient` when
targets do), so it composes with node construction, instrumentation, and
capture. When dispatching to a target, the target's model is authoritative:
the request's model is rewritten per target.

Configuration problems surface at construction: empty or fully
unresolvable chains fail with `routing.ErrNoTargets`; unresolvable single
targets (unknown providers, missing names) are skipped and reported in
`ChainConfig.MalformedWarnings` — a chain never silently shrinks below one
usable target without saying so.

## Workflow configuration

`llm_prompt` nodes accept a `fallback_models` list in workflow YAML:

```yaml
nodes:
  - id: answer
    type: llm_prompt
    provider: anthropic
    model: claude-sonnet-4-6
    prompt_template: "{{.input}}"
    fallback_models:
      - provider: openai
        model: gpt-4o
        max_attempts: 2
```

Hydration preflights the primary model's capabilities (#204) and builds the
chain. Malformed fallback config (missing provider/model, unconfigured
provider, non-object entries) is a hard hydration error — a misconfigured
chain must not silently shrink.

## Observability

Every fallback is visible in the run event stream:

```
routing.fallback  provider=openai model=gpt-4o
                  trigger_error="rate limited"
                  fallback_to_provider=anthropic fallback_to_model=claude-sonnet-4-6
```

Events carry the run ID and originating node ID (the LLM node attaches both
to request metadata). The winning response records the outcome in its
metadata: `routing_selected` (the provider/model that answered) and
`routing_attempted` (everything tried before it, with the reason each was
skipped). Streaming responses carry `routing_selected` on the terminal
chunk. The event flows to all consumers — SSE, bus, OTel handlers — like any
runtime event.

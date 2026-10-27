# Capability Discovery and Preflight Validation

Part of the capability-aware routing, fallback, and budget controls work
([#174](https://github.com/petal-labs/petalflow/issues/174)). This document
covers capability discovery and preflight validation (#204). Fallback chains
(#205) and budget controls (#206/#207) build on it.

## Overview

The `capability` package answers one question before any provider request is
spent: does this model support what this request needs?

Capabilities are declared per provider/model in a registry. Requests imply
their own requirements from their shape — tool definitions imply `tools`, a
JSON schema or JSON response format implies `structured_output`, reasoning
effort implies `reasoning`, non-text content parts imply `multimodal_input`,
and streaming calls imply `streaming`.

## Resolution order

`capability.Registry.Model(provider, model)` resolves metadata in order:

1. **Exact model entry** — including configuration overrides, which win over
   the bundled snapshot.
2. **Model pattern** — entries ending in `*` (e.g. `claude-sonnet-4*`),
   longest pattern wins.
3. **Provider default** — conservative defaults for known providers.
4. **Unknown model** — empty capability set: **fail closed**. Any required
   capability is reported missing, with guidance to declare the model's
   capabilities via configuration.

The bundled snapshot is a best-effort table, not ground truth. It is
deliberately conservative (e.g. ollama defaults to streaming only) and must
be corrected through configuration for private or unknown models.

## Configuration

Declare per-model capability overrides on a provider:

```json
{
  "providers": {
    "acme": {
      "api_key": "...",
      "capabilities": {
        "private-model": ["tools", "structured_output", "streaming"],
        "small-*": ["streaming"]
      }
    }
  }
}
```

Capability names (`tools`, `structured_output`, `reasoning`,
`multimodal_input`, `embeddings`, `streaming`) accept common aliases.
Malformed entries are skipped and reported as warnings — they never corrupt
the registry or panic.

## Preflight validation

Validation happens twice, both times without a network call:

1. **Hydration** — when a graph is built, node-level requirements (a JSON
   schema on an `llm_prompt` node implies `structured_output`) are validated
   against the configured model. A workflow whose model cannot satisfy it
   fails at load with an error naming the node, the model, and the missing
   capability:

   ```
   node "answer": model does not support required capabilities:
   node "answer" (model anthropic/text-embedding-3-small) lacks [structured_output]
   ```

2. **Per request** — `llmprovider` wraps every client with capability
   preflight: the request shape is validated against the model immediately
   before dispatch. An unsupported combination fails with
   `capability.ErrUnsupportedCapability` instead of an expensive provider
   call. When the request carries `meta.node_id`, the error names the node.

## Behavior when no model satisfies the requirements

There is no silent fallback in this layer: an unsupported capability is a
distinct, diagnosable error path (`capability.ErrUnsupportedCapability`,
`*capability.UnsupportedError`), separating it from runtime provider errors.
Unknown models fail closed rather than passing validation by optimism — the
error explains how to declare the model's capabilities. Fallback to other
models is the concern of the routing layer (#205), which will use this same
registry to check candidates.

## Cost metadata and preflight estimates (#206)

The registry also resolves per-model prices (USD per 1M tokens), resolved
like capabilities: config override → bundled snapshot (best-effort, for
known OpenAI/Anthropic models) → unknown. Unknown is explicit: cost is
"unknown", never silently zero — a silently wrong price is worse than an
admitted gap.

Declare or override prices per provider:

```json
{
  "providers": {
    "acme": {
      "api_key": "...",
      "costs": {
        "private-model": {"input_per_million": 5.0, "output_per_million": 20.0},
        "small-*": {"input_per_million": 0.1, "output_per_million": 0.4}
      }
    }
  }
}
```

Negative prices are malformed: skipped with a warning. Zero prices are legal
(free or locally-hosted models).

### Preflight estimates

`llmprovider.CostEstimator` estimates the prompt token count of a request
before dispatch — building on the ~4 chars/token heuristic from the memory
package, with a pluggable counter for model-specific tokenizers — and the
input-side cost from the resolved price (output size is unknowable before
the call).

Estimates flow into the event stream with field names that mirror the
actuals, so tooling can diff estimate vs. actual per call:

| Event            | Fields                                                              |
|------------------|----------------------------------------------------------------------|
| `llm.call`       | `estimated_input_tokens`, `estimated_cost_usd` (or `cost_unknown`)  |
| `llm.response`   | `input_tokens`, `output_tokens`, `total_tokens`, `cost_usd` (or `cost_unknown`) |

Cost resolution on the response: a provider-reported cost wins; otherwise
the price registry computes it from actual usage; otherwise the event marks
`cost_unknown: true`. Wire it via the instrumentation layer:

```go
instrumented := llmprovider.NewInstrumentedClient(client, emitter, llmprovider.LLMEventContext{
    RunID: runID, NodeID: nodeID, NodeKind: core.NodeKindLLM,
    Provider:  "openai",
    Estimator: llmprovider.NewCostEstimator("openai", providerCfg),
})
```

These actual/estimated/unknown distinctions are what the budget ceilings
(#207) enforce against.

# Budgets and Hard Cost Ceilings

Part of the capability-aware routing, fallback, and budget controls work
([#174](https://github.com/petal-labs/petalflow/issues/174)). This document
covers cumulative budget enforcement (#207), building on cost metadata and
preflight estimates (#206, see `docs/capabilities.md`) and interacting with
fallback chains (#205, see `docs/routing.md`).

## Overview

The `budget` package accounts cumulative spend (tokens and USD) against
ceilings with a concurrency-safe **ledger**. Every provider dispatch:

1. **Checks** the ledger against the predicted spend (the preflight token
   estimate, plus cost when the model's price is known) — a predicted
   breach rejects the call **before dispatch** with
   `budget.ErrBudgetExceeded`.
2. **Records** the actual spend after the call.

Because every dispatch draws from one shared ledger, retries, parallel
branches, and map fan-outs cannot multiply spend past a ceiling: the check
runs per attempt and the ceiling is enforced atomically.

## Scopes

- **Run ledger**: created by the caller and passed via
  `runtime.RunOptions.BudgetLedger`. It is propagated to every node
  execution (`budget.LedgerFromContext`) and shared by all dispatch points
  in the run — parallel branches included.
- **Node budget**: an `llm_prompt` node's own ceiling, cumulative across
  its retries:

```yaml
nodes:
  - id: answer
    type: llm_prompt
    provider: anthropic
    model: claude-sonnet-4-6
    prompt_template: "{{.input}}"
    max_total_tokens: 50000
    max_cost_usd: 2.50
```

Malformed budgets (negative ceilings) are rejected at hydration — they can
never be satisfied, so they fail loudly instead of silently doing nothing.

```go
// Run-level ceiling from code:
ledger, err := budget.NewLedger(budget.Ceiling{
    MaxTotalTokens: 200_000,
    MaxCostUSD:     10.0,
})
opts.BudgetLedger = ledger
```

## Interaction with other subsystems

- **Fallback chains (#205)**: the chain checks the run ledger before every
  target attempt and records actuals per attempt — a fallback that would
  breach the ceiling is rejected like any other call, and the chain
  declares `budget.SelfRecorder` so outer layers do not double-count.
- **Cost-unknown models (#206)**: when a model's price is not registered,
  cost estimates are unavailable; the token gate still enforces and the
  cost gate applies only to recorded actuals. Cost is unknown, not zero.
- **Post-call actuals**: enforcement is pre-dispatch; actual spend recorded
  after a call may exceed the ceiling (the spend already happened). All
  subsequent checks then fail until the ledger is reset (per run).
- **Error policy**: budget rejections are regular node errors — they follow
  `RunOptions.ContinueOnError` like any failure, and are diagnosable via
  `budget.ErrBudgetExceeded`.

## Observability

A rejected dispatch emits a `budget.exceeded` run event naming the scope
(`run` / `node:<id>`), the gate that tripped (`total_tokens` /
`cost_usd`), the ceiling, and the spent/projected values — the same
attribution style as eval gate failures (#198). The event flows to SSE, bus,
and OTel consumers like any runtime event.

```sh
$ petalflow run workflow.yaml ...
Error: node "answer": budget exceeded (run): total_tokens projected 1250 exceeds ceiling 1000 (spent 500)
```

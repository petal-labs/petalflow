# Evaluation Harness

Part of the evaluation and deterministic replay workflows
([#173](https://github.com/petal-labs/petalflow/issues/173)). This document
covers dataset runs, graders, and latency/cost metrics (#198). Replay mode
(#197) plugs in through the `CaseRunner` interface.

## Overview

The `eval` package runs a workflow against a dataset of cases, grades every
case with configurable assertions, and aggregates latency and cost metrics
into a machine-readable report.

```go
ds, err := eval.LoadDataset("datasets/support.json")
if err != nil { ... }

runner := &eval.Runner{
    Graders: []eval.Grader{
        eval.VarEquals{Var: "answer_output"},
        eval.Contains{Var: "answer_output", Substring: "refund"},
    },
    Concurrency: 4,
}

result, err := runner.Run(ctx, ds, &eval.RuntimeCaseRunner{
    Graph: workflow,
})
if err != nil { ... }

fmt.Println(eval.Summarize(result))
if err := eval.WriteReport(result, "reports/latest.json"); err != nil { ... }
```

## Dataset format

Datasets are JSON documents: a name plus cases with inputs, initial
variables, expectations, metadata, and tags.

```json
{
  "name": "support-quality",
  "cases": [
    {
      "id": "refund-policy",
      "input": "how do I get a refund?",
      "vars": {"locale": "en"},
      "expected": {"answer_output": "submit a refund request within 30 days"},
      "tags": ["policy"]
    }
  ]
}
```

`Dataset.Validate` requires a name, at least one case, and unique non-empty
case IDs. `Runner.Tags` filters cases by tag; filtering to zero matches is
an error.

## Graders

A grader asserts on one case's run outcome. The interface is deliberately
small so graders can be implemented outside the PetalFlow tree (an
LLM-as-judge, a production trace comparison) using only this contract:

```go
type Grader interface {
    Name() string
    Grade(ctx context.Context, eval CaseEvaluation) (Grade, error)
}
```

`CaseEvaluation` carries the case, the run's final envelope, and the run
error; `Grade` carries a score, pass/fail, and a reason.

Built-in graders (a case passes when every grader passes):

| Grader       | Asserts                                                        |
|--------------|----------------------------------------------------------------|
| `VarEquals`  | Envelope var equals `Case.Expected[Var]` (canonical JSON compare, decode-type agnostic) |
| `Exact`      | Envelope var equals a fixed value                              |
| `Contains`   | Envelope var text contains a substring                         |
| `Regex`      | Envelope var text matches a pattern                            |
| `Schema`     | Envelope var validates against a JSON Schema                   |
| `Threshold`  | Numeric var within inclusive bounds (`NewThreshold`)           |

A grader that returns an error (bad pattern, missing expectation) fails the
case and records the error — it never panics the batch.

## Running

`CaseRunner` executes one case; `RuntimeCaseRunner` runs it through the live
runtime with a fresh envelope seeded from the case input and variables.
Implement `CaseRunner` for other execution modes — for example, a runner
that calls `replay.Replayer` for offline replay against fixture-backed
graphs, or a thin wrapper around a deployed workflow's HTTP API.

The batch runs with bounded concurrency (`Runner.Concurrency`, default 1)
and per-case failure isolation: a case that errors is recorded and the
batch continues. A run error fails the case even if graders tolerate it.

## Metrics and reports

Each case records duration and token usage (captured from `llm.response`
events: input, output, total tokens, and cost). The aggregate reports pass
rate, total tokens/cost, average latency, and p50/p95 latency.

Reports are JSON (`WriteReport` / `LoadReport`) for baseline diffing across
runs — the input to regression gating
([#199](https://github.com/petal-labs/petalflow/issues/199)). `Summarize`
renders a content-free one-line summary (plus failing cases and reasons)
for logs and CI output.

## CI regression gates (#199)

`eval.Gates` enforces documented quality, cost, and latency budgets against
a dataset result, optionally comparing against the last accepted baseline
artifact:

```json
{
  "graders": [
    {"type": "var_equals", "var": "answer_output"},
    {"type": "contains", "var": "answer_output", "substring": "refund"}
  ],
  "gates": {
    "min_pass_rate": 0.9,
    "max_cost_usd": 1.0,
    "max_total_tokens": 50000,
    "max_latency_p95_ms": 3000,
    "max_pass_rate_drop": 0.1,
    "max_cost_increase": 0.25,
    "max_latency_p95_increase": 0.5,
    "tolerance": 0.05
  }
}
```

Absolute budgets cap the current run's metrics. Baseline-relative gates
(`max_pass_rate_drop`, `max_cost_increase`, `max_latency_p95_increase`)
compare against the baseline artifact: a metric that regresses beyond its
cap fails; within the cap it passes.

The `tolerance` margin absorbs flakiness: a metric that misses its budget or
baseline cap by at most `tolerance` (a relative fraction, e.g. 0.05 = 5%) is
reported as a **warning** and allowed; beyond tolerance it is an **error**.
`CheckGates` returns `ErrGateFailed` when any error-severity violation
exists.

### CLI

`petalflow eval` runs the workflow against a dataset, grades the cases with
the policy's graders, and enforces the gates — exiting non-zero on gate
errors:

```sh
petalflow eval workflow.yaml \
  --dataset datasets/support.json \
  --policy eval-policy.json \
  --report reports/latest.json \
  --baseline reports/baseline.json \
  --concurrency 4
```

Flags: `--report` writes the result artifact; `--baseline` compares against
the last accepted run; `--update-baseline` rewrites the baseline (accepting
intentional changes); `--tags`/`--concurrency` scope and parallelize the
run; provider, env, and store flags mirror `petalflow run`.

Gate failures print each violation with its gate, severity, and the metric
that moved:

```
dataset support-quality: 7/10 passed (70%), tokens=1234, latency avg=210ms p95=480ms
GATE ERROR [min_pass_rate] pass_rate: pass_rate 0.700 below floor 0.900
Error: eval: quality gate failed
```

### Baseline update path

Baselines are explicit artifacts checked into your pipeline — never implicit
state. When an intentional change (a better prompt, a new model, a pricing
change) shifts metrics, re-run with `--update-baseline` after reviewing the
report; subsequent runs compare against the new baseline. A missing baseline
is not an error: the first run establishes one.

For flaky workflows, prefer widening `tolerance` and re-running the eval
command over loosening budgets: the tolerance absorbs measurement noise
while still failing on real regressions.

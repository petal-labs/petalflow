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

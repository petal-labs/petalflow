# Replay Capture

Part of the evaluation and deterministic replay workflows
([#173](https://github.com/petal-labs/petalflow/issues/173)). This document
covers the recording side: capturing a run into a durable, versioned,
redacted artifact. Playback with deterministic fixtures is tracked separately
([#197](https://github.com/petal-labs/petalflow/issues/197)).

## Overview

A replay recording is a JSON document describing one run: workflow identity,
graph definition, inputs, and — per node execution — the envelope state
around it, plus the model calls and tool invocations the node performed.
Recordings are written by `replay.Capture`, an observational layer that
implements both `runtime.EventHandler` and `runtime.NodeRecorder`:

```go
capture := replay.NewCapture(replay.DefaultPolicy())

opts := petalflow.DefaultRunOptions()
opts.EventHandler = capture.Handle
opts.NodeRecorder = capture
opts.CaptureSnapshots = true // include graph definition + inputs in the recording

recording := capture.Finish()
if err := replay.Save(recording, "runs/run-abc.json"); err != nil { ... }
```

The capture does not alter execution semantics: it only observes the event
stream and the per-node envelopes, and redacts content as it is captured, so
sensitive content is never retained.

## Format

Recordings are JSON with a schema version (`replay.FormatVersion`, currently
`1`), validated on read — `replay.Load` rejects recordings with an
incompatible version or missing invariants. The top-level structure:

| Field                | Contents                                                        |
|----------------------|-----------------------------------------------------------------|
| `version`            | Schema version                                                  |
| `run_id`             | Runtime run identifier                                          |
| `graph_name`         | Executed graph name                                             |
| `graph_definition`   | Workflow structure (only with `CaptureSnapshots`)               |
| `workflow_id` / `workflow_version` / `trigger` | Run identity metadata               |
| `inputs`             | Initial workflow input (redacted)                               |
| `started_at` / `finished_at` / `status` / `final_error` | Run bounds and outcome |
| `nodes`              | Node execution records, in execution order                      |

Each node record contains the node ID and kind, redacted input/output
envelope snapshots (`input`, `output`), elapsed time, error, and the model
calls (`llm`) and tool invocations (`tools`) performed during that execution.
Usage and latency are always captured; content is governed by policy.

## Redaction policy

The zero value of `replay.RedactionPolicy` is **default-deny**: prompts,
outputs (completions, envelope inputs, variable values), tool arguments and
results, and artifact contents are replaced with `[redacted]`. Timing, token
usage, structure, and metadata are always kept, so redacted recordings
remain useful for analysis. Additionally, common credential formats (API
keys, bearer tokens, `key=value` secrets) are scrubbed from every string
that is kept.

```go
// Default-deny (recommended).
capture := replay.NewCapture(replay.DefaultPolicy())

// Full fidelity for offline fixture replay. The artifact must be handled
// as sensitive data; never commit it to source control.
capture := replay.NewCapture(replay.AllowAllPolicy())
```

Opt-outs are explicit and granular:

```go
policy := replay.DefaultPolicy()
policy.IncludeOutputs = true          // keep completions and variable values
policy.IncludePrompts = true          // keep prompts and chat messages
policy.IncludeToolData = true         // keep tool args and results
policy.IncludeArtifacts = true        // keep artifact text and bytes
policy.ScrubCredentials = false       // disable credential scrubbing (trusted local workflows only)
policy.Placeholder = "[redacted]"     // replacement text

// Field-level overrides on top of the include flags (last match wins):
policy.Fields = []replay.FieldPolicy{
    {Field: "prompts", Action: replay.DropAction}, // drop prompts entirely
}
```

Recordings are written with `0600` permissions. Because even redacted
recordings can contain sensitive metadata, treat the capture directory as
sensitive data and keep it out of source control.

## Reading recordings

```go
rec, err := replay.Load("runs/run-abc.json") // validates version + invariants
fmt.Println(replay.Summarize(rec))            // content-free one-line summary
```

`Summarize` reports run identity, status, node count, and aggregate token
usage without content, making it safe for logs and CI output.

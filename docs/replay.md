# Replay Capture and Offline Replay

Part of the evaluation and deterministic replay workflows
([#173](https://github.com/petal-labs/petalflow/issues/173)). This document
covers recording a run into a durable artifact (#196) and replaying it
offline with deterministic fixtures (#197).

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

## Offline replay with deterministic fixtures (#197)

A full-fidelity recording doubles as a fixture store: `replay.NewFixtureStore`
derives deterministic LLM and tool fixtures from the recording, and
`replay.Verify` diffs a replay against the original.

Build the replay graph with fixture-backed dependencies — identical node
IDs, prompt construction, and output keys to the recorded workflow — then
run through the replayer:

```go
store := replay.NewFixtureStore(recording)

client := replay.NewFixtureLLMClient(store, replay.FixtureMissFail, nil).ForNode("answer")
llm := petalflow.NewLLMNode("answer", client, petalflow.LLMNodeConfig{
    Model:          "test-model",
    PromptTemplate: "Summarize: {{.input}}",
    OutputKey:      "answer_output",
})
// ... build the rest of the graph; wrap tools with
// replay.ReplayToolRegistry(liveRegistry, store, replay.FixtureMissFail)

replayer := replay.NewReplayer(recording, replay.ReplayOptions{})
result, err := replayer.Replay(ctx, g, petalflow.DefaultRunOptions())

if diffs := replay.Verify(recording, result.Recording); len(diffs) > 0 {
    // the replay diverged from the recording
}
```

### Matching strategy

- **Full-fidelity recordings** (captured with `AllowAllPolicy`): fixtures
  match by input fingerprint — model, prompts, and input text for LLM
  calls, argument maps for tools — falling back to recorded order.
- **Redacted recordings**: prompts are not available, so fixtures match
  strictly in recorded order.
- Each fixture is consumed once, which keeps replay aligned with the
  recorded sequence across loops and parallel branches. Fixture stores are
  concurrency-safe for parallel map branches.

### Mismatch policy

`FixtureMissPolicy` controls unmatched interactions:

- `replay.FixtureMissFail` (default): the interaction fails — replay is
  deterministic and never silently invokes live systems. The error guides
  re-recording.
- `replay.FixtureMissLive`: fall through to the live client or tool passed
  at construction. Breaks determinism; use for hybrid runs only.

### Re-recording

Fixtures are stale as soon as prompts, models, or workflow logic change:
fingerprint matches miss, and (under the default policy) replay fails
loudly rather than silently passing. The update path is deliberate:
re-capture with `AllowAllPolicy` and replace the fixture artifact. Keep
recordings out of source control — they are sensitive data.

### Non-replayable nodes

Nodes with external side effects or interaction requirements implement
`core.ReplayAware` (`Replayable() bool`): `webhook_call`, `webhook_trigger`,
and `human` are marked non-replayable. Nodes without the marker are
replayable by default.

`ReplayOptions.OnNonReplayable` selects the runner behavior:

- `replay.ReplayNonReplayableFail` (default): the replay fails before
  execution, naming the offending nodes. Non-replayable nodes are never
  silently invoked.
- `replay.ReplayNonReplayableStub`: the runner substitutes the node's
  recorded output envelope from the capture artifact. This makes runs
  containing human gates or webhooks replayable offline; human gates
  resolve to their recorded decision instead of pausing.

### Replay semantics

- **Clock**: frozen at the recording's start time; elapsed measurements are
  deterministic (zero). Event timestamps and usage metadata are observation
  values and are not reproduced.
- **Timeouts**: disabled during replay (`NodeTimeout` is cleared); a node
  that timed out live executes to completion offline.
- **Retries**: fixture-backed dependencies never fail transiently, so retry
  loops do not fire. A recorded tool error replays as the same error.
- **Parallel maps**: executed through the normal runtime path; fixtures are
  consumed exactly once per interaction under a lock, so branch scheduling
  order cannot corrupt matching.
- **Human gates**: cannot wait offline — see the non-replayable policy.

`Replayer.Replay` returns the final envelope plus the recording of the
replay run itself (captured at full fidelity internally), so `Verify` can
compare node sequence, per-node outputs, LLM interactions, tool results,
and terminal status against the original.

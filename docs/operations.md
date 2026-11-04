# Operations Guide

This guide summarizes operational behaviors that matter in production deployments.

## SQLite Storage

By default, PetalFlow stores workflows, tools, schedules, and run events in:

- `~/.petalflow/petalflow.db`

Daemon override:

```bash
petalflow serve --sqlite-path /var/lib/petalflow/petalflow.db
```

CLI override for tool registry:

```bash
petalflow tools list --store-path /var/lib/petalflow/petalflow.db
```

Use the same DB path across CLI and daemon when they need to share registrations/workflows.

## Sensitive Config Handling

- Tool config fields marked `sensitive: true` are encrypted at rest in SQLite.
- CLI/API responses mask sensitive values as `**********`.
- Runtime invocations receive decrypted values in memory only.

For stable key derivation across hosts/environments, set:

- `PETALFLOW_SECRET_KEY`

## Health Scheduler

When running `petalflow serve`:

- A background health scheduler runs automatically.
- Check interval follows `manifest.health.interval_seconds` (default behavior applies when unset).
- Unhealthy transitions respect `manifest.health.unhealthy_threshold`.
- On-demand checks are available via `GET /api/tools/{name}/health`.

## Retry Behavior

Transport retries are configured via manifest `transport.retry`, for example:

```json
{
  "retry": {
    "max_attempts": 3,
    "backoff_ms": 250,
    "retryable_codes": [429, 502, 503]
  }
}
```

Retry metadata is included in invocation metadata (for example `attempts`, `retry_count`).

## Workflow Scheduler

Daemon mode includes a background cron scheduler:

- Poll interval: `--workflow-schedule-poll` (default `5s`)
- Cron is UTC-only
- Due schedules are claimed with a durable lease, so multiple daemon
  replicas sharing a database can never execute the same due schedule
  concurrently. A replica that crashes mid-run leaves its lease to
  expire, after which any replica claims the schedule again.
- Missed runs coalesce into a single backfill execution at the next
  claim: the schedule runs once and `next_run_at` advances to the next
  cron occurrence.
- Failed scheduled runs retry with exponential backoff
  (`--workflow-schedule-poll` governs the claim cadence; the backoff
  base is 30s, capped at 1h). A schedule that fails 5 consecutive times
  is dead-lettered: it is disabled with `last_status=dead_letter` and
  the final error in `last_error`.
- Stop drains in-flight scheduled runs before returning.

## Async Run Worker

Daemon mode runs a background worker that executes asynchronous run
jobs (`options.async=true`):

- Poll interval: `--run-worker-poll` (default `1s`)
- Concurrency: `--run-worker-concurrency` (default `4` jobs per
  instance; the global run-slot limit still applies)
- Lease duration: `--run-worker-lease` (default `60s`). A worker
  renews the lease while a job executes; if a worker crashes, the
  lease expires and any replica reclaims the job from the run's last
  checkpoint. Lease-loss cancellation guarantees a single owner.
- Retries: `--run-job-max-attempts` (default `3`). Failed attempts
  retry with exponential backoff (base 5s, capped at 1h); exhausting
  the budget dead-letters the job with status `dead`. Deterministic
  client errors (missing workflow, invalid input, hydration failure)
  dead-letter immediately.
- Retention: `--run-job-retention` (default `24h`). Terminal jobs are
  pruned in the background; paused (human-input) jobs are never pruned.

## Event Persistence and Streaming

- Workflow run events are persisted to SQLite.
- Retrieve via `GET /api/runs/{run_id}/events`.
- `POST /api/workflows/{id}/run` supports SSE streaming with `options.stream=true`.

## Event Privacy

Runtime events, spans, and persisted payloads are **metadata by default**: no
prompt, completion, message, tool argument, or credential value leaves the
process boundary unless content capture is explicitly opted in. The
classification is applied once, at the runtime's emit choke point, so the
event store, `GET /api/runs/{run_id}/events`, SSE streams, and tracing
handlers all inherit the same behavior — there is no separate redaction path
to forget.

### Payload classification

Every event payload field belongs to one of three classes:

| Class    | Examples                                                      | Behavior                                                                    |
|----------|---------------------------------------------------------------|-----------------------------------------------------------------------------|
| metadata | model, token counts, latency, statuses, node/graph IDs         | Always emitted (low cardinality, no user content)                           |
| content  | `system_prompt`, `messages`, `input_text`, `completion`, `text`, `delta`, `arguments`, `data_preview`, `inputs` | Replaced with a **shape descriptor** (sizes, counts, key names — never values) by default |
| secret   | `password`, `api_key`, `auth_token`, `authorization`, ...      | Dropped in every mode                                                       |

Shape descriptors follow the audit-summary conventions: key names and value
kinds only (`"query": "string"`), capped at 16 keys and 64 characters per key.

Failure events (`node.failed`, `run.finished`, `routing.fallback`) carry a
typed `error_code` (`canceled`, `deadline_exceeded`, `budget_exceeded`,
`node_panic`, ...) plus a message scrubbed of credential-shaped material —
raw provider/transport errors are never persisted.

### Opt-in content capture

Capture is gated by the `CaptureMode` ladder and is **off by default**:

| `observability.petaltrace.capture_mode` | Event content capture |
|------------------------------------------|-----------------------|
| `minimal` (default)                      | Disabled — metadata only |
| `standard`                               | Enabled: prompts, completions, messages, tool arguments |
| `full`                                   | Enabled, plus edge transfer data |

In code, opt in via `RunOptions.ContentCapture` (embedded servers:
`server.ServerConfig.ContentCapture`). Captured values are:

- **Size-bounded** — each field is capped at `ContentCapture.MaxBytes`
  (default 16 KiB; oversized values degrade to a shape descriptor).
- **Redacted** — credential-shaped substrings are scrubbed
  (`security.ScrubSecrets`) and secret-named keys in tool arguments are
  masked (`[redacted]`) before emission or persistence.

The OpenTelemetry tracing handler is metadata-only by default; wire
`otel.NewTracingHandlerWithCapture(tracer, capture)` to copy content-classified
attributes onto spans. The `petalflow run --stream` CLI flag opts in locally:
streaming output goes to the caller's terminal and CLI runs do not persist
events.

### Replay fixtures

Replay recordings inherit the same gates: LLM prompts/completions and tool
arguments only appear when the captured run opted in to content capture
(`replay.FullFidelityCapture()`), and envelope snapshots are governed by the
recording's `RedactionPolicy`. Redacted or contentless recordings yield
order-matched fixtures; `replay.Save` writes recordings with `0600`
permissions and they should be treated as sensitive artifacts when captured
at full fidelity. Because captured content is redacted, replays of runs whose
prompts contain credential-shaped material may report verification diffs.

## Event Retention

Persisted run events grow unboundedly unless retention is configured.
Set it under `observability.events`:

```yaml
observability:
  events:
    retention: 720h          # age bound: delete events older than 30 days
    max_events_per_run: 1000 # count bound: keep the most recent N per run
    prune_interval: 1h       # background prune cadence (default 1h)
```

- Both bounds are optional and combine (age first, then per-run count).
- A background pruner in `petalflow serve` runs on the prune interval;
  SQLite and Postgres stores both support it.
- **Tenant scoping**: every event row carries the run's tenant. The
  store's `PruneBefore(cutoff, tenantID)` prunes a single tenant without
  touching other tenants' events; serve's background prune is age-based
  across all tenants (uniform policy). Databases created before tenant
  scoping are migrated automatically (the `tenant_id` column is added).
- Invalid values (non-Go durations, negative bounds, non-positive
  intervals) fail startup loudly.

## Dropped Events and Alerting

Event delivery is **drop-not-block**: a slow consumer never stalls
workflow execution.

| Source | Behavior | Counter |
|--------|----------|---------|
| Bus subscribers (SSE streams) | Per-subscriber buffer of 256; events beyond it while the consumer is behind are dropped | `petalflow.events.dropped{source="bus_subscriber"}` |
| Runtime event channel | Per-run buffer of 100 (`Events()`); dropped when no reader keeps up | `petalflow.events.dropped{source="runtime_channel"}` |

The `petalflow.events.dropped` metric (cumulative counter, exported when
telemetry is enabled) aggregates both sources with the single bounded
`source` dimension.

**Alert guidance**: alert when `rate(petalflow.events.dropped[5m]) > 0`.
Drops mean a consumer (SSE client, event exporter, or channel reader)
fell behind: for SSE clients this is expected when clients disconnect
mid-run; sustained non-zero rates on `runtime_channel` indicate the
`Events()` channel is not being drained.

## OpenTelemetry Signals

`petalflow serve` emits correlated traces and bounded-cardinality metrics.

### Enabling export

Telemetry export is a **documented no-op by default**: with no `OTEL_*`
configuration present, no providers are installed and nothing leaves the
process. Export activates from the standard OTel environment variables
(OTLP/HTTP):

| Variable | Effect |
|----------|--------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` (or `_TRACES` / `_METRICS` variants) | Activates the OTLP pipeline for that signal |
| `OTEL_TRACES_EXPORTER` / `OTEL_METRICS_EXPORTER` | `otlp` activates explicitly; `none` disables; other values are rejected loudly |
| `OTEL_TRACES_SAMPLER` (+ `OTEL_TRACES_SAMPLER_ARG`) | Head sampling (see below) |
| `OTEL_SERVICE_NAME` | Resource service name (default `petalflow`) |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes |

When `observability.petaltrace` is configured (enabled + valid), the
PetalTrace adapter owns the trace pipeline instead, and its capture
ladder (`minimal`/`standard`/`full`, see [Event Privacy](#event-privacy))
also drives run event content capture.

### Sampling

Head sampling decides whether a run's spans are recorded at all —
sampled-out runs produce no trace, which is the primary lever for trace
volume.

| Path | Configuration |
|------|----------------|
| OTEL_* bootstrap | `OTEL_TRACES_SAMPLER` (`always_on`, `always_off`, `traceidratio`, `parentbased_*`) with `OTEL_TRACES_SAMPLER_ARG` for ratios; unset keeps the SDK default (`parentbased_always_on`); invalid values are rejected loudly |
| PetalTrace adapter | `observability.petaltrace.sample_rate` (0.0–1.0); `sample_overrides` raises the rate per tag match (`"environment:staging": 1.0` with matching `tags`) — the highest matching override wins and overrides never reduce the base rate |

Interaction: when PetalTrace owns the trace pipeline, its sample rate is
the head sampler; the OTEL sampler variables are not consulted. When the
OTEL bootstrap owns it, `OTEL_TRACES_SAMPLER` applies. Metrics are never
sampled.

### Traces

With tracing active, each workflow run produces **one correlated trace
tree**: `run` span → `node` spans → `chat {model}` spans (LLM calls) and
tool spans (named after the tool). Transport-level tool spans from the
tool observer join the same tree underneath the tool span. Run, workflow,
schedule, webhook, and tenant context is attached to the run span; events
carry `trace_id`/`span_id` for correlation with the persisted event
stream.

Span attribute names follow the current OpenTelemetry GenAI semantic
conventions on LLM and tool spans:

| Signal | Attributes |
|--------|------------|
| LLM span (`chat {model}`) | `gen_ai.operation.name` (`chat`), `gen_ai.provider.name` (deprecated alias `gen_ai.system` also emitted), `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.response.id`, `gen_ai.usage.input_tokens` / `output_tokens` / `total_tokens`, `gen_ai.response.finish_reasons` (deprecated singular alias also emitted) |
| Tool span (tool name) | `gen_ai.operation.name` (`execute_tool`), `gen_ai.tool.name`, `gen_ai.tool.description` |
| Correlation (all spans) | `petalflow.run_id`, `petalflow.node_id`, `petalflow.workflow_id`, `petalflow.schedule_id`, `petalflow.tenant_id`, `petalflow.error_code` |

Prompt and completion content never appears on spans unless content
capture is opted in (see [Event Privacy](#event-privacy)).

### Metrics

| Instrument | Kind | Dimensions |
|------------|------|------------|
| `petalflow.node.executions` / `petalflow.node.failures` | counter | `node_kind`, `tenant_id` |
| `petalflow.node.duration` / `petalflow.run.duration` | histogram (s) | `node_kind`, `tenant_id`; run adds `graph`, `trigger`, `workflow_id`, `webhook_method` |
| `petalflow.tool.invocations` / `petalflow.tool.retries` / `petalflow.tool.health.checks` / `petalflow.tool.latency` | counter / histogram | `tool_name`, `action`, `transport`, `success`, `error_code`, `tenant_id` (bounded tool/tenant dimensions) |
| `gen_ai.client.token.usage` | histogram (`{token}`) | `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.token.type` (input/output), `tenant_id` |
| `gen_ai.client.operation.duration` | histogram (s) | `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `error.type` (on failure), `tenant_id` |
| `petalflow.events.dropped` | counter | `source` (`bus_subscriber`, `runtime_channel`) — see [Dropped Events and Alerting](#dropped-events-and-alerting) |

**Cardinality rules** (test-enforced): no metric attribute can grow per
run — `run_id`, `node_id`, `schedule_id`, and `webhook_trigger_id` never
appear on metric attributes (they are correlation attributes for spans
and events only). Every metric dimension is bounded by deployment
configuration (tenants, workflows, registered tools, configured
providers/models) or a fixed enum.

## Recommended Pre-Release Checks

```bash
# Root module
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go build ./...

# Submodules
(cd irisadapter && go test -race ./... && go build ./...)
(cd examples && go test -race ./... && go build ./...)

# Security scans
$(go env GOPATH)/bin/gosec -severity medium -exclude-dir=examples -exclude-dir=irisadapter ./...
(cd irisadapter && $(go env GOPATH)/bin/gosec -severity medium ./...)
(cd examples && $(go env GOPATH)/bin/gosec -severity medium ./...)
```

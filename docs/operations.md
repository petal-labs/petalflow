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

## OpenTelemetry Signals

With an OpenTelemetry SDK configured, PetalFlow can emit traces and metrics from runtime/tool paths.

Runtime event correlation fields:

- `trace_id`
- `span_id`

Examples of tool metrics/spans emitted by the observability layer include invocation, retry, health, and latency signals.

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

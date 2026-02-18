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
- Missed runs are not backfilled after downtime
- Overlapping due runs for the same schedule are skipped

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

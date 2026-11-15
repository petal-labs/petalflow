# Daemon API Guide

`petalflow serve` starts a single HTTP server that combines:

- Workflow APIs (`/api/workflows/*`, `/api/runs/*`, `/api/node-types`)
- Tool APIs (`/api/tools/*`)
- Health endpoint (`/health`)

Start daemon mode:

```bash
petalflow serve --host 0.0.0.0 --port 8080
```

## Quick Start

### 1) Create a Graph workflow

```bash
curl -sS -X POST http://localhost:8080/api/workflows/graph \
  -H 'Content-Type: application/json' \
  --data-binary @examples/06_cli_workflow/greeting.graph.json | jq
```

### 2) Run it synchronously

```bash
curl -sS -X POST http://localhost:8080/api/workflows/greeting_graph/run \
  -H 'Content-Type: application/json' \
  -d '{"input":{"name":"World"}}' | jq
```

### 3) Stream run events (SSE)

```bash
curl -N -X POST http://localhost:8080/api/workflows/greeting_graph/run \
  -H 'Content-Type: application/json' \
  -d '{"input":{"name":"World"},"options":{"stream":true}}'
```

### 4) Fetch persisted events for a run

```bash
curl -N http://localhost:8080/api/runs/<run_id>/events
```

## Endpoint Reference

### Base and Utility

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Health check (`{"status":"ok"}`) |
| `GET` | `/api/node-types` | Built-in + dynamic node types |

### Workflows

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/workflows/agent` | Create workflow from Agent/Task schema |
| `POST` | `/api/workflows/graph` | Create workflow from Graph IR schema |
| `GET` | `/api/workflows` | List workflows |
| `GET` | `/api/workflows/{id}` | Get workflow by ID |
| `PUT` | `/api/workflows/{id}` | Update workflow source and recompile |
| `DELETE` | `/api/workflows/{id}` | Delete workflow |
| `POST` | `/api/workflows/{id}/run` | Execute workflow, or submit asynchronously with `options.async=true` (returns `202`) |

### Webhook Trigger Route

| Method | Path | Purpose |
| --- | --- | --- |
| `ANY` | `/api/workflows/{id}/webhooks/{trigger_id}` | Invoke a specific `webhook_trigger` node |

This route validates:

- Trigger node exists and is type `webhook_trigger`
- HTTP method is allowed by node config
- Auth (for example `header_token`) if configured

If valid, the daemon runs the workflow with that trigger node as the entry point.

### Scheduling

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/workflows/{id}/schedules` | List workflow schedules |
| `POST` | `/api/workflows/{id}/schedules` | Create schedule |
| `GET` | `/api/workflows/{id}/schedules/{schedule_id}` | Get schedule |
| `PUT` | `/api/workflows/{id}/schedules/{schedule_id}` | Update schedule |
| `DELETE` | `/api/workflows/{id}/schedules/{schedule_id}` | Delete schedule |

### Runs and Events

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/runs/{run_id}` | Read non-sensitive run status |
| `POST` | `/api/runs/{run_id}/cancel` | Cancel a run terminally |
| `POST` | `/api/runs/{run_id}/retry` | Requeue a terminal async run job (returns `202`) |
| `POST` | `/api/runs/{run_id}/resume` | Resume a durable run; optional `{"resume_token":"..."}` body |
| `GET` | `/api/runs/{run_id}/pending-actions` | List the pending human action |
| `POST` | `/api/runs/{run_id}/pending-actions/{action_id}` | Complete a pending human action once |
| `GET` | `/api/runs/{run_id}/events` | Read persisted run events |
| `POST` | `/api/runs/{run_id}/artifacts` | Stream an artifact upload into the run scope |
| `GET` | `/api/runs/{run_id}/artifacts/{artifact_id}` | Stream an artifact download |
| `GET` | `/api/runs/{run_id}/export` | Export a terminal run, events, and referenced artifacts |

### Run artifacts

Artifact routes require the same bearer identity and tenant ownership as the
run routes. A run owned by another tenant and a missing run or artifact all
return the same `404 NOT_FOUND` response.

Upload a raw body:

```bash
curl -sS -X POST "http://localhost:8080/api/runs/$RUN_ID/artifacts" \
  -H "Authorization: Bearer $PETALFLOW_API_TOKEN" \
  -H 'Content-Type: application/pdf' \
  --data-binary @report.pdf | jq
```

Multipart uploads are also accepted when the file part is named `file` and
declares its own `Content-Type`. Successful uploads return `artifact_id`,
`uri`, `download_url`, `digest`, `size`, and `content_type`. The configured
artifact size and media-type policies apply while the body streams; rejected
uploads are not partially committed.

Downloads always use `Content-Disposition: attachment` and include
`Content-Length`, `Digest`, `X-Artifact-SHA256`, and `ETag` verification
headers. The daemon does not fetch arbitrary external artifact URLs; any future
server-side external fetch must pass the outbound egress policy first.

The CLI provides the same flow without placing the bearer token in command
history:

```bash
export PETALFLOW_API_TOKEN='...'
export PETALFLOW_API_URL='https://petalflow.example.com'

petalflow artifacts upload "$RUN_ID" report.pdf --content-type application/pdf
petalflow artifacts download "$RUN_ID" "$ARTIFACT_ID" --output report.pdf
petalflow artifacts export "$RUN_ID" --output "$RUN_ID.tar.gz"
```

Downloads refuse to overwrite an existing file, remove partial files on
failure, and verify `X-Artifact-SHA256` before completing.

Run export is limited to terminal runs and returns a gzip-compressed tar archive
containing `manifest.json`, `run.json`, `events.json`, and referenced artifact
files. The manifest carries per-entry SHA-256 digests, while
`X-Export-SHA256` covers the entire response. Resume tokens are omitted. Import
is not supported.

### Tools

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/tools` | Register tool |
| `GET` | `/api/tools` | List tools |
| `GET` | `/api/tools/{name}` | Get tool |
| `PUT` | `/api/tools/{name}` | Update tool |
| `DELETE` | `/api/tools/{name}` | Delete tool |
| `PUT` | `/api/tools/{name}/config` | Update tool config/secrets |
| `POST` | `/api/tools/{name}/test` | Test action invocation |
| `GET` | `/api/tools/{name}/health` | Run/read health |
| `POST` | `/api/tools/{name}/refresh` | Re-discover MCP actions |
| `PUT` | `/api/tools/{name}/overlay` | Set/clear MCP overlay |
| `PUT` | `/api/tools/{name}/disable` | Disable tool |
| `PUT` | `/api/tools/{name}/enable` | Enable tool |

## Run Request Options

`POST /api/workflows/{id}/run` accepts:

- `input` (`object`): initial envelope variables
- `options.timeout` (`duration`, default `5m`)
- `options.stream` (`bool`): stream run events via SSE
- `options.async` (`bool`): submit as a durable asynchronous job and
  return `202` immediately with `run_id`, `status_url`, and
  `events_url` — the client does not hold the HTTP request open while
  the run executes. Poll `GET /api/runs/{run_id}` for status (including
  `attempt` and `next_attempt_at` while queued), stream persisted
  events via `GET /api/runs/{run_id}/events`, cancel with
  `POST /api/runs/{run_id}/cancel` at any time, and requeue a terminal
  job with `POST /api/runs/{run_id}/retry`.
- `options.human` (`object`): human node handling

`options.human.mode` values:

- `strict` (default): fails if a human node requests input
- `auto_approve`
- `auto_reject`

Example:

```json
{
  "input": {
    "topic": "release notes"
  },
  "options": {
    "timeout": "45s",
    "human": {
      "mode": "auto_approve",
      "responded_by": "daemon"
    }
  }
}
```

## Schedule Semantics (Cron)

Schedules use standard 5-field cron:

`minute hour day-of-month month day-of-week`

Behavior:

- UTC only (`CRON_TZ` / `TZ` prefixes are rejected)
- No missed-run backfill after downtime
- If a previous scheduled run is still active, overlapping due run is skipped
- `options.stream` is not allowed for schedules
- Scheduler polling interval is controlled by `--workflow-schedule-poll`

## Startup Tool Config Discovery

On `petalflow serve`, startup tool declarations are loaded from the first existing path:

1. `--config /path/to/petalflow.yaml` (if provided)
2. `./petalflow.yaml`
3. `~/.petalflow/config.yaml`

Files are not merged. First match wins.

Example `petalflow.yaml`:

```yaml
tools:
  s3_fetch:
    type: mcp
    transport:
      mode: stdio
      command: go
      args:
        - run
        - ./examples/07_mcp_overlay/mock_mcp_server.go
    overlay: ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
    enabled: true
```

## Error Shape

Errors are returned as:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "graph validation failed",
    "details": ["..."]
  }
}
```

## Notes

- Default max request body is `1 MiB` (`--max-body` to change). When artifact
  storage is explicitly configured, artifact uploads instead use the artifact
  maximum plus bounded multipart overhead; all other endpoints retain the
  normal request limit.
- `GET /api/runs/{run_id}/events` returns SSE when supported by the response writer.
- Sensitive tool config values are masked in API responses.

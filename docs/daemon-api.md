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
| `POST` | `/api/workflows/{id}/run` | Execute workflow |

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
| `GET` | `/api/runs/{run_id}/events` | Read persisted run events |

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

- Default max request body is `1 MiB` (`--max-body` to change).
- `GET /api/runs/{run_id}/events` returns SSE when supported by the response writer.
- Sensitive tool config values are masked in API responses.

# Phase 4 Hardening Guide

This document summarizes the Phase 4 production hardening behaviors for tool + daemon workflows.

## Health Scheduler

- `petalflow serve` starts a background health scheduler automatically.
- Health checks run per tool using `manifest.health.interval_seconds` (default `30s`).
- Unhealthy transitions respect `manifest.health.unhealthy_threshold`:
  - failures below threshold: `unverified`
  - failures at/above threshold: `unhealthy`
- Health failures reset to `0` after a healthy check.

## Secrets at Rest and in Responses

- Sensitive config fields (`sensitive: true`) are encrypted at rest in SQLite-backed stores.
- API and CLI outputs mask sensitive values as `**********`.
- Runtime invocation paths still receive decrypted values in-memory.

### Key Material

- Set `PETALFLOW_SECRET_KEY` to control encryption key derivation.
- If unset, a host/user/store-scoped fallback key is derived automatically.
- For shared environments, set `PETALFLOW_SECRET_KEY` explicitly to ensure stable decryption across hosts.

## Retry/Backoff

Adapters now honor `transport.retry`:

```json
{
  "retry": {
    "max_attempts": 3,
    "backoff_ms": 250,
    "retryable_codes": [429, 502, 503]
  }
}
```

- Retry decisions are based on structured retryable errors, timeouts, and adapter-specific retry classifications.
- HTTP retryable status handling respects `retryable_codes`.
- Retry metadata is included in invocation metadata (`attempts`, `retry_count`).

## Pooling and Concurrency

- HTTP adapters use shared pooled clients with keepalive transports.
- MCP adapters use a shared client pool (`PETALFLOW_MCP_POOL_SIZE`, default `4`, max `32`).
- MCP calls are concurrency-safe via pooled client checkout per invocation.

## Structured Error Propagation

Tool invocation errors use a machine-readable shape:

```json
{
  "error": {
    "code": "UPSTREAM_FAILURE",
    "message": "tool: HTTP invoke returned status 503: busy",
    "details": {
      "retryable": true,
      "details": {
        "http_status": 503
      }
    }
  }
}
```

Common codes:

- `ACTION_NOT_FOUND`
- `INVALID_REQUEST`
- `TRANSPORT_FAILURE`
- `TIMEOUT`
- `UPSTREAM_FAILURE`
- `DECODE_FAILURE`
- `MCP_FAILURE`

## OpenTelemetry Signals

When running with an OpenTelemetry SDK/provider, daemon/tool paths emit:

- `petalflow.tool.invocations` (counter)
- `petalflow.tool.retries` (counter)
- `petalflow.tool.health.checks` (counter)
- `petalflow.tool.latency` (histogram, seconds)

Spans:

- `tool.invoke`
- `tool.health.check`

## Release Readiness Checklist

- [ ] All `go test ./...` passing.
- [ ] Sensitive config not present in API/CLI outputs.
- [ ] File-backed stores contain encrypted sensitive values (`enc:v1:` prefix).
- [ ] Health scheduler behavior validated for interval + threshold transitions.
- [ ] Retry policies verified with deterministic tests for retryable and non-retryable failures.
- [ ] OTel metrics visible in target environment.

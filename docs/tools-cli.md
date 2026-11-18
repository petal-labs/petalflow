# Tools CLI Guide

PetalFlow tools let workflows call external capabilities (HTTP services, stdio programs, MCP servers, and built-ins).

This guide covers the common CLI flow:

1. Register a tool
2. Configure values (including secrets)
3. Inspect and test actions
4. Keep the same SQLite store when running workflows

## Quick Commands

```bash
# Show all built-in + registered tools
petalflow tools list

# Inspect a tool's manifest
petalflow tools inspect <tool_name>

# Show action schemas only
petalflow tools inspect <tool_name> --actions
```

## 1) Register an HTTP Tool from a Manifest

Manifest example (`tools/echo_http.tool.json`):

```json
{
  "$schema": "https://petalflow.dev/schemas/tool-manifest/v1.json",
  "manifest_version": "1.0",
  "tool": {
    "name": "echo_http",
    "version": "0.1.0",
    "description": "Echoes request payloads"
  },
  "transport": {
    "type": "http",
    "endpoint": "http://localhost:9801/echo"
  },
  "actions": {
    "echo": {
      "inputs": {
        "value": { "type": "string", "required": true }
      },
      "outputs": {
        "value": { "type": "string" }
      }
    }
  },
  "config": {
    "api_key": { "type": "string", "required": true, "sensitive": true }
  }
}
```

Register it:

```bash
petalflow tools register echo_http \
  --type http \
  --manifest ./tools/echo_http.tool.json
```

## 2) Configure Values

```bash
# Non-sensitive values
petalflow tools config echo_http --set region=us-west-2

# Sensitive values
petalflow tools config echo_http --set-secret api_key=sk-example

# Show effective config (sensitive values are masked)
petalflow tools config echo_http --show
```

## 3) Test an Action

```bash
petalflow tools test echo_http echo --input value=hello
```

Or JSON input:

```bash
petalflow tools test echo_http echo --input-json '{"value":"hello"}'
```

## 4) Register an MCP Tool

```bash
petalflow tools register s3_fetch \
  --type mcp \
  --transport-mode stdio \
  --command go \
  --arg run \
  --arg ./examples/07_mcp_overlay/mock_mcp_server.go \
  --overlay ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

MCP maintenance commands:

```bash
petalflow tools refresh s3_fetch
petalflow tools overlay s3_fetch --set ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
petalflow tools health s3_fetch
petalflow tools health --all
```

## Built-In Tools

Built-ins are available without registration.

Example:

```bash
petalflow tools test template_render render \
  --input template='Hello, {{.name}}!' \
  --input name=Ada
```

## Safety Classification

Every tool carries a safety classification describing dangerous or
side-effecting behavior. It lives in the manifest (`safety` block) and is
part of the registry schema, so classifications survive MCP overlay
merges and cannot be stripped by re-registering a rename — stripped
manifests fall back to conservative defaults automatically.

### Declaring a Classification

In a manifest:

```json
{
  "safety": {
    "side_effect_level": "side_effect",
    "network": true,
    "downstream_write": true,
    "notes": "sends emails on the customer table"
  }
}
```

Fields:

- `side_effect_level`: `readonly` (computes without durable effects),
  `side_effect` (durable writes or unverified external access), or
  `destructive` (deletes or irreversibly overwrites).
- `network`: the tool makes outbound network calls (egress).
- `downstream_write`: the tool writes to downstream systems (email,
  webhooks, records, payments).
- `destructive`: the tool deletes or destroys targets.
- `notes`: free-form context shown alongside the classification.

An action may override the tool-level classification by declaring its
own `safety` block on the action (`side_effect_level` replaces; boolean
flags only upgrade — merges cannot under-classify).

### Origin Defaults

A manifest without `safety` is classified conservatively at
registration:

| Origin | Default classification |
| ------ | ---------------------- |
| `http` | `side_effect`, `network` |
| `mcp`  | `side_effect` (`network` for SSE / streamable HTTP modes) |
| `stdio` | `side_effect` (runs a local subprocess) |
| `native` | `side_effect` (undeclared in-process effects) |

Built-ins are classified out of the box: `template_render` is
`readonly`, and `http_fetch` is `readonly` + `network` at the tool level
with `side_effect` + `network` on the `fetch` action (methods and
bodies are caller-controlled).

### Requiring Approval

Workflow and tenant tool policies can gate dangerous tools two ways:
by exact/pattern tool name (`require_approval`) or by classification
(`require_approval_classes` with `side_effect`, `destructive`,
`network`, `downstream_write`, `readonly`). Both produce a
`needs_approval` decision for allowed tools; nothing executes until
durable approval exists, and deny rules always win. Blocking a
classification outright is a `deny` on `require_approval_classes`
semantics away: add matching tool names or use allowlists that exclude
the class.

Example `tool_policy` in `petalflow.yaml`:

```yaml
tool_policy:
  allow:
    - "*"
  require_approval_classes:
    - destructive
    - downstream_write
```

### Approval Binding

An approval authorizes exactly one invocation: the same tool, the same
action, and the same arguments the operator approved, spent once.

When a tool node needs approval it computes an arguments digest from the
arguments it built and records it on the approval request
(`arguments_digest`). The digest also appears on the `approval.requested`
and `approval.decided` events and in the run's durable approval ledger.
It identifies the invocation without revealing it, so it is safe to
show to approvers and in audit tooling.

The digest is `tool.ArgumentsDigest(reference, args)`: `sha256:`
followed by the lowercase hex SHA-256 of the canonical JSON

```json
{"arguments":{...},"reference":"tool.action"}
```

with object keys sorted, no insignificant whitespace, and no HTML
escaping. Numbers encode by value (`5` and `5.0` digest the same), and
missing arguments digest like `{}`. Arguments that cannot be encoded as
JSON, such as NaN, cannot be approved.

Once the operator decides:

- **Resume replays only the approved invocation.** On resume the node
  rebuilds its arguments. If the reference or digest differs from the
  approved request, the approval is consumed and the invocation fails
  with `nodes.ErrToolApprovalMismatch`. It never runs with arguments
  the operator did not see.
- **Approvals are single use.** Before invoking, the node claims the
  approval through the runtime's `ToolApprovalInvocationVerifier`. The
  durable verifier refuses an approval that is unknown, not approved,
  expired, already consumed, or recorded for another reference or
  digest. A refused mismatch still consumes the approval.
- **The grant travels with the invocation.** The node attaches a
  `tool.ApprovalGrant` carrying `Reference` and `ArgumentsDigest` to the
  invocation context.

#### For Tool Authors

HTTP, MCP, and stdio tools registered with the daemon run through the
tool service gate. It accepts a grant only when `tool.action` and the
invocation inputs match the grant exactly, so these adapters need no
changes.

Native tools that enforce approval themselves should check the grant
against their own canonical digest of the arguments they received:

```go
func (t *execTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	digest := tool.ArgumentsDigest(t.Name(), args)
	grant, ok := tool.ApprovalGrantForInvocation(ctx, t.Name(), digest)
	if !ok {
		return nil, errors.New("invocation is not covered by an approval")
	}
	// grant.ApprovalID and grant.DecidedBy identify the decision.
	return t.run(ctx, args, grant)
}
```

`tool.ApprovalGrantFromContext` returns the attached grant for tools
that need to inspect `Reference` and `ArgumentsDigest` directly.

#### Compatibility

Workflows without approval policies are unaffected. Existing approval
APIs keep working:

- `tool.ApprovalGrantForTool` still matches by base tool name. It
  ignores action and arguments, so prefer `ApprovalGrantForInvocation`
  wherever the invocation is known.
- Grants built without a digest (`ApprovalGrant.Bound()` is false) are
  legacy grants. The tool service gate still accepts them by tool name.
- Ledger entries and paused requests written before approval binding
  have no digest. They are matched by reference alone, so runs paused
  across an upgrade still resume.
- `runtime.ToolApprovalVerifier` and Guardian `approval` checks are
  unchanged. Tool nodes use the separate
  `RunOptions.ToolApprovalInvocationVerifier`. When it is nil (embedded
  use without a durable server), the approval bridge's decision stands
  without a claim.

## Payload Limits and Validation

Every invocation validates its inputs against the registered action
schema before any side effect: unknown actions (`ACTION_NOT_FOUND`),
missing required fields and type mismatches (`INVALID_PAYLOAD`) fail
before the adapter engages, and error messages name fields and expected
types only — never payload values.

Payloads are also size-bounded (defaults: 4 MiB input, 16 MiB output):

- `PAYLOAD_TOO_LARGE` for oversized inputs (before side effects) and
  oversized outputs (before results are handed back). Set explicit
  limits per deployment via the tool service configuration
  (`PayloadLimits.MaxInputBytes` / `MaxOutputBytes`).

Invocation budgets (all opt-in, enforced at the invocation gate before
adapters run, counting retries and parallel branches):

| Limit | Meaning |
| ----- | ------- |
| `MaxConcurrentPerTool` (with `ToolConcurrency` overrides) | in-flight invocations per tool (`TOOL_CONCURRENCY_LIMIT`) |
| `MaxPerMinutePerTool` (with `ToolRatePerMinute` overrides) | dispatch starts per tool per rolling minute (`TOOL_RATE_LIMITED`) |
| `MaxConcurrentPerScope` | in-flight invocations per workflow run (scope from the run ID) |

Per-call deadlines come from the manifest transport `timeout_ms`
(adapter defaults: 30s; the service adds a uniform wall-clock backstop
above the adapter deadline so misbehaving tools cannot hang).

## Audit Trail

Tool lifecycle events — discovery (registration, MCP refresh), 
invocations, approval decisions, rejections (policy denials), and 
failures (invalid payloads, budget limits, timeouts) — are written to a
tenant-scoped, redacted audit trail when the audit store is configured
(petalflow databases include it by default via `petalflow serve`).

Query from a running daemon:

```bash
# Tenant-scoped page (auth deployment; records of other tenants are
# never visible at any query path)
curl -H "Authorization: Bearer $PETALFLOW_API_TOKEN" \
  "http://localhost:8080/api/audit?limit=50"

# Filters: tool, workflow_id, run_id, kind (discovery|invocation|
# approval|rejection|failure), cursor pagination
curl -H "Authorization: Bearer $PETALFLOW_API_TOKEN" \
  "http://localhost:8080/api/audit?tool=http_fetch&kind=failure&cursor=..."

# Retention pruning (records strictly before the cutoff)
curl -X DELETE -H "Authorization: Bearer $PETALFLOW_API_TOKEN" \
  "http://localhost:8080/api/audit?before=2026-01-01T00:00:00Z"
```

Redaction is enforced on the write path: audit records contain typed
reason codes and redacted payload summaries (key names and value kinds,
never values). Free-form reasons pass a credential scrubber (bearer
tokens, `sk-…` keys, `password:`/`api_key:` assignments) as a final
backstop.

## Remove a Tool

```bash
petalflow tools unregister echo_http
```

## Important: Keep Store Path Consistent

Tool registrations are stored in SQLite. Use the same DB path across commands.

Default:

- `~/.petalflow/petalflow.db`

Custom path example:

```bash
petalflow tools list --store-path /tmp/petalflow.db
petalflow run workflow.yaml --store-path /tmp/petalflow.db
petalflow serve --sqlite-path /tmp/petalflow.db
```

If store paths differ, workflows may fail to find registered tools.

## Troubleshooting

- `NAME_NOT_UNIQUE`:
  A tool with that name already exists. Use a new name or unregister first.
- `REQUIRED_FIELD` on `config.<field>`:
  Set required config values with `tools config --set` or `--set-secret`.
- `UNREACHABLE` for HTTP endpoint:
  Verify the endpoint is up and reachable from your environment.
- Action not found during `tools test`:
  Use `petalflow tools inspect <name> --actions` to confirm action names.

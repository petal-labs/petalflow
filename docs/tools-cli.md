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

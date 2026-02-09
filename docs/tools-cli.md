# Tool Manifest and CLI Quickstart

This guide covers the Phase 1 tool contract workflow:

- Register tools from manifest JSON.
- List and inspect built-in + registered tools.
- Configure required and sensitive settings.
- Invoke tool actions from the CLI for fast validation.

## 1. Manifest Shape (v1.0)

Create a manifest file (for example `tools/echo_http.tool.json`):

```json
{
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

## 2. Register and Inspect

```bash
# Register from manifest
petalflow tools register echo_http \
  --type http \
  --manifest ./tools/echo_http.tool.json

# List built-ins + registered tools
petalflow tools list

# Inspect full registration (manifest + status + masked config)
petalflow tools inspect echo_http

# Inspect action schemas only
petalflow tools inspect echo_http --actions
```

## 3. Configure Values (Sensitive Masking)

```bash
# Set non-sensitive values
petalflow tools config echo_http --set region=us-west-2

# Set sensitive values
petalflow tools config echo_http --set-secret api_key="sk-example"

# Show effective config (sensitive values are masked)
petalflow tools config echo_http --show
```

Expected masking behavior:

```text
Tool: echo_http
Config:
  api_key: ********** (sensitive)
  region: us-west-2
```

## 4. Invoke Actions with `tools test`

```bash
petalflow tools test echo_http echo --input value=hello

# JSON input form
petalflow tools test echo_http echo \
  --input-json '{"value":"hello"}'
```

Built-in native tools are available immediately:

```bash
petalflow tools test template_render render \
  --input template='Hello, {{.name}}!' \
  --input name=Ada
```

## 5. Unregister

```bash
petalflow tools unregister echo_http
```

## Troubleshooting

- `REGISTRATION_VALIDATION_FAILED` with `NAME_NOT_UNIQUE`:
  Existing registration already uses that name. Run `petalflow tools list` and choose a new name or `unregister` first.
- `REGISTRATION_VALIDATION_FAILED` with `UNREACHABLE` on `transport.endpoint`:
  PetalFlow could not reach the HTTP endpoint during registration. Verify service availability and URL.
- `REGISTRATION_VALIDATION_FAILED` with `REQUIRED_FIELD` on `config.<field>`:
  A required config value is missing. Set it via `petalflow tools config <name> --set ...` or `--set-secret ...`.
- `tool test failed` for native tools:
  Verify action name using `petalflow tools inspect <name> --actions`.
- Sensitive value appears in output:
  This is a bug. Sensitive keys declared as `sensitive: true` must always render as masked values.

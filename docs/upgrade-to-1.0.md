# Upgrade from pre-1.0 to PetalFlow 1.0

This guide covers upgrades from the current pre-1.0 line to the PetalFlow 1.0
contract. Test the procedure with production-shaped data before changing a
production deployment.

## What changes in 1.0

- The minimum toolchain is Go 1.25.x; all modules declare Go 1.25.0.
- Iris v1.0.0 is the supported provider dependency. PetalFlow supports chat,
  structured output, JSON mode, tool calling, reasoning, response
  continuation, built-in tools, search grounding, multimodal input, response
  metadata, and streaming through its adapter contract.
- Iris batch operations, image generation, embeddings, reranking,
  provider-specific response items without a PetalFlow representation, and
  strict tool schemas remain unsupported. See the
  [Iris compatibility contract](./iris-compatibility.md) for exact behavior.
- Agent and graph definitions use semantic `schema_version` values. Contract
  major `1` is supported; unknown majors are rejected.
- HTTP responses advertise `PetalFlow-API-Version: 1.0.0`, and runtime events
  include `schema_version: 1.0.0`.
- Event stores add a `schema_version` column. Existing rows are interpreted as
  event schema 1.0.0 during the automatic additive migration.
- MCP `streamable_http` is the supported remote transport. Legacy `sse`
  registrations still work but are deprecated.

## Before upgrading

1. Upgrade local and CI toolchains to Go 1.25.x.
2. Pin Iris to v1.0.0 and update imports that used deprecated Iris re-exports.
3. Back up every SQLite store and PostgreSQL database. Record the running
   binary version and configuration needed for rollback.
4. Inventory workflows without `schema_version`, MCP registrations using
   legacy `sse`, custom providers, and clients that validate response fields
   strictly.
5. Run the pre-upgrade test suite and retain a representative run export for
   comparison after deployment.

## Migration steps

### 1. Update dependencies

Use the release tag for the root module and the matching separately published
adapter tag (for example, `irisadapter/v1.0.0`) when applicable. Local
`replace` directives are for checkout validation and are ignored as a
dependency's release contract. Keep the root, `irisadapter`, examples, and
Iris pins aligned with the [compatibility matrix](./compatibility.md).

Custom provider adapters must implement the Iris 1.0 interfaces and pass
capability checks before dispatch. A requested capability that the provider or
model does not advertise now fails explicitly.

### 2. Version workflow artifacts

Add the canonical kind and schema version to every maintained workflow:

```yaml
kind: agent_workflow
schema_version: "1.0.0"
```

```json
{
  "kind": "graph",
  "schema_version": "1.0.0"
}
```

Legacy unversioned workflows remain readable during the documented transition,
but newly published artifacts should always declare their version. Remove
unknown fields: loaders reject them instead of silently accepting typos.

### 3. Update API and event consumers

Read `PetalFlow-API-Version` from HTTP and SSE responses and reject an unknown
major. Within major 1, accept additive response fields and event kinds. Event
consumers should read `schema_version` and preserve unknown optional fields.

Do not strip the event version before persistence or replay. SQLite and
PostgreSQL migrations add the version column automatically; startup fails
loudly if a migration cannot complete.

### 4. Migrate MCP registrations

Move remote registrations from legacy `sse` to `streamable_http`. Keep bearer
tokens and OAuth client secrets in sensitive configuration references, never
in endpoint URLs. Follow the [MCP migration steps](./mcp-overlay.md#migrating-from-sse-to-streamable_http)
and re-run discovery after registration.

### 5. Validate the deployment

Run the commands in the [release guide](./release.md#required-validation), then
verify:

- representative workflows compile and produce the same intended outputs;
- API responses and stored events expose contract version 1.0.0;
- old event rows remain readable and new rows persist their schema version;
- every configured provider/model passes its requested capability checks;
- MCP stdio and Streamable HTTP tools can initialize, discover, and execute;
- restart, resume, export, and tenant authorization paths still pass.

## Rollback

PetalFlow migrations are additive, but there are no automatic down migrations.
If the previous binary has been verified against the upgraded store, stop 1.0
writers and redeploy that binary. Otherwise restore the pre-upgrade backup
before redeploying it. Never drop the new event column from a live database as
an ad hoc rollback.

Workflow and API artifacts created after the upgrade may contain additive 1.0
fields or event kinds that an older client does not understand. Quiesce writers
before rollback and restore the matching workflow/configuration snapshot when
necessary. Preserve failed data and logs, then publish a forward-fix patch
rather than moving or replacing the 1.0 tag.

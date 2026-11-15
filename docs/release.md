# PetalFlow 1.0 release guide

This guide defines the release contract for PetalFlow 1.0. A release is ready
only when its code, documentation, module graph, database migrations, and
published compatibility claims have been tested together.

## Prerequisites

- Go 1.25.x; every module declares Go 1.25.0.
- A clean, up-to-date `main` checkout with no uncommitted files.
- Docker with PostgreSQL 16 available for persistence conformance tests.
- `golangci-lint` v2.9.0 and `gosec` v2.22.11.
- An `OPENAI_API_KEY` in the protected CI environment for required provider
  integration tests. Anthropic credentials extend the optional matrix.
- Permission to push the release tag and publish a GitHub release.

Review the supported Go, module, Cortex, provider, and MCP combinations in the
[compatibility matrix](./compatibility.md). The exact supported and unsupported
Iris 1.0 capabilities are listed in the
[Iris compatibility contract](./iris-compatibility.md). A release must not
claim support beyond those documents without conformance tests.

## Required validation

Run the commands below from the repository root. They mirror the required CI
and release jobs; any failure blocks the release.

```bash
# Root, adapter, and examples modules
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go build ./...
(cd irisadapter && go test -race ./... && go build ./...)
(cd examples && go test -race ./... && go build ./...)

# PostgreSQL 16 compatibility (set a test-only database URL)
PETALFLOW_TEST_POSTGRES_DSN='postgres://petalflow:petalflow-test-only@localhost:5432/petalflow?sslmode=disable' \
  go test -tags=integration -race ./artifact/postgres ./bus ./server ./tool

# Provider integration tests
OPENAI_API_KEY="$OPENAI_API_KEY" \
  go test -tags=integration ./tests/integration/... -count=1 -v -timeout 120s

# Security checks
"$(go env GOPATH)/bin/gosec" -severity medium -exclude-dir=examples -exclude-dir=irisadapter ./...
(cd irisadapter && "$(go env GOPATH)/bin/gosec" -severity medium ./...)
(cd examples && "$(go env GOPATH)/bin/gosec" -severity medium ./...)

# Release documentation contract
go test ./compatibility -run 'Test(CurrentDocumentation|ReleaseDocumentation)'
```

### Integration tests

The scheduled integration workflow requires OpenAI credentials and exercises
real provider behavior. Optional Anthropic tests run when that credential is
present. Unit tests must not call paid providers or rely on developer secrets.

## Release checklist

1. Choose the version according to semantic versioning and update the
   `CHANGELOG.md` Unreleased section with user-visible changes and migrations.
2. Confirm the constants in `contract/version.go`, workflow schema versions,
   module pins, and the [compatibility matrix](./compatibility.md) describe the
   same release graph.
3. Follow the [1.0 upgrade guide](./upgrade-to-1.0.md) against a copy of a
   representative pre-1.0 deployment, including rollback rehearsal.
4. Run all required validation above and confirm CI, Codecov patch coverage,
   lint, build, security, and scheduled integration tests are green.
5. Verify examples compile against the checkout being released and that the
   release notes link to unsupported Iris capabilities and migration guidance.
6. Tag the reviewed `main` commit. Tags are immutable; never move a published
   tag to a different commit.
7. Verify the GitHub release contains every platform binary and
   `checksums-sha256.txt`, then smoke-test one downloaded binary.
8. Confirm the Go proxy can resolve the root module and any separately tagged
   submodule releases.

The development `replace` directives make auxiliary modules compile against
the exact checkout. Before publishing `irisadapter` as a separate module,
update its root PetalFlow requirement to the release tag and publish it with a
submodule tag such as `irisadapter/v1.0.0`; do not rely on a local replacement
in the published graph. The examples module remains a checkout validation
module unless it is deliberately released separately.

## Migration and rollback policy

Database migrations must be additive, covered by upgrade tests, and documented
before tagging. Back up SQLite files or PostgreSQL databases before deploying a
new major version. PetalFlow does not run automatic down migrations.

If validation fails before tagging, fix the release candidate and repeat the
checklist. If a published release is defective, do not rewrite its tag:

1. Stop new workflow writers and preserve logs, traces, and the database.
2. Restore the pre-upgrade backup when the previous binary cannot safely read
   the upgraded data; otherwise deploy the last known-good binary.
3. Mark the affected release as deprecated and publish a corrected patch with
   explicit recovery notes.
4. Re-run contract, persistence, and provider validation before resuming
   traffic.

Detailed artifact and schema changes for pre-1.0 deployments are in the
[upgrade guide](./upgrade-to-1.0.md).

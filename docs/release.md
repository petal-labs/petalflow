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
   `checksums-sha256.txt`, its SBOMs, provenance manifests, and Sigstore
   bundles, then smoke-test one downloaded binary.
8. Confirm the Go proxy can resolve the root module and any separately tagged
   submodule releases.

The development `replace` directives make auxiliary modules compile against
the exact checkout. Before publishing `irisadapter` as a separate module,
update its root PetalFlow requirement to the release tag and publish it with a
submodule tag such as `irisadapter/v1.0.0`; do not rely on a local replacement
in the published graph. The examples module remains a checkout validation
module unless it is deliberately released separately.

## Release artifacts and retention

Binary names are stable and identify their target platform:

- `petalflow-linux-amd64`
- `petalflow-darwin-arm64`
- `petalflow-windows-amd64.exe`

Each binary has two machine-readable companion files and each published
payload has a keyless Sigstore bundle:

- `<binary>.sbom.spdx.json` is an SPDX JSON SBOM generated from that binary.
- `<binary>.provenance.json` records the release version, source commit/ref,
  repository, Go toolchain, target OS/architecture, CGO and trimpath settings,
  deterministic commit build time/`SOURCE_DATE_EPOCH`, linker flags, artifact
  digest, and `go.mod`/`go.sum` input digests. The manifest contains an
  allowlisted set of fields and never copies environment variables or secrets.
- `<payload>.sigstore.json` contains its Cosign signature, short-lived signing
  certificate, timestamp, and transparency-log proof.
- `checksums-sha256.txt` preserves SHA-256 checksums for the three binaries;
  `checksums-sha256.txt.sigstore.json` signs that checksum set.

Intermediate GitHub Actions artifacts are retained for 7 days. Published
GitHub release assets follow the release's retention lifecycle; do not delete
an asset independently of its SBOM, provenance manifest, or signature bundle.
GitHub stores build-provenance attestations separately against the repository
and source workflow identity.

## Verify a downloaded release

Download the release assets, install Cosign and GitHub CLI, and run the checks
below. Replace `v1.0.0` with the release tag being verified.

```bash
VERSION=v1.0.0
REPOSITORY=petal-labs/petalflow
ARTIFACT=petalflow-linux-amd64

gh release download "$VERSION" --repo "$REPOSITORY" --dir "petalflow-$VERSION"
cd "petalflow-$VERSION"

# Verify binary digests (macOS: shasum -a 256 -c checksums-sha256.txt)
sha256sum -c checksums-sha256.txt

# Verify the workflow identity, certificate, and transparency-log proof.
IDENTITY="https://github.com/$REPOSITORY/.github/workflows/release.yml@refs/tags/$VERSION"
cosign verify-blob "$ARTIFACT" \
  --bundle "$ARTIFACT.sigstore.json" \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-blob checksums-sha256.txt \
  --bundle checksums-sha256.txt.sigstore.json \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Verify GitHub's signed build-provenance attestation.
gh attestation verify "$ARTIFACT" --repo "$REPOSITORY"

# Inspect the SBOM and provenance manifest.
jq '.spdxVersion, .packages' "$ARTIFACT.sbom.spdx.json"
jq . "$ARTIFACT.provenance.json"
```

Repeat `cosign verify-blob` for an SBOM or provenance payload when consuming it
directly; its adjacent `.sigstore.json` bundle uses the same identity. The
release workflow fails before publication if an expected binary, SBOM,
provenance field, checksum, signature bundle, signature verification, or
GitHub attestation cannot be produced.

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

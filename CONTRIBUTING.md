# Contributing to PetalFlow

Thanks for contributing to PetalFlow.

This guide focuses on fast, low-friction contributions that match current CI behavior.

## Prerequisites

- Go `1.24` (same as CI)
- `make` (optional, but convenient)
- `golangci-lint` (for local lint parity)
- `gosec` (`go install github.com/securego/gosec/v2/cmd/gosec@v2.22.11`)

PetalFlow has 3 Go modules:

- root module (`./`)
- `irisadapter/`
- `examples/`

Install dependencies:

```bash
go mod download
(cd irisadapter && go mod download)
(cd examples && go mod download)
```

## Local Development Workflow

Build CLI binary:

```bash
make build
```

Run unit/package tests (root module):

```bash
make test
```

Run lint and vet:

```bash
make lint
make vet
```

## CI-Parity Checks (Recommended Before PR)

Run the same test/build/security coverage CI runs:

```bash
# Root module tests with race + coverage
go test -race -coverprofile=coverage.out -covermode=atomic ./...

# Additional module tests
(cd irisadapter && go test -race ./...)
(cd examples && go test -race ./...)

# Security scans
$(go env GOPATH)/bin/gosec -severity medium -exclude-dir=examples -exclude-dir=irisadapter ./...
(cd irisadapter && $(go env GOPATH)/bin/gosec -severity medium ./...)
(cd examples && $(go env GOPATH)/bin/gosec -severity medium ./...)

# Build validation
go build ./...
(cd irisadapter && go build ./...)
(cd examples && go build ./...)
```

## Integration Tests

Integration tests require provider credentials.

```bash
export OPENAI_API_KEY=sk-...
go test -tags=integration ./tests/integration/... -count=1 -v -timeout 120s
```

Optional matrix expansion:

```bash
export ANTHROPIC_API_KEY=...
```

## Code Style Expectations

- Keep changes scoped to the issue/feature.
- Run `gofmt`/`goimports` on touched Go files.
- Add or update tests for behavior changes.
- Update docs/examples when UX or APIs change.
- Do not include secrets, tokens, or local environment data in commits.

If you update compiler snapshot behavior, refresh snapshots with:

```bash
make snapshot-update
```

## Pull Request Guidelines

Before opening a PR:

1. Rebase onto current `main`.
2. Run CI-parity checks locally.
3. Ensure new behavior is covered by tests.
4. Include a clear PR description:
   - what changed
   - why it changed
   - how it was tested

Commit messages can be plain or conventional; conventional prefixes like `feat:`, `fix:`, and `test:` are preferred for readability.

## Security Reporting

If you find a security issue, please avoid filing a public issue.
Use GitHub Security Advisories to report it privately to maintainers.

## License

By contributing, you agree that your contributions are licensed under the repository's [LICENSE](./LICENSE).

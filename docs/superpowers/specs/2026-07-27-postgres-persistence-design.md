# PostgreSQL Persistence for PetalFlow — Design

**Date:** 2026-07-27
**Status:** Approved (pending spec review)
**Branch:** `feat/postgres-persistence`

## 1. Goal

Add PostgreSQL as a selectable persistence backend for PetalFlow, at full
parity with the existing SQLite backend. An operator can run PetalFlow entirely
on PostgreSQL by supplying a `postgres://` DSN; nothing else changes. SQLite
remains the default and is untouched.

Out of scope for this iteration: copying data from an existing SQLite database
into PostgreSQL (fresh PG schema only).

## 2. Decisions (locked)

| Decision | Choice |
|---|---|
| Store scope | All four logical stores: event (bus), workflow + schedule (server), tool registration (tool) |
| Driver | `github.com/jackc/pgx/v5` in `database/sql` stdlib mode (`pgx/v5/stdlib`) — pure Go, keeps CGO-free build |
| Backend selection | DSN scheme auto-detect: `postgres://` / `postgresql://` → Postgres; anything else → SQLite (existing path logic) |
| Data migration | None — fresh PG schema only |
| Testing | Shared conformance suite + build-tagged (`//go:build integration`) PG tests gated on `PETALFLOW_TEST_POSTGRES_DSN`; local Postgres via Docker |
| Code-sharing strategy | Approach A — separate PG implementation per package, same package as its SQLite sibling, SQLite code untouched |

## 3. Architecture

PetalFlow's persistence is already interface-driven. PostgreSQL support means
adding new *implementations* of the existing interfaces; core, server, and
runtime consumers are unchanged.

Interfaces (unchanged):

- `bus.EventStore` — `Append`, `List`, `LatestSeq` (event sourcing / replay)
- `server.WorkflowStore` — CRUD for workflow records
- `server.WorkflowScheduleStore` — CRUD + due-scheduling for cron schedules
- `tool.Store` — tool registration CRUD + upsert

### 3.1 New files

| New file | Type | Interface(s) satisfied |
|---|---|---|
| `bus/postgresstore.go` | `bus.PostgresEventStore` | `bus.EventStore` |
| `bus/postgres_schema.sql` | embedded DDL | — |
| `server/store_postgres.go` | `server.PostgresStore` | `server.WorkflowStore` + `server.WorkflowScheduleStore` |
| `server/postgres_schema.sql` | embedded DDL | — |
| `tool/store_postgres.go` | `tool.PostgresStore` | `tool.Store` |
| `tool/postgres_schema.sql` | embedded DDL | — |
| `internal/sqldialect/placeholder.go` | `Rebind(query string) string` | shared `?` → `$N` rewriter |

Each PG store lives in the same package as its SQLite sibling so it can reuse
unexported row scanners and JSON marshaling helpers already defined there.

### 3.2 Why this is low-risk

PostgreSQL and SQLite share the vast majority of SQL used here: standard
`SELECT`/`JOIN`, and `INSERT ... ON CONFLICT ... DO UPDATE` upserts. Only three
things differ, all mechanical:

1. **Placeholders** — `?` (SQLite) vs `$N` (Postgres). Handled by
   `sqldialect.Rebind`.
2. **Auto-increment DDL** — `INTEGER PRIMARY KEY AUTOINCREMENT` (SQLite) vs
   `BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY` (Postgres). Handled in the
   per-package `postgres_schema.sql`.
3. **PRAGMAs** — `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON` (SQLite
   only). Postgres needs no equivalent; simply omitted.

The SQLite stores also carry legacy column-migration logic (`PRAGMA table_info`
+ `ALTER TABLE` to upgrade old databases). Because PG support is fresh-only, the
PG stores create the final schema in one shot and skip that logic entirely.

## 4. Backend selection

Today DSN resolution is duplicated across `cli/serve.go` and `cli/tools.go`.
This work consolidates it into one shared resolver + factory in the `cli`
package. This is a targeted improvement in service of the goal, not unrelated
refactoring.

```go
type databaseBackend string

const (
    backendSQLite   databaseBackend = "sqlite"
    backendPostgres databaseBackend = "postgres"
)

// resolveDatabaseDSN returns the resolved DSN, the detected backend, and a
// scope string. postgres:// or postgresql:// scheme selects Postgres; anything
// else uses the existing SQLite path/DSN logic unchanged.
func resolveDatabaseDSN(cmd *cobra.Command) (dsn string, backend databaseBackend, scope string, err error)
```

Per-interface factory functions select the concrete type:

```go
func openEventStore(dsn string, backend databaseBackend) (bus.EventStore, error)
func openWorkflowStore(dsn string, backend databaseBackend) (workflowStoreCloser, error) // WorkflowStore + ScheduleStore + Close
func openToolStore(dsn string, backend databaseBackend, scope string) (tool.Store, error)
```

All store-opening sites route through these:

- `cli/serve.go` (the daemon — opens all four stores)
- `cli/tools.go` `resolveToolStore` (tool + run subcommands)
- `daemon/server.go` default fallback tool store

So `postgres://…` works uniformly everywhere a store is opened.

### 4.1 Flags & env

- New backend-neutral flag `--database-dsn` and env `PETALFLOW_DATABASE_DSN`.
- Existing `--sqlite-path`, `PETALFLOW_SQLITE_PATH`, and
  `PETALFLOW_TOOLS_STORE_PATH` continue to work unchanged (a bare path or
  `file:` DSN resolves to SQLite). This preserves backward compatibility for
  existing automation.
- Precedence: explicit `--database-dsn` flag > `PETALFLOW_DATABASE_DSN` >
  existing SQLite flag/env chain > default SQLite path.

## 5. Store implementations

Each PG store mirrors its SQLite sibling method-for-method, differing only in:

- `sql.Open("pgx", dsn)` (driver registered via
  `_ "github.com/jackc/pgx/v5/stdlib"`).
- Schema created from the embedded `postgres_schema.sql`.
- Every query passed through `sqldialect.Rebind` (`?` → `$N`).
- No PRAGMAs; no legacy `ALTER TABLE` migration.
- `db.SetMaxOpenConns(10)` (sane default; Postgres handles concurrency
  natively, so the process-level `Scope` mutex used by SQLite is not needed).

### 5.1 Schema translation summary

| SQLite | PostgreSQL |
|---|---|
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY` |
| `TEXT` timestamps (RFC3339Nano) | `TEXT` (RFC3339Nano) — kept as TEXT so Go scan/marshal code is identical across backends |
| `UNIQUE(run_id, seq)`, indexes | carried over as-is (valid PG) |
| `INSERT ... ON CONFLICT(col) DO UPDATE` | carried over as-is (valid PG) |
| `PRAGMA ...` | omitted |

Timestamps stay `TEXT` in both backends deliberately: it keeps ordering,
round-trip parsing, and the existing scan/marshal helpers byte-identical, so the
PG store reuses them without divergence.

## 6. Testing

### 6.1 Shared conformance suite

Extract the behavioral assertions from the existing `*_sqlite_test.go` files
into constructor-parameterized helpers, e.g.:

```go
func runEventStoreConformance(t *testing.T, newStore func(t *testing.T) bus.EventStore)
func runWorkflowStoreConformance(t *testing.T, newStore func(t *testing.T) server.WorkflowStore)
func runScheduleStoreConformance(t *testing.T, newStore func(t *testing.T) server.WorkflowScheduleStore)
func runToolStoreConformance(t *testing.T, newStore func(t *testing.T) tool.Store)
```

SQLite runs each suite unconditionally in normal `go test ./...`. PostgreSQL
runs the *same* suite when a DSN is present. Byte-identical assertions across
both backends make "PG behaves like SQLite" a mechanically-checkable property.

### 6.2 Build-tagged integration tests

One `//go:build integration` test file per package
(`bus/postgresstore_integration_test.go`, `server/store_postgres_integration_test.go`,
`tool/store_postgres_integration_test.go`). Each:

- Skips unless `PETALFLOW_TEST_POSTGRES_DSN` is set.
- Creates a uniquely-named schema per run (avoids collisions on parallel /
  repeated runs) and drops it on cleanup.
- Runs the conformance suite against the real database.

`go test ./...` stays fast, green, and Postgres-free. `go test -tags=integration
./...` with the env var exercises the real backend. Local development uses a
Dockerized Postgres, e.g.:

```
docker run --rm -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16
export PETALFLOW_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
go test -tags=integration ./bus/... ./server/... ./tool/...
```

## 7. Error handling & edge cases

- **Fast-fail open:** an unreachable `postgres://` server fails at store-open
  with a wrapped, actionable error (`opening postgres event store: %w`),
  matching the existing SQLite error shape.
- **Sentinel-error parity:** PG unique-violations surface as `pgconn.PgError`
  code `23505`. The PG stores map that to the existing sentinels
  (`server.ErrWorkflowExists`, `ErrWorkflowNotFound`,
  `ErrWorkflowScheduleExists`, `ErrWorkflowScheduleNotFound`, and tool upsert
  semantics) so handlers stay backend-agnostic. This mapping is covered by the
  conformance suite.
- **Concurrency:** PG stores rely on Postgres's native concurrency and the
  `database/sql` pool (`SetMaxOpenConns`); they omit SQLite's process-level
  `Scope` mutex.
- **CGO-free:** pgx stdlib mode is pure Go, keeping parity with
  `modernc.org/sqlite`; the build stays CGO-free.

## 8. Non-goals

- SQLite → PostgreSQL data migration tooling.
- Changing any store interface or any core/server/runtime consumer.
- Refactoring stores into a new top-level package.
- Dialect-parameterized single-implementation stores (rejected: rewrites
  working SQLite code for no proportional benefit with only two backends).

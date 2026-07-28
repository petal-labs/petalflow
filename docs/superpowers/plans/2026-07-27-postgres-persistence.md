# PostgreSQL Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PostgreSQL as a selectable persistence backend at full parity with SQLite for all four PetalFlow stores (event, workflow, schedule, tool), chosen by DSN scheme.

**Architecture:** Each SQLite store gets a sibling PostgreSQL implementation in the same package, satisfying the same existing interface. A shared `sqldialect.Rebind` helper rewrites `?`→`$N` so query text stays nearly identical to the proven SQLite queries. A single DSN resolver + factory in the `cli` package selects the backend everywhere a store is opened. The SQLite code is not modified.

**Tech Stack:** Go 1.24, `database/sql`, `github.com/jackc/pgx/v5` (stdlib mode, pure Go / CGO-free), existing `modernc.org/sqlite`.

## Global Constraints

- Build stays CGO-free: use `github.com/jackc/pgx/v5/stdlib` (blank import), never a CGO driver — copied verbatim from spec §2/§4.
- Do not modify any store interface (`bus.EventStore`, `server.WorkflowStore`, `server.WorkflowScheduleStore`, `tool.Store`) or any core/server/runtime consumer — spec §3, §8.
- Backend detection: DSN with scheme `postgres://` or `postgresql://` → Postgres; anything else → existing SQLite path logic — spec §2, §4.
- No SQLite→Postgres data migration in this work — spec §1, §8.
- Timestamps stored as `TEXT` RFC3339Nano in both backends; do not switch Postgres to `timestamptz` — spec §5.1.
- PG stores create the final schema in one shot; no `PRAGMA`, no legacy `ALTER TABLE`/column-variant logic — spec §3.2.
- Conventional commits, subject ≤72 chars, no trailing period, no backticks/emojis in the subject.

---

## File Structure

**Created:**
- `internal/sqldialect/placeholder.go` — `Rebind(query string) string` (`?`→`$N`).
- `internal/sqldialect/placeholder_test.go` — unit tests for Rebind.
- `bus/postgres_schema.sql` — embedded PG DDL for `events`.
- `bus/postgresstore.go` — `bus.PostgresEventStore` (implements `bus.EventStore`).
- `bus/store_conformance_test.go` — shared `runEventStoreConformance` helper + SQLite invocation.
- `bus/postgresstore_integration_test.go` — `//go:build integration` PG test.
- `server/postgres_schema.sql` — embedded PG DDL for `workflows` + `workflow_schedules`.
- `server/store_postgres.go` — `server.PostgresStore` (implements `WorkflowStore` + `WorkflowScheduleStore`).
- `server/store_conformance_test.go` — shared conformance helpers + SQLite invocation.
- `server/store_postgres_integration_test.go` — `//go:build integration` PG test.
- `tool/postgres_schema.sql` — embedded PG DDL for `tool_registrations`.
- `tool/store_postgres.go` — `tool.PostgresStore` (implements `tool.Store`).
- `tool/store_conformance_test.go` — shared `runToolStoreConformance` helper + SQLite invocation.
- `tool/store_postgres_integration_test.go` — `//go:build integration` PG test.
- `cli/database.go` — `resolveDatabaseDSN` + `openEventStore`/`openWorkflowStore`/`openToolStore` factories.
- `cli/database_test.go` — unit tests for DSN/backend detection.

**Modified:**
- `go.mod` / `go.sum` — add `github.com/jackc/pgx/v5`.
- `cli/serve.go` — replace inline store construction with the factories; add `--database-dsn` flag.
- `cli/tools.go` — route `resolveToolStore` through the shared resolver/factory.
- `daemon/server.go` — default tool store honors the resolver (falls back to SQLite default when no DSN).

---

## Task 1: `sqldialect.Rebind` placeholder rewriter

**Files:**
- Create: `internal/sqldialect/placeholder.go`
- Test: `internal/sqldialect/placeholder_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func sqldialect.Rebind(query string) string` — replaces each unquoted `?` with `$1`, `$2`, … in order. Package path `github.com/petal-labs/petalflow/internal/sqldialect`.

- [ ] **Step 1: Write the failing test**

```go
package sqldialect

import "testing"

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"WHERE a = ?", "WHERE a = $1"},
		{"VALUES (?, ?, ?)", "VALUES ($1, $2, $3)"},
		{"a = ? AND b = ? LIMIT ?", "a = $1 AND b = $2 LIMIT $3"},
	}
	for _, c := range cases {
		if got := Rebind(c.in); got != c.want {
			t.Errorf("Rebind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRebindLeavesQuotedQuestionMark(t *testing.T) {
	in := "SELECT '?' AS lit WHERE a = ?"
	want := "SELECT '?' AS lit WHERE a = $1"
	if got := Rebind(in); got != want {
		t.Errorf("Rebind(%q) = %q, want %q", in, got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sqldialect/ -run TestRebind -v`
Expected: FAIL — `undefined: Rebind`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package sqldialect adapts SQL text between database backends.
package sqldialect

import "strings"

// Rebind rewrites positional '?' placeholders to PostgreSQL '$N' placeholders.
// '?' characters inside single-quoted string literals are left untouched.
func Rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inQuote := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(strconvItoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func strconvItoa(n int) string {
	return itoa(n)
}
```

Replace the `strconvItoa`/`itoa` shim with a direct `strconv.Itoa(n)` call and `import "strconv"`; the shim is only shown to keep the code block self-contained. Final form:

```go
package sqldialect

import (
	"strconv"
	"strings"
)

// Rebind rewrites positional '?' placeholders to PostgreSQL '$N' placeholders.
// '?' characters inside single-quoted string literals are left untouched.
func Rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inQuote := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sqldialect/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sqldialect/
git commit -m "feat(sqldialect): add postgres placeholder rebind helper"
```

---

## Task 2: bus PostgreSQL event store

**Files:**
- Create: `bus/postgres_schema.sql`, `bus/postgresstore.go`
- Create: `bus/store_conformance_test.go`, `bus/postgresstore_integration_test.go`
- Modify: `go.mod`, `go.sum`
- Reference (mirror, do not edit): `bus/sqlitestore.go`, `bus/sqlite_schema.sql`, `bus/store.go`

**Interfaces:**
- Consumes: `sqldialect.Rebind` (Task 1); `bus.EventStore` interface (`Append`, `List`, `LatestSeq`); existing unexported `scanEvents(rows *sql.Rows) ([]runtime.Event, error)` in package `bus`.
- Produces:
  - `type PostgresStoreConfig struct { DSN string; RetentionAge time.Duration; RetentionCount int; PruneInterval time.Duration }`
  - `func NewPostgresEventStore(cfg PostgresStoreConfig) (*PostgresEventStore, error)`
  - `*PostgresEventStore` with methods `Append`, `List`, `LatestSeq`, `RunIDs`, `Prune`, `Close` (same signatures as `*SQLiteEventStore`).
  - `func runEventStoreConformance(t *testing.T, newStore func(t *testing.T) bus.EventStore)` (test-only, exported within package for reuse by the integration test).

- [ ] **Step 1: Add the pgx dependency**

Run:
```bash
go get github.com/jackc/pgx/v5@latest
```
Expected: `go.mod` gains `github.com/jackc/pgx/v5`.

- [ ] **Step 2: Write the Postgres schema file**

Create `bus/postgres_schema.sql`:

```sql
-- PostgreSQL schema for PetalFlow event store. Mirrors bus/sqlite_schema.sql.
CREATE TABLE IF NOT EXISTS events (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id    TEXT    NOT NULL,
    seq       BIGINT  NOT NULL,
    kind      TEXT    NOT NULL,
    node_id   TEXT    NOT NULL DEFAULT '',
    node_kind TEXT    NOT NULL DEFAULT '',
    time      TEXT    NOT NULL,
    attempt   BIGINT  NOT NULL DEFAULT 1,
    elapsed   BIGINT  NOT NULL DEFAULT 0,
    payload   TEXT    NOT NULL DEFAULT '{}',
    trace_id  TEXT    NOT NULL DEFAULT '',
    span_id   TEXT    NOT NULL DEFAULT '',
    UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_events_run_id ON events (run_id);
CREATE INDEX IF NOT EXISTS idx_events_run_id_seq ON events (run_id, seq);
CREATE INDEX IF NOT EXISTS idx_events_time ON events (time);
```

- [ ] **Step 3: Write the conformance helper and its SQLite invocation (failing test)**

Create `bus/store_conformance_test.go`. This exercises the `bus.EventStore` contract and is run by both backends. Use the existing SQLite store as the first caller so the helper is proven immediately.

```go
package bus

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

// runEventStoreConformance asserts the EventStore contract. Both backends run it.
func runEventStoreConformance(t *testing.T, newStore func(t *testing.T) EventStore) {
	t.Helper()

	t.Run("append_list_latestseq", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ev := runtime.Event{
			RunID: "run-1", Seq: 1, Kind: runtime.EventKind("node.start"),
			Time: time.Now().UTC(), Attempt: 1, Payload: map[string]any{"k": "v"},
		}
		if err := s.Append(ctx, ev); err != nil {
			t.Fatalf("append: %v", err)
		}
		got, err := s.List(ctx, "run-1", 0, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].Seq != 1 {
			t.Fatalf("list = %+v, want 1 event seq=1", got)
		}
		latest, err := s.LatestSeq(ctx, "run-1")
		if err != nil {
			t.Fatalf("latestseq: %v", err)
		}
		if latest != 1 {
			t.Fatalf("latestseq = %d, want 1", latest)
		}
	})

	t.Run("list_after_seq_and_limit", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for i := uint64(1); i <= 3; i++ {
			if err := s.Append(ctx, runtime.Event{RunID: "r", Seq: i, Kind: "k", Time: time.Now().UTC(), Attempt: 1}); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
		}
		got, err := s.List(ctx, "r", 1, 1)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].Seq != 2 {
			t.Fatalf("list afterSeq=1 limit=1 = %+v, want seq=2", got)
		}
	})

	t.Run("latestseq_unknown_run_is_zero", func(t *testing.T) {
		s := newStore(t)
		latest, err := s.LatestSeq(context.Background(), "nope")
		if err != nil {
			t.Fatalf("latestseq: %v", err)
		}
		if latest != 0 {
			t.Fatalf("latestseq unknown = %d, want 0", latest)
		}
	})
}

func TestSQLiteEventStoreConformance(t *testing.T) {
	runEventStoreConformance(t, func(t *testing.T) EventStore {
		dir := t.TempDir()
		s, err := NewSQLiteEventStore(SQLiteStoreConfig{DSN: filepathJoin(dir, "events.db")})
		if err != nil {
			t.Fatalf("new sqlite store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
```

Add a tiny local `filepathJoin` wrapper or replace with `filepath.Join(dir, "events.db")` and `import "path/filepath"` (per repo path rule — never string-concatenate paths).

- [ ] **Step 4: Run to verify the conformance helper passes on SQLite**

Run: `go test ./bus/ -run TestSQLiteEventStoreConformance -v`
Expected: PASS (proves the helper is correct before the PG store exists).

- [ ] **Step 5: Write the Postgres store (mirror of `bus/sqlitestore.go`)**

Create `bus/postgresstore.go`. Mirror `bus/sqlitestore.go` method-for-method with exactly these differences:
- Package-level `//go:embed postgres_schema.sql` → `var postgresSchema string`.
- Driver blank import: `_ "github.com/jackc/pgx/v5/stdlib"`.
- `sql.Open("pgx", cfg.DSN)` instead of `sql.Open("sqlite", …)`.
- Remove the `PRAGMA journal_mode=WAL` exec entirely.
- Create schema with `db.Exec(postgresSchema)`.
- After open, `db.SetMaxOpenConns(10)`.
- Wrap every SQL string passed to `QueryContext`/`QueryRowContext`/`ExecContext` in `sqldialect.Rebind(...)`. The query text is otherwise identical to the SQLite store, including the `Prune` `NOT IN (... LIMIT ?)` subquery.
- Reuse the existing `scanEvents` helper unchanged.
- Type/receiver names: `PostgresEventStore`, `PostgresStoreConfig`, `NewPostgresEventStore`.
- Error message prefixes: use `postgresstore:` instead of `sqlitestore:` (e.g. `fmt.Errorf("postgresstore: append: %w", err)`).
- Keep the identical background pruner goroutine, `stop`/`done` channels, and `PruneInterval` default of `time.Hour`.

Constructor skeleton (the rest of the methods follow the mirror rules above):

```go
package bus

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/internal/sqldialect"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed postgres_schema.sql
var postgresSchema string

type PostgresStoreConfig struct {
	DSN            string
	RetentionAge   time.Duration
	RetentionCount int
	PruneInterval  time.Duration
}

type PostgresEventStore struct {
	db   *sql.DB
	cfg  PostgresStoreConfig
	stop chan struct{}
	done chan struct{}
}

func NewPostgresEventStore(cfg PostgresStoreConfig) (*PostgresEventStore, error) {
	if cfg.PruneInterval == 0 {
		cfg.PruneInterval = time.Hour
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: open: %w", err)
	}
	db.SetMaxOpenConns(10)
	if _, err := db.Exec(postgresSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgresstore: create schema: %w", err)
	}
	s := &PostgresEventStore{db: db, cfg: cfg, stop: make(chan struct{}), done: make(chan struct{})}
	if cfg.RetentionAge > 0 || cfg.RetentionCount > 0 {
		go s.pruneLoop()
	} else {
		close(s.done)
	}
	return s, nil
}

// Append/List/LatestSeq/RunIDs/Prune/pruneLoop/Close: mirror *SQLiteEventStore,
// wrapping each query in sqldialect.Rebind and using scanEvents unchanged.

var _ EventStore = (*PostgresEventStore)(nil)
```

- [ ] **Step 6: Verify the package compiles and SQLite tests still pass**

Run: `go build ./bus/ && go test ./bus/ -run 'TestSQLite|TestMemBus' -v`
Expected: build OK, existing tests PASS.

- [ ] **Step 7: Write the build-tagged PG integration test**

Create `bus/postgresstore_integration_test.go`:

```go
//go:build integration

package bus

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresEventStoreConformance(t *testing.T) {
	dsn := os.Getenv("PETALFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PETALFLOW_TEST_POSTGRES_DSN to run postgres integration tests")
	}
	runEventStoreConformance(t, func(t *testing.T) EventStore {
		schema := uniqueSchema(t, dsn) // creates + sets search_path to an isolated schema
		s, err := NewPostgresEventStore(PostgresStoreConfig{DSN: schema})
		if err != nil {
			t.Fatalf("new postgres store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// uniqueSchema creates a throwaway schema named from the test and returns a DSN
// whose search_path points at it, dropping it on cleanup.
func uniqueSchema(t *testing.T, baseDSN string) string {
	t.Helper()
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	name := "pf_test_" + sanitize(t.Name())
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings_Contains(baseDSN, "?") {
		sep = "&"
	}
	return baseDSN + sep + "search_path=" + name
}
```

Replace `strings_Contains` with `strings.Contains` (`import "strings"`) and implement `sanitize` to lower-case the test name and replace every non-`[a-z0-9_]` rune with `_` (Postgres identifier safety). Keep `uniqueSchema`/`sanitize` in this file so they can be reused by the other packages' integration tests (each package gets its own copy — packages don't share test helpers).

- [ ] **Step 8: (Optional local) run against Docker Postgres**

```bash
docker run --rm -d --name pf-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16
export PETALFLOW_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
go test -tags=integration ./bus/ -run TestPostgresEventStoreConformance -v
docker rm -f pf-pg
```
Expected: PASS when Docker Postgres is up; the test SKIPs without the env var.

- [ ] **Step 9: Tidy and commit**

```bash
go mod tidy
go test ./bus/
git add go.mod go.sum bus/postgres_schema.sql bus/postgresstore.go bus/store_conformance_test.go bus/postgresstore_integration_test.go
git commit -m "feat(bus): add postgres event store backend"
```

---

## Task 3: server PostgreSQL workflow + schedule store

**Files:**
- Create: `server/postgres_schema.sql`, `server/store_postgres.go`
- Create: `server/store_conformance_test.go`, `server/store_postgres_integration_test.go`
- Reference (mirror, do not edit): `server/store_sqlite.go`, `server/store.go`, `server/schedule_store.go`

**Interfaces:**
- Consumes: `sqldialect.Rebind`; `server.WorkflowStore` + `server.WorkflowScheduleStore` interfaces; sentinels `ErrWorkflowExists`, `ErrWorkflowNotFound`, `ErrWorkflowScheduleExists`, `ErrWorkflowScheduleNotFound`; existing unexported helpers `scanWorkflowRecord`, `scanWorkflowSchedule`, `normalizeWorkflowSource`, `marshalCompiledGraph` in package `server`.
- Produces:
  - `type PostgresStoreConfig struct { DSN string }`
  - `func NewPostgresStore(cfg PostgresStoreConfig) (*PostgresStore, error)`
  - `*PostgresStore` implementing all of `WorkflowStore` + `WorkflowScheduleStore` + `Close() error`.
  - Test helpers `runWorkflowStoreConformance` / `runScheduleStoreConformance`.

- [ ] **Step 1: Write the Postgres schema file**

Create `server/postgres_schema.sql` using only the canonical columns (no legacy `kind`/`source_json`/`compiled_json`):

```sql
-- PostgreSQL schema for PetalFlow workflow store. Mirrors server SQLite schema.
CREATE TABLE IF NOT EXISTS workflows (
    seq         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id          TEXT NOT NULL UNIQUE,
    schema_kind TEXT NOT NULL,
    name        TEXT,
    source      BYTEA NOT NULL,
    compiled    BYTEA,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_schedules (
    id           TEXT PRIMARY KEY,
    workflow_id  TEXT NOT NULL,
    cron_expr    TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    input_json   BYTEA NOT NULL,
    options_json BYTEA NOT NULL,
    next_run_at  TEXT NOT NULL,
    last_run_at  TEXT,
    last_run_id  TEXT,
    last_status  TEXT,
    last_error   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow
ON workflow_schedules(workflow_id);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_due
ON workflow_schedules(enabled, next_run_at);
```

Note the two deliberate type choices: `BLOB`→`BYTEA`, and the SQLite `enabled INTEGER` becomes `BOOLEAN`. Because `enabled` changes type, the PG store must scan it into a Go `bool` (see Step 3), whereas the SQLite store scans an int. This is the one place the scan helper cannot be reused as-is — handle `enabled` explicitly in the PG store's schedule methods and pass through the rest of `scanWorkflowSchedule`'s fields, OR store `enabled` as `INTEGER`/`BIGINT` in PG too to reuse the helper verbatim. **Decision: store `enabled` as `BOOLEAN` and give the PG schedule store its own row-scan that reads `enabled` as bool.** If mirroring `scanWorkflowSchedule` proves to depend on int scanning, fall back to `SMALLINT` with `1/0` to reuse the helper — pick whichever keeps the diff smallest after reading `scanWorkflowSchedule`.

- [ ] **Step 2: Write conformance helpers + SQLite invocation (failing test)**

Create `server/store_conformance_test.go` with `runWorkflowStoreConformance(t, newStore func(t *testing.T) WorkflowStore)` and `runScheduleStoreConformance(t, newStore func(t *testing.T) WorkflowScheduleStore)`. Cover, at minimum:

- Workflow: `Create` then `Get` returns the record; `Create` duplicate id → `ErrWorkflowExists`; `Update` missing id → `ErrWorkflowNotFound`; `List` returns created records; `Delete` missing id → `ErrWorkflowNotFound`; `Delete` existing → subsequent `Get` reports not-found.
- Schedule: `CreateSchedule` + `GetSchedule`; duplicate id → `ErrWorkflowScheduleExists`; `UpdateSchedule` missing → `ErrWorkflowScheduleNotFound`; `ListSchedules` by workflow; `ListDueSchedules(now, limit)` returns only enabled schedules with `next_run_at <= now`; `DeleteSchedulesByWorkflow` removes all; `DeleteSchedule` missing → `ErrWorkflowScheduleNotFound`.

Add `TestSQLiteWorkflowStoreConformance` / `TestSQLiteScheduleStoreConformance` that construct `NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "wf.db")})` and run the helpers. Build the concrete `WorkflowRecord` / `WorkflowSchedule` values from the struct definitions in `server/store.go:19-28` and `server/schedule_store.go:21-38`.

- [ ] **Step 3: Run to verify conformance passes on SQLite**

Run: `go test ./server/ -run 'TestSQLite(Workflow|Schedule)StoreConformance' -v`
Expected: PASS.

- [ ] **Step 4: Write the Postgres store (mirror of `server/store_sqlite.go`, canonical-only)**

Create `server/store_postgres.go`. Mirror the SQLite store with these differences:
- `//go:embed postgres_schema.sql` → `var workflowPostgresSchema string`; `sql.Open("pgx", …)`; `db.SetMaxOpenConns(10)`; no PRAGMAs; no legacy migration or column inspection.
- The struct has no `workflowHasLegacy*` fields. `Create`/`Update` use only the canonical query, rebound:
  - Insert: `sqldialect.Rebind("INSERT INTO workflows (id, schema_kind, name, source, compiled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)")` — i.e. `workflowInsertQueries[0]` with no legacy args.
  - Update: `sqldialect.Rebind("UPDATE workflows SET schema_kind = ?, name = ?, source = ?, compiled = ?, created_at = ?, updated_at = ? WHERE id = ?")` — i.e. `workflowUpdateQueries[0]`.
- Reuse `normalizeWorkflowSource`, `marshalCompiledGraph`, `scanWorkflowRecord` unchanged. Reuse `scanWorkflowSchedule` unless the `enabled` bool decision in Task 3 Step 1 requires a dedicated scan.
- **Sentinel mapping:** replace the SQLite string-match unique-violation detection (`isWorkflowSQLiteUniqueViolation`, `strings.Contains(msg, "UNIQUE constraint failed: workflows.id")`) with a PG error-code check:

```go
import "github.com/jackc/pgx/v5/pgconn"

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

Use it in `Create` → `ErrWorkflowExists` and `CreateSchedule` → `ErrWorkflowScheduleExists`. For `Update`/`Delete`/`UpdateSchedule`/`DeleteSchedule`, use `RowsAffected() == 0` → the corresponding NotFound sentinel, exactly as the SQLite store does.
- Receiver/type/constructor names: `PostgresStore`, `PostgresStoreConfig`, `NewPostgresStore`. Error prefixes: `workflow postgres store: …`.
- `enabled` writes: pass a Go `bool` (not `1`/`0`) if the column is `BOOLEAN`.
- Add compile-time checks: `var _ WorkflowStore = (*PostgresStore)(nil)` and `var _ WorkflowScheduleStore = (*PostgresStore)(nil)`.

- [ ] **Step 5: Verify build + SQLite tests unaffected**

Run: `go build ./server/ && go test ./server/ -run 'TestSQLite' -v`
Expected: build OK, PASS.

- [ ] **Step 6: Write the build-tagged PG integration test**

Create `server/store_postgres_integration_test.go` (`//go:build integration`) mirroring Task 2 Step 7: copy `uniqueSchema`/`sanitize` into this package, gate on `PETALFLOW_TEST_POSTGRES_DSN`, and run both `runWorkflowStoreConformance` and `runScheduleStoreConformance` against `NewPostgresStore`.

- [ ] **Step 7: (Optional local) run against Docker Postgres**

```bash
export PETALFLOW_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
go test -tags=integration ./server/ -run TestPostgres -v
```
Expected: PASS with Docker PG up; SKIP without the env var.

- [ ] **Step 8: Commit**

```bash
go test ./server/
git add server/postgres_schema.sql server/store_postgres.go server/store_conformance_test.go server/store_postgres_integration_test.go
git commit -m "feat(server): add postgres workflow and schedule store"
```

---

## Task 4: tool PostgreSQL registration store

**Files:**
- Create: `tool/postgres_schema.sql`, `tool/store_postgres.go`
- Create: `tool/store_conformance_test.go`, `tool/store_postgres_integration_test.go`
- Reference (mirror, do not edit): `tool/store_sqlite.go`, `tool/registry.go`

**Interfaces:**
- Consumes: `sqldialect.Rebind`; `tool.Store` interface (`List`, `Get`, `Upsert`, `Delete`); existing unexported helpers `encodeRegistration`, `decodeRegistration`, `cloneRegistration`, `cloneRegistrations` in package `tool`; `ToolRegistration` struct and `StatusUnverified` constant.
- Produces:
  - `type PostgresStoreConfig struct { DSN string; Scope string }`
  - `func NewPostgresStore(cfg PostgresStoreConfig) (*PostgresStore, error)`
  - `*PostgresStore` implementing `tool.Store` + `Close() error`.
  - Test helper `runToolStoreConformance`.

- [ ] **Step 1: Write the Postgres schema file**

Create `tool/postgres_schema.sql` (`BLOB`→`BYTEA`):

```sql
-- PostgreSQL schema for PetalFlow tool registration store.
CREATE TABLE IF NOT EXISTS tool_registrations (
    name       TEXT PRIMARY KEY,
    payload    BYTEA NOT NULL,
    updated_at TEXT NOT NULL
);
```

- [ ] **Step 2: Write the conformance helper + SQLite invocation (failing test)**

Create `tool/store_conformance_test.go` with `runToolStoreConformance(t, newStore func(t *testing.T) Store)`. Cover: `Upsert` new registration then `Get` returns it with `found=true`; `Get` missing → `found=false, err=nil`; `Upsert` again with same name updates payload (second `Get` reflects the change); `List` returns name-sorted registrations; `Upsert` preserves `RegisteredAt` across updates and defaults `Status` to `StatusUnverified` when empty; `Delete` existing then `Get` → not found; `Delete` missing is a no-op (no error). Construct `ToolRegistration` values per its struct in `tool` (set at least `Name`; leave `Status`/`RegisteredAt` zero to exercise the defaulting). Add `TestSQLiteToolStoreConformance` using `NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "tools.db"), Scope: "test"})`.

- [ ] **Step 3: Run to verify conformance passes on SQLite**

Run: `go test ./tool/ -run TestSQLiteToolStoreConformance -v`
Expected: PASS.

- [ ] **Step 4: Write the Postgres store (mirror of `tool/store_sqlite.go`)**

Create `tool/store_postgres.go`. Mirror the SQLite store with these differences:
- `//go:embed postgres_schema.sql` → `var toolPostgresSchema string`; `sql.Open("pgx", …)`; `db.SetMaxOpenConns(10)`; no PRAGMA; no `migrateLegacySQLiteSchema` call and no `PRAGMA table_info` inspection.
- Keep the `scope` field and its defaulting (`scope = cfg.DSN` when empty) — `encode/decodeRegistration` depend on it for secret-key derivation; reuse those helpers unchanged.
- `List`/`Get`/`Delete`: identical query text wrapped in `sqldialect.Rebind`. Reuse `cloneRegistration(s)`.
- `Upsert`: identical logic (read existing via `Get`, default `Status`/`RegisteredAt`/`LastHealthCheck`, then the `INSERT … ON CONFLICT(name) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at` upsert — valid in PG) wrapped in `sqldialect.Rebind`. The `excluded` pseudo-table works identically in Postgres.
- Receiver/type/constructor names: `PostgresStore`, `PostgresStoreConfig`, `NewPostgresStore`. Error prefixes: `tool: postgres …`.
- Add `var _ Store = (*PostgresStore)(nil)`.

- [ ] **Step 5: Verify build + SQLite tests unaffected**

Run: `go build ./tool/ && go test ./tool/ -run 'TestSQLite' -v`
Expected: build OK, PASS.

- [ ] **Step 6: Write the build-tagged PG integration test**

Create `tool/store_postgres_integration_test.go` (`//go:build integration`) mirroring Task 2 Step 7 (copy `uniqueSchema`/`sanitize` into package `tool`), gate on `PETALFLOW_TEST_POSTGRES_DSN`, run `runToolStoreConformance` against `NewPostgresStore(PostgresStoreConfig{DSN: schema, Scope: "test"})`.

- [ ] **Step 7: Commit**

```bash
go test ./tool/
git add tool/postgres_schema.sql tool/store_postgres.go tool/store_conformance_test.go tool/store_postgres_integration_test.go
git commit -m "feat(tool): add postgres registration store backend"
```

---

## Task 5: CLI backend selection + wiring

**Files:**
- Create: `cli/database.go`, `cli/database_test.go`
- Modify: `cli/serve.go`, `cli/tools.go`, `daemon/server.go`
- Reference: `cli/serve.go:66-186,241-266`, `cli/tools.go:655-680`, `daemon/server.go:40-55`

**Interfaces:**
- Consumes: `bus.NewSQLiteEventStore` / `bus.NewPostgresEventStore`; `server.NewSQLiteStore` / `server.NewPostgresStore`; `tool.NewSQLiteStore` / `tool.NewPostgresStore` / `tool.NewDefaultSQLiteStore` / `tool.DefaultSQLitePath`.
- Produces:
  - `type databaseBackend string` with `backendSQLite`, `backendPostgres`.
  - `func detectBackend(dsn string) databaseBackend`
  - `func resolveDatabaseDSN(cmd *cobra.Command) (dsn string, backend databaseBackend, scope string, err error)`
  - `func openEventStore(dsn string, backend databaseBackend) (bus.EventStore, error)` — note: returns an `EventStore` that also satisfies `io.Closer`; expose `Close` via a small interface so `serve.go` can defer-close it.
  - `func openWorkflowStore(dsn string, backend databaseBackend) (workflowStore, error)` where `workflowStore` is a local interface embedding `server.WorkflowStore`, `server.WorkflowScheduleStore`, and `Close() error`.
  - `func openToolStore(dsn string, backend databaseBackend, scope string) (tool.Store, error)`

- [ ] **Step 1: Write the failing detection/resolution test**

Create `cli/database_test.go`:

```go
package cli

import "testing"

func TestDetectBackend(t *testing.T) {
	cases := []struct {
		dsn  string
		want databaseBackend
	}{
		{"postgres://u:p@h:5432/db", backendPostgres},
		{"postgresql://u:p@h:5432/db", backendPostgres},
		{"/var/lib/petalflow/petalflow.db", backendSQLite},
		{"file:/tmp/x.db?cache=shared", backendSQLite},
		{"petalflow.db", backendSQLite},
	}
	for _, c := range cases {
		if got := detectBackend(c.dsn); got != c.want {
			t.Errorf("detectBackend(%q) = %q, want %q", c.dsn, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cli/ -run TestDetectBackend -v`
Expected: FAIL — `undefined: detectBackend`.

- [ ] **Step 3: Implement `cli/database.go`**

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/server"
	"github.com/petal-labs/petalflow/tool"
)

type databaseBackend string

const (
	backendSQLite   databaseBackend = "sqlite"
	backendPostgres databaseBackend = "postgres"
)

func detectBackend(dsn string) databaseBackend {
	l := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(l, "postgres://") || strings.HasPrefix(l, "postgresql://") {
		return backendPostgres
	}
	return backendSQLite
}

// resolveDatabaseDSN resolves the database DSN and backend from flags/env.
// Precedence: --database-dsn > PETALFLOW_DATABASE_DSN > --sqlite-path >
// PETALFLOW_SQLITE_PATH > PETALFLOW_TOOLS_STORE_PATH > default sqlite path.
func resolveDatabaseDSN(cmd *cobra.Command) (string, databaseBackend, string, error) {
	dsn := ""
	if cmd != nil {
		if v, _ := cmd.Flags().GetString("database-dsn"); strings.TrimSpace(v) != "" {
			dsn = strings.TrimSpace(v)
		}
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_DATABASE_DSN"))
	}
	if dsn == "" && cmd != nil {
		if v, _ := cmd.Flags().GetString("sqlite-path"); strings.TrimSpace(v) != "" {
			dsn = strings.TrimSpace(v)
		}
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_SQLITE_PATH"))
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_TOOLS_STORE_PATH"))
	}
	if dsn == "" {
		def, err := tool.DefaultSQLitePath()
		if err != nil {
			return "", "", "", fmt.Errorf("resolving default sqlite path: %w", err)
		}
		dsn = def
	}

	backend := detectBackend(dsn)
	scope := dsn
	if backend == backendSQLite && !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		clean := filepath.Clean(dsn)
		dsn, scope = clean, clean
	}
	return dsn, backend, scope, nil
}

// eventStoreCloser is a bus.EventStore that can be closed.
type eventStoreCloser interface {
	bus.EventStore
	io.Closer
}

func openEventStore(dsn string, backend databaseBackend) (eventStoreCloser, error) {
	switch backend {
	case backendPostgres:
		return bus.NewPostgresEventStore(bus.PostgresStoreConfig{DSN: dsn})
	default:
		return bus.NewSQLiteEventStore(bus.SQLiteStoreConfig{DSN: dsn})
	}
}

// workflowStore is the combined workflow + schedule store surface used by serve.
type workflowStore interface {
	server.WorkflowStore
	server.WorkflowScheduleStore
	io.Closer
}

func openWorkflowStore(dsn string, backend databaseBackend) (workflowStore, error) {
	switch backend {
	case backendPostgres:
		return server.NewPostgresStore(server.PostgresStoreConfig{DSN: dsn})
	default:
		return server.NewSQLiteStore(server.SQLiteStoreConfig{DSN: dsn})
	}
}

func openToolStore(dsn string, backend databaseBackend, scope string) (tool.Store, error) {
	switch backend {
	case backendPostgres:
		return tool.NewPostgresStore(tool.PostgresStoreConfig{DSN: dsn, Scope: scope})
	default:
		return tool.NewSQLiteStore(tool.SQLiteStoreConfig{DSN: dsn, Scope: scope})
	}
}
```

Confirm `*bus.SQLiteEventStore`, `*server.SQLiteStore`, `*tool.SQLiteStore` each already have a `Close() error` method (they do — verified in the SQLite sources) so they satisfy the closer interfaces.

- [ ] **Step 4: Run detection test**

Run: `go test ./cli/ -run TestDetectBackend -v`
Expected: PASS.

- [ ] **Step 5: Register the `--database-dsn` flag**

In `cli/serve.go` where flags are defined (near the other `serve` flags), add:

```go
cmd.Flags().String("database-dsn", "", "Database DSN. postgres:// or postgresql:// selects PostgreSQL; otherwise treated as a SQLite path/DSN (default: SQLite at ~/.petalflow/petalflow.db)")
```

Ensure `cli/tools.go` and `cli/run.go` command trees also expose `--database-dsn` where they currently expose `--store-path`/`--sqlite-path` (add the flag so `resolveDatabaseDSN` can read it; keep the old flags).

- [ ] **Step 6: Rewire `cli/serve.go` to use the factories**

Replace the body of `runServe` store construction (currently `resolveServeSQLiteDSN` + `tool.NewSQLiteStore` + `bus.NewSQLiteEventStore` + `server.NewSQLiteStore`) with:

```go
dsn, backend, scope, err := resolveDatabaseDSN(cmd)
if err != nil {
	return err
}

toolStore, err := openToolStore(dsn, backend, scope)
if err != nil {
	return fmt.Errorf("opening tool store: %w", err)
}
defer func() { _ = closeIfCloser(toolStore) }()

// ... daemon server, observability, config discovery unchanged ...

es, err := openEventStore(dsn, backend)
if err != nil {
	return fmt.Errorf("opening event store: %w", err)
}
defer func() { _ = es.Close() }()

workflowStore, err := openWorkflowStore(dsn, backend)
if err != nil {
	return fmt.Errorf("opening workflow store: %w", err)
}
defer func() { _ = workflowStore.Close() }()
```

`toolStore` is a `tool.Store` (no `Close` in the interface), so add a small helper in `cli/database.go`:

```go
func closeIfCloser(v any) error {
	if c, ok := v.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
```

Leave the rest of `runServe` (server config, scheduler, mux, signal handling) unchanged — `workflowStore` still satisfies both `Store` and `ScheduleStore` config fields. Delete the now-unused `resolveServeSQLiteDSN` (its logic moved into `resolveDatabaseDSN`).

- [ ] **Step 7: Rewire `cli/tools.go` `resolveToolStore`**

Replace its body with:

```go
func resolveToolStore(cmd *cobra.Command) (tool.Store, error) {
	dsn, backend, scope, err := resolveDatabaseDSN(cmd)
	if err != nil {
		return nil, err
	}
	return openToolStore(dsn, backend, scope)
}
```

Keep the existing `--store-path` flag readable by adding it into the `resolveDatabaseDSN` precedence chain if `--store-path` is the flag these commands actually register (check the command's flag set; if it uses `store-path`, add a `store-path` lookup alongside `sqlite-path` in `resolveDatabaseDSN`). Verify with `go test ./cli/`.

- [ ] **Step 8: Rewire `daemon/server.go` default store**

The default fallback currently calls `tool.NewDefaultSQLiteStore()`. Leave that as the zero-config default (SQLite at `~/.petalflow/petalflow.db`) — the daemon default has no cobra command to read flags from, and callers that want Postgres inject a `Store` via `ServerConfig.Store`. No behavioral change needed; add a one-line comment noting Postgres is selected by the caller injecting a store built from `openToolStore`.

- [ ] **Step 9: Build, vet, full test**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: all PASS, CGO-free (no build tags needed for the default suite).

- [ ] **Step 10: (Optional local) full integration run against Docker Postgres**

```bash
docker run --rm -d --name pf-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16
export PETALFLOW_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
go test -tags=integration ./bus/ ./server/ ./tool/ -run TestPostgres -v
# Smoke: start the daemon on Postgres
./petalflow serve --database-dsn "$PETALFLOW_TEST_POSTGRES_DSN" &
sleep 2 && curl -fsS localhost:8080/health && kill %1
docker rm -f pf-pg
```
Expected: integration tests PASS; `/health` returns OK with the daemon backed by Postgres.

- [ ] **Step 11: Commit**

```bash
git add cli/database.go cli/database_test.go cli/serve.go cli/tools.go cli/run.go daemon/server.go
git commit -m "feat(cli): select sqlite or postgres backend by dsn scheme"
```

---

## Self-Review

**Spec coverage:**
- §2 all four stores → Tasks 2, 3, 4. ✓
- §2 pgx stdlib mode → Task 2 Step 1 + blank imports in each store. ✓
- §2/§4 DSN auto-detect + precedence → Task 5 Steps 1–7. ✓
- §3.1 new files → mapped in File Structure + per-task Files. ✓
- §5 Rebind, SetMaxOpenConns(10), no PRAGMA/legacy → Tasks 2–4 mirror rules. ✓
- §5.1 schema translations (AUTOINCREMENT→IDENTITY, BLOB→BYTEA, TEXT timestamps) → Tasks 2/3/4 schema files. ✓
- §6.1 shared conformance suite → Tasks 2/3/4 Step "conformance". ✓
- §6.2 build-tagged integration tests gated on `PETALFLOW_TEST_POSTGRES_DSN` + Docker → Tasks 2/3/4 integration steps + Task 5 Step 10. ✓
- §7 fast-fail open, sentinel-error parity (23505), concurrency (no Scope mutex), CGO-free → Task 3 Step 4 sentinel mapping; Tasks 2–4 SetMaxOpenConns; Global Constraints. ✓
- §8 non-goals (no migration, no interface change, no repackage) → respected; SQLite files only referenced, never edited. ✓

**Placeholder scan:** No "TBD/TODO/handle edge cases" left; every code step has real code. The one genuinely conditional decision (`enabled` BOOLEAN vs SMALLINT) is spelled out with a concrete default and a named fallback, not deferred. ✓

**Type consistency:** `PostgresStoreConfig`/`NewPostgresStore` used consistently per package; `databaseBackend`, `detectBackend`, `resolveDatabaseDSN`, `openEventStore`/`openWorkflowStore`/`openToolStore`, `eventStoreCloser`/`workflowStore`/`closeIfCloser` names match between definition (Task 5 Step 3) and use (Task 5 Steps 6–7). `runEventStoreConformance`/`runWorkflowStoreConformance`/`runScheduleStoreConformance`/`runToolStoreConformance` match between definition and integration-test callers. ✓

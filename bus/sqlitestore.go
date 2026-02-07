package bus

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"

	_ "modernc.org/sqlite"
)

//go:embed sqlite_schema.sql
var sqliteSchema string

// SQLiteStoreConfig configures the SQLite event store.
type SQLiteStoreConfig struct {
	// DSN is the database connection string.
	DSN string

	// RetentionAge deletes events older than this duration (0 = no age pruning).
	RetentionAge time.Duration

	// RetentionCount keeps at most this many events per run (0 = no count pruning).
	RetentionCount int

	// PruneInterval is how often to run pruning (default 1 hour).
	PruneInterval time.Duration
}

// SQLiteEventStore persists events to a SQLite database.
// It satisfies the EventStore interface and supports WAL mode
// for concurrent read access and a background pruner goroutine.
type SQLiteEventStore struct {
	db   *sql.DB
	cfg  SQLiteStoreConfig
	stop chan struct{}
	done chan struct{}
}

// NewSQLiteEventStore opens (or creates) a SQLite event store.
func NewSQLiteEventStore(cfg SQLiteStoreConfig) (*SQLiteEventStore, error) {
	if cfg.PruneInterval == 0 {
		cfg.PruneInterval = time.Hour
	}

	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open: %w", err)
	}

	// Enable WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: set WAL mode: %w", err)
	}

	// Create schema.
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: create schema: %w", err)
	}

	s := &SQLiteEventStore{
		db:   db,
		cfg:  cfg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	// Start background pruner if any retention is configured.
	if cfg.RetentionAge > 0 || cfg.RetentionCount > 0 {
		go s.pruneLoop()
	} else {
		close(s.done)
	}

	return s, nil
}

// Append stores an event in the database.
func (s *SQLiteEventStore) Append(ctx context.Context, event runtime.Event) error {
	payload := event.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sqlitestore: marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO events (run_id, seq, kind, node_id, node_kind, time, attempt, elapsed, payload, trace_id, span_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.RunID,
		event.Seq,
		string(event.Kind),
		event.NodeID,
		string(event.NodeKind),
		event.Time.Format(time.RFC3339Nano),
		event.Attempt,
		int64(event.Elapsed),
		string(payloadJSON),
		event.TraceID,
		event.SpanID,
	)
	if err != nil {
		return fmt.Errorf("sqlitestore: append: %w", err)
	}
	return nil
}

// List returns events for a run, optionally filtered by afterSeq and limit.
func (s *SQLiteEventStore) List(ctx context.Context, runID string, afterSeq uint64, limit int) ([]runtime.Event, error) {
	var rows *sql.Rows
	var err error

	query := `SELECT run_id, seq, kind, node_id, node_kind, time, attempt, elapsed, payload, trace_id, span_id
	           FROM events WHERE run_id = ? AND seq > ? ORDER BY seq ASC`
	args := []any{runID, afterSeq}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err = s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: list: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// LatestSeq returns the highest Seq for a run (0 if no events).
func (s *SQLiteEventStore) LatestSeq(ctx context.Context, runID string) (uint64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM events WHERE run_id = ?`, runID,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("sqlitestore: latest seq: %w", err)
	}
	if !seq.Valid || seq.Int64 < 0 {
		return 0, nil
	}
	return uint64(seq.Int64), nil // #nosec G115 -- seq is always non-negative (auto-increment)
}

// RunIDs returns distinct run IDs from the store.
func (s *SQLiteEventStore) RunIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT run_id FROM events ORDER BY run_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: run ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlitestore: scan run id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Close stops the background pruner and closes the database connection.
func (s *SQLiteEventStore) Close() error {
	select {
	case <-s.stop:
		// Already closed.
	default:
		close(s.stop)
	}
	<-s.done
	return s.db.Close()
}

// Prune runs a single pruning pass. Exported for testing.
func (s *SQLiteEventStore) Prune(ctx context.Context) error {
	if s.cfg.RetentionAge > 0 {
		cutoff := time.Now().Add(-s.cfg.RetentionAge).Format(time.RFC3339Nano)
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM events WHERE time < ?`, cutoff,
		); err != nil {
			return fmt.Errorf("sqlitestore: prune by age: %w", err)
		}
	}

	if s.cfg.RetentionCount > 0 {
		// For each run, keep only the most recent RetentionCount events.
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT run_id FROM events`)
		if err != nil {
			return fmt.Errorf("sqlitestore: prune list runs: %w", err)
		}
		var runIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return fmt.Errorf("sqlitestore: prune scan run id: %w", err)
			}
			runIDs = append(runIDs, id)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("sqlitestore: prune rows err: %w", err)
		}

		for _, runID := range runIDs {
			if _, err := s.db.ExecContext(ctx,
				`DELETE FROM events WHERE run_id = ? AND id NOT IN (
					SELECT id FROM events WHERE run_id = ? ORDER BY seq DESC LIMIT ?
				)`, runID, runID, s.cfg.RetentionCount,
			); err != nil {
				return fmt.Errorf("sqlitestore: prune by count for %s: %w", runID, err)
			}
		}
	}

	return nil
}

func (s *SQLiteEventStore) pruneLoop() {
	defer close(s.done)

	ticker := time.NewTicker(s.cfg.PruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			_ = s.Prune(context.Background())
		}
	}
}

func scanEvents(rows *sql.Rows) ([]runtime.Event, error) {
	var events []runtime.Event
	for rows.Next() {
		var (
			e           runtime.Event
			kind        string
			nodeKind    string
			timeStr     string
			elapsedNano int64
			payloadJSON string
		)
		err := rows.Scan(
			&e.RunID,
			&e.Seq,
			&kind,
			&e.NodeID,
			&nodeKind,
			&timeStr,
			&e.Attempt,
			&elapsedNano,
			&payloadJSON,
			&e.TraceID,
			&e.SpanID,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlitestore: scan event: %w", err)
		}

		e.Kind = runtime.EventKind(kind)
		e.NodeKind = core.NodeKind(nodeKind)
		e.Elapsed = time.Duration(elapsedNano)

		t, err := time.Parse(time.RFC3339Nano, timeStr)
		if err != nil {
			return nil, fmt.Errorf("sqlitestore: parse time %q: %w", timeStr, err)
		}
		e.Time = t

		if payloadJSON != "" && payloadJSON != "{}" {
			if err := json.Unmarshal([]byte(payloadJSON), &e.Payload); err != nil {
				return nil, fmt.Errorf("sqlitestore: unmarshal payload: %w", err)
			}
		} else {
			e.Payload = map[string]any{}
		}

		events = append(events, e)
	}
	return events, rows.Err()
}

// Compile-time interface check.
var _ EventStore = (*SQLiteEventStore)(nil)

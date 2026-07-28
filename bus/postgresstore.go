package bus

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/internal/sqldialect"
	"github.com/petal-labs/petalflow/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed postgres_schema.sql
var postgresSchema string

// PostgresStoreConfig configures the PostgreSQL event store.
type PostgresStoreConfig struct {
	// DSN is the database connection string.
	DSN string

	// RetentionAge deletes events older than this duration (0 = no age pruning).
	RetentionAge time.Duration

	// RetentionCount keeps at most this many events per run (0 = no count pruning).
	RetentionCount int

	// PruneInterval is how often to run pruning (default 1 hour).
	PruneInterval time.Duration
}

// PostgresEventStore persists events to a PostgreSQL database.
// It satisfies the EventStore interface and supports a background
// pruner goroutine.
type PostgresEventStore struct {
	db   *sql.DB
	cfg  PostgresStoreConfig
	stop chan struct{}
	done chan struct{}
}

// NewPostgresEventStore opens (or creates) a PostgreSQL event store.
func NewPostgresEventStore(cfg PostgresStoreConfig) (*PostgresEventStore, error) {
	if cfg.PruneInterval == 0 {
		cfg.PruneInterval = time.Hour
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: open: %w", err)
	}
	db.SetMaxOpenConns(10)

	// Create schema.
	if _, err := db.Exec(postgresSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgresstore: create schema: %w", err)
	}

	s := &PostgresEventStore{
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
func (s *PostgresEventStore) Append(ctx context.Context, event runtime.Event) error {
	payload := event.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("postgresstore: marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		sqldialect.Rebind(`INSERT INTO events (run_id, seq, kind, node_id, node_kind, time, attempt, elapsed, payload, trace_id, span_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
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
		return fmt.Errorf("postgresstore: append: %w", err)
	}
	return nil
}

// List returns events for a run, optionally filtered by afterSeq and limit.
func (s *PostgresEventStore) List(ctx context.Context, runID string, afterSeq uint64, limit int) ([]runtime.Event, error) {
	var rows *sql.Rows
	var err error

	query := `SELECT run_id, seq, kind, node_id, node_kind, time, attempt, elapsed, payload, trace_id, span_id
	           FROM events WHERE run_id = ? AND seq > ? ORDER BY seq ASC`
	args := []any{runID, afterSeq}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err = s.db.QueryContext(ctx, sqldialect.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: list: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// LatestSeq returns the highest Seq for a run (0 if no events).
func (s *PostgresEventStore) LatestSeq(ctx context.Context, runID string) (uint64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		sqldialect.Rebind(`SELECT MAX(seq) FROM events WHERE run_id = ?`), runID,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("postgresstore: latest seq: %w", err)
	}
	if !seq.Valid || seq.Int64 < 0 {
		return 0, nil
	}
	return uint64(seq.Int64), nil // #nosec G115 -- seq is always non-negative (auto-increment)
}

// RunIDs returns distinct run IDs from the store.
func (s *PostgresEventStore) RunIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		sqldialect.Rebind(`SELECT DISTINCT run_id FROM events ORDER BY run_id`))
	if err != nil {
		return nil, fmt.Errorf("postgresstore: run ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgresstore: scan run id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Close stops the background pruner and closes the database connection.
func (s *PostgresEventStore) Close() error {
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
func (s *PostgresEventStore) Prune(ctx context.Context) error {
	if s.cfg.RetentionAge > 0 {
		cutoff := time.Now().Add(-s.cfg.RetentionAge).Format(time.RFC3339Nano)
		if _, err := s.db.ExecContext(ctx,
			sqldialect.Rebind(`DELETE FROM events WHERE time < ?`), cutoff,
		); err != nil {
			return fmt.Errorf("postgresstore: prune by age: %w", err)
		}
	}

	if s.cfg.RetentionCount > 0 {
		// For each run, keep only the most recent RetentionCount events.
		rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(`SELECT DISTINCT run_id FROM events`))
		if err != nil {
			return fmt.Errorf("postgresstore: prune list runs: %w", err)
		}
		var runIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return fmt.Errorf("postgresstore: prune scan run id: %w", err)
			}
			runIDs = append(runIDs, id)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgresstore: prune rows err: %w", err)
		}

		for _, runID := range runIDs {
			if _, err := s.db.ExecContext(ctx,
				sqldialect.Rebind(`DELETE FROM events WHERE run_id = ? AND id NOT IN (
					SELECT id FROM events WHERE run_id = ? ORDER BY seq DESC LIMIT ?
				)`), runID, runID, s.cfg.RetentionCount,
			); err != nil {
				return fmt.Errorf("postgresstore: prune by count for %s: %w", runID, err)
			}
		}
	}

	return nil
}

func (s *PostgresEventStore) pruneLoop() {
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

// Compile-time interface check.
var _ EventStore = (*PostgresEventStore)(nil)

package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/petal-labs/petalflow/internal/sqldialect"
)

const postgresRunSchema = `
CREATE TABLE IF NOT EXISTS run_records (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    payload JSONB NOT NULL,
    updated_at TEXT NOT NULL
);`

type PostgresRunStoreConfig struct {
	DSN string
}

// PostgresRunStore persists run records and uses row locking for cancellation
// and human-action updates shared by multiple workers.
type PostgresRunStore struct {
	db *sql.DB
}

func NewPostgresRunStore(cfg PostgresRunStoreConfig) (*PostgresRunStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("run store postgres dsn is required")
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("run postgres store open: %w", err)
	}
	if _, err := db.Exec(postgresRunSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run postgres store create schema: %w", err)
	}
	return &PostgresRunStore{db: db}, nil
}

func (s *PostgresRunStore) Create(ctx context.Context, record *RunRecord) error {
	if err := validateRunRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("run postgres store marshal: %w", err)
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, sqldialect.Rebind(`
INSERT INTO run_records (id, status, cancel_requested, payload, updated_at)
VALUES (?, ?, ?, ?, ?)`), record.ID, record.Status, record.CancelRequested, payload, updatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("run postgres store create: %w", err)
	}
	return nil
}

func (s *PostgresRunStore) Get(ctx context.Context, runID string) (*RunRecord, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, sqldialect.Rebind(`SELECT payload FROM run_records WHERE id = ?`), runID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("run postgres store get: %w", err)
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("run postgres store decode: %w", err)
	}
	return &record, nil
}

func (s *PostgresRunStore) Update(ctx context.Context, record *RunRecord) error {
	if err := validateRunRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("run postgres store marshal: %w", err)
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_records
SET status = ?, cancel_requested = ?, payload = ?, updated_at = ?
WHERE id = ? AND cancel_requested = FALSE`), record.Status, record.CancelRequested, payload, updatedAt.Format(time.RFC3339Nano), record.ID)
	if err != nil {
		return fmt.Errorf("run postgres store update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("run postgres store update affected rows: %w", err)
	}
	if affected == 0 {
		current, getErr := s.Get(ctx, record.ID)
		if errors.Is(getErr, ErrRunNotFound) {
			return ErrRunNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.CancelRequested {
			return nil
		}
	}
	return nil
}

func (s *PostgresRunStore) Cancel(ctx context.Context, runID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("run postgres store cancel begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var payload []byte
	var status string
	var requested bool
	err = tx.QueryRowContext(ctx, sqldialect.Rebind(`
SELECT status, cancel_requested, payload FROM run_records WHERE id = ? FOR UPDATE`), runID).Scan(&status, &requested, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("run postgres store cancel read: %w", err)
	}
	if requested || isTerminal(RunStatus(status)) {
		return ErrRunAlreadySettled
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return fmt.Errorf("run postgres store cancel decode: %w", err)
	}
	now := time.Now().UTC()
	record.Status, record.CancelRequested = RunStatusCanceled, true
	record.CompletedAt, record.UpdatedAt = now, now
	payload, err = json.Marshal(&record)
	if err != nil {
		return fmt.Errorf("run postgres store cancel marshal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_records SET status = ?, cancel_requested = TRUE, payload = ?, updated_at = ? WHERE id = ?`), RunStatusCanceled, payload, now.Format(time.RFC3339Nano), runID); err != nil {
		return fmt.Errorf("run postgres store cancel: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("run postgres store cancel commit: %w", err)
	}
	return nil
}

func (s *PostgresRunStore) CompletePendingAction(ctx context.Context, runID, actionID string, response any) (*RunRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("run postgres store complete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var payload []byte
	if err := tx.QueryRowContext(ctx, sqldialect.Rebind(`SELECT payload FROM run_records WHERE id = ? FOR UPDATE`), runID).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	} else if err != nil {
		return nil, fmt.Errorf("run postgres store complete read: %w", err)
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("run postgres store complete decode: %w", err)
	}
	if record.PendingAction == nil || record.PendingAction.ID != actionID {
		return nil, ErrPendingNotFound
	}
	if record.PendingAction.Response != nil {
		return nil, ErrPendingCompleted
	}
	now := time.Now().UTC()
	pending := *record.PendingAction
	pending.Response, pending.RespondedAt = response, now
	record.PendingAction, record.UpdatedAt = &pending, now
	payload, err = json.Marshal(&record)
	if err != nil {
		return nil, fmt.Errorf("run postgres store complete marshal: %w", err)
	}
	result, err := tx.ExecContext(ctx, sqldialect.Rebind(`UPDATE run_records SET payload = ?, updated_at = ? WHERE id = ? AND cancel_requested = FALSE`), payload, now.Format(time.RFC3339Nano), runID)
	if err != nil {
		return nil, fmt.Errorf("run postgres store complete: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, fmt.Errorf("run postgres store complete affected rows: %w", err)
	} else if affected == 0 {
		return nil, ErrRunAlreadySettled
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("run postgres store complete commit: %w", err)
	}
	return &record, nil
}

func (s *PostgresRunStore) Close() error { return s.db.Close() }

var _ RunStore = (*PostgresRunStore)(nil)

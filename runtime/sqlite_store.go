package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteRunSchema = `
CREATE TABLE IF NOT EXISTS run_records (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    payload BLOB NOT NULL,
    updated_at TEXT NOT NULL
);`

// SQLiteRunStoreConfig configures the durable run store.
type SQLiteRunStoreConfig struct {
	DSN string
}

// SQLiteRunStore persists complete run records as versioned JSON payloads.
// The status and cancellation columns are kept separately so cancellation can
// be made terminal atomically without inspecting or trusting the payload.
type SQLiteRunStore struct {
	db *sql.DB
}

func NewSQLiteRunStore(cfg SQLiteRunStoreConfig) (*SQLiteRunStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("run store sqlite dsn is required")
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("run sqlite store open: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run sqlite store set WAL mode: %w", err)
	}
	if _, err := db.Exec(sqliteRunSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run sqlite store create schema: %w", err)
	}
	return &SQLiteRunStore{db: db}, nil
}

func (s *SQLiteRunStore) Create(ctx context.Context, record *RunRecord) error {
	if err := validateRunRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("run sqlite store marshal: %w", err)
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_records (id, status, cancel_requested, payload, updated_at)
VALUES (?, ?, ?, ?, ?)`, record.ID, record.Status, boolInt(record.CancelRequested), payload, updatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("run sqlite store create: %w", err)
	}
	return nil
}

func (s *SQLiteRunStore) Get(ctx context.Context, runID string) (*RunRecord, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM run_records WHERE id = ?`, runID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("run sqlite store get: %w", err)
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("run sqlite store decode: %w", err)
	}
	return &record, nil
}

func (s *SQLiteRunStore) Update(ctx context.Context, record *RunRecord) error {
	if err := validateRunRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("run sqlite store marshal: %w", err)
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE run_records
SET status = ?, cancel_requested = ?, payload = ?, updated_at = ?
WHERE id = ? AND cancel_requested = 0`, record.Status, boolInt(record.CancelRequested), payload, updatedAt.Format(time.RFC3339Nano), record.ID)
	if err != nil {
		return fmt.Errorf("run sqlite store update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("run sqlite store update affected rows: %w", err)
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

func (s *SQLiteRunStore) Cancel(ctx context.Context, runID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("run sqlite store cancel begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var payload []byte
	var status string
	var cancelRequested int
	err = tx.QueryRowContext(ctx, `
SELECT status, cancel_requested, payload FROM run_records WHERE id = ?`, runID).Scan(&status, &cancelRequested, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("run sqlite store cancel read: %w", err)
	}
	if cancelRequested != 0 || isTerminal(RunStatus(status)) {
		return ErrRunAlreadySettled
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return fmt.Errorf("run sqlite store cancel decode: %w", err)
	}
	now := time.Now().UTC()
	record.Status = RunStatusCanceled
	record.CancelRequested = true
	record.CompletedAt = now
	record.UpdatedAt = now
	payload, err = json.Marshal(&record)
	if err != nil {
		return fmt.Errorf("run sqlite store cancel marshal: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE run_records
SET status = ?, cancel_requested = 1, payload = ?, updated_at = ?
WHERE id = ? AND cancel_requested = 0`, RunStatusCanceled, payload, now.Format(time.RFC3339Nano), runID)
	if err != nil {
		return fmt.Errorf("run sqlite store cancel: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("run sqlite store cancel affected rows: %w", err)
	}
	if affected == 0 {
		return ErrRunAlreadySettled
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("run sqlite store cancel commit: %w", err)
	}
	return nil
}

func (s *SQLiteRunStore) CompletePendingAction(ctx context.Context, runID, actionID string, response any) (*RunRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("run sqlite store complete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM run_records WHERE id = ?`, runID).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	} else if err != nil {
		return nil, fmt.Errorf("run sqlite store complete read: %w", err)
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("run sqlite store complete decode: %w", err)
	}
	if isTerminal(record.Status) {
		return nil, ErrRunAlreadySettled
	}
	if record.PendingAction == nil || record.PendingAction.ID != actionID {
		return nil, ErrPendingNotFound
	}
	if record.PendingAction.Completed || record.PendingAction.Response != nil {
		return nil, ErrPendingCompleted
	}
	now := time.Now().UTC()
	pending := *record.PendingAction
	pending.Response, pending.RespondedAt = response, now
	pending.Completed = true
	record.PendingAction, record.UpdatedAt = &pending, now
	payload, err = json.Marshal(&record)
	if err != nil {
		return nil, fmt.Errorf("run sqlite store complete marshal: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE run_records SET payload = ?, updated_at = ? WHERE id = ? AND cancel_requested = 0`, payload, now.Format(time.RFC3339Nano), runID)
	if err != nil {
		return nil, fmt.Errorf("run sqlite store complete: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, fmt.Errorf("run sqlite store complete affected rows: %w", err)
	} else if affected == 0 {
		return nil, ErrRunAlreadySettled
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("run sqlite store complete commit: %w", err)
	}
	return &record, nil
}

func (s *SQLiteRunStore) Close() error {
	return s.db.Close()
}

func validateRunRecord(record *RunRecord) error {
	if record == nil || strings.TrimSpace(record.ID) == "" {
		return errors.New("run record ID is required")
	}
	if record.Status == "" {
		return errors.New("run record status is required")
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var _ RunStore = (*SQLiteRunStore)(nil)

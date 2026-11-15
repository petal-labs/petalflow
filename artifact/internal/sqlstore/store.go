package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/internal/sqldialect"
)

const (
	DialectSQLite      = "sqlite"
	DialectPostgres    = "postgres"
	databaseTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"
)

type Config struct {
	MaxSizeBytes        int64
	AllowedContentTypes []string
	DefaultRetention    time.Duration
	Retention           artifact.RetentionPolicy
	PruneInterval       time.Duration
	Logger              *slog.Logger
}

type Store struct {
	db           *sql.DB
	dialect      string
	maxSize      int64
	allowedTypes map[string]struct{}
	retention    artifact.RetentionPolicy
	closeOnce    sync.Once
	closeErr     error
}

func New(db *sql.DB, dialect string, config Config) (*Store, error) {
	if db == nil {
		return nil, errors.New("artifact sql store: database is required")
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("artifact sql store: unsupported dialect %q", dialect)
	}
	allowed, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	if config.Retention.Default == 0 {
		config.Retention.Default = config.DefaultRetention
	}
	store := &Store{
		db: db, dialect: dialect, maxSize: config.MaxSizeBytes,
		allowedTypes: allowed, retention: config.Retention.Clone(),
	}
	// Reference-aware garbage collection is coordinated by the server. The
	// storage layer deliberately does not delete expiry candidates on its own.
	return store, nil
}

func (s *Store) Put(ctx context.Context, request artifact.PutRequest, content io.Reader) (artifact.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return artifact.Metadata{}, err
	}
	if err := request.Validate(); err != nil {
		return artifact.Metadata{}, fmt.Errorf("artifact sql put: %w", err)
	}
	if content == nil {
		return artifact.Metadata{}, errors.New("artifact sql put: content reader is required")
	}
	contentType, _ := normalizeContentType(request.ContentType)
	if !s.contentTypeAllowed(contentType) {
		return artifact.Metadata{}, fmt.Errorf("artifact sql put: %w: content type %q", artifact.ErrPolicyRejected, contentType)
	}
	data, err := readContent(content, s.maxSize)
	if err != nil {
		return artifact.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return artifact.Metadata{}, err
	}
	return s.putData(ctx, request, contentType, data)
}

func (s *Store) Get(ctx context.Context, scope artifact.Scope, reference artifact.Reference) (io.ReadCloser, error) {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return nil, fmt.Errorf("artifact sql get: %w", err)
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, s.query(`
SELECT b.content
FROM artifact_entries AS e
JOIN artifact_blobs AS b
  ON b.digest = e.digest AND b.size = e.size AND b.content_type = e.content_type
WHERE e.tenant_id = ? AND e.run_id = ? AND e.digest = ? AND e.size = ? AND e.content_type = ?`),
		scope.TenantID, scope.RunID, reference.Digest, reference.Size, reference.ContentType,
	).Scan(&data)
	if err != nil {
		return nil, classifyError("artifact sql get", err)
	}
	if !contentMatches(reference, data) {
		return nil, fmt.Errorf("artifact sql get: %w: stored content failed integrity check", artifact.ErrUnavailable)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}

func (s *Store) Stat(ctx context.Context, scope artifact.Scope, reference artifact.Reference) (artifact.Metadata, error) {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return artifact.Metadata{}, fmt.Errorf("artifact sql stat: %w", err)
	}
	row := s.db.QueryRowContext(ctx, s.query(metadataSelect+`
WHERE tenant_id = ? AND run_id = ? AND digest = ? AND size = ? AND content_type = ?`),
		scope.TenantID, scope.RunID, reference.Digest, reference.Size, reference.ContentType)
	metadata, err := scanMetadata(row)
	if err != nil {
		return artifact.Metadata{}, classifyError("artifact sql stat", err)
	}
	return metadata, nil
}

func (s *Store) Delete(ctx context.Context, scope artifact.Scope, reference artifact.Reference) error {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return fmt.Errorf("artifact sql delete: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return classifyError("artifact sql delete begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, s.query(`DELETE FROM artifact_entries
WHERE tenant_id = ? AND run_id = ? AND digest = ? AND size = ? AND content_type = ?`),
		scope.TenantID, scope.RunID, reference.Digest, reference.Size, reference.ContentType)
	if err != nil {
		return classifyError("artifact sql delete entry", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return classifyError("artifact sql delete result", err)
	}
	if affected == 0 {
		return fmt.Errorf("artifact sql delete: %w", artifact.ErrNotFound)
	}
	if err := s.deleteOrphan(ctx, tx, reference); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyError("artifact sql delete commit", err)
	}
	return nil
}

func (s *Store) ListByRun(ctx context.Context, scope artifact.Scope) ([]artifact.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("artifact sql list by run: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, s.query(metadataSelect+`
WHERE tenant_id = ? AND run_id = ?
ORDER BY created_at, digest, size, content_type`), scope.TenantID, scope.RunID)
	if err != nil {
		return nil, classifyError("artifact sql list by run", err)
	}
	defer rows.Close()
	items := make([]artifact.Metadata, 0)
	for rows.Next() {
		metadata, err := scanMetadata(rows)
		if err != nil {
			return nil, classifyError("artifact sql list by run scan", err)
		}
		items = append(items, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("artifact sql list by run rows", err)
	}
	return items, nil
}

// ListExpired returns a bounded deterministic snapshot of expiry candidates.
func (s *Store) ListExpired(ctx context.Context, before time.Time, tenantID string, offset, limit int) ([]artifact.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 || limit <= 0 || limit > 10_000 {
		return nil, errors.New("artifact sql list expired: offset must not be negative and limit must be between 1 and 10000")
	}
	if tenantID != "" {
		if err := (artifact.Scope{TenantID: tenantID, RunID: "gc"}).Validate(); err != nil {
			return nil, fmt.Errorf("artifact sql list expired: %w", err)
		}
	}
	query := metadataSelect + `
WHERE expires_at IS NOT NULL AND expires_at <= ?`
	args := []any{formatTime(before)}
	if tenantID != "" {
		query += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY expires_at, tenant_id, run_id, digest, size, content_type LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, s.query(query), args...)
	if err != nil {
		return nil, classifyError("artifact sql list expired", err)
	}
	defer rows.Close()
	items := make([]artifact.Metadata, 0)
	for rows.Next() {
		metadata, scanErr := scanMetadata(rows)
		if scanErr != nil {
			return nil, classifyError("artifact sql list expired scan", scanErr)
		}
		items = append(items, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("artifact sql list expired rows", err)
	}
	return items, nil
}

// DeleteExpired conditionally deletes one tenant-scoped candidate.
func (s *Store) DeleteExpired(ctx context.Context, metadata artifact.Metadata, before time.Time) (bool, error) {
	if err := validateLookup(ctx, metadata.Scope, metadata.Reference); err != nil {
		return false, fmt.Errorf("artifact sql delete expired: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyError("artifact sql delete expired begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, s.query(`DELETE FROM artifact_entries
WHERE tenant_id = ? AND run_id = ? AND digest = ? AND size = ? AND content_type = ?
  AND expires_at IS NOT NULL AND expires_at <= ?`), metadata.Scope.TenantID,
		metadata.Scope.RunID, metadata.Reference.Digest, metadata.Reference.Size,
		metadata.Reference.ContentType, formatTime(before))
	if err != nil {
		return false, classifyError("artifact sql delete expired entry", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, classifyError("artifact sql delete expired result", err)
	}
	if affected == 0 {
		return false, nil
	}
	if err := s.deleteOrphan(ctx, tx, metadata.Reference); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, classifyError("artifact sql delete expired commit", err)
	}
	return true, nil
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

const metadataSelect = `SELECT digest, size, content_type, tenant_id, run_id,
workflow_id, node_id, created_at, expires_at
FROM artifact_entries`

func (s *Store) putData(ctx context.Context, request artifact.PutRequest, contentType string, data []byte) (artifact.Metadata, error) {
	digest := sha256.Sum256(data)
	reference := artifact.Reference{Digest: hex.EncodeToString(digest[:]), Size: int64(len(data)), ContentType: contentType}
	createdAt := time.Now().UTC()
	expiresAt := request.ExpiresAt
	retention := s.retention.Resolve(request.Scope.TenantID, request.WorkflowID)
	if expiresAt.IsZero() && retention > 0 {
		expiresAt = createdAt.Add(retention)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact.Metadata{}, classifyError("artifact sql put begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.insertBlob(ctx, tx, reference, data, createdAt); err != nil {
		return artifact.Metadata{}, err
	}
	if err := s.insertEntry(ctx, tx, request, reference, createdAt, expiresAt); err != nil {
		return artifact.Metadata{}, err
	}
	row := tx.QueryRowContext(ctx, s.query(metadataSelect+`
WHERE tenant_id = ? AND run_id = ? AND digest = ? AND size = ? AND content_type = ?`),
		request.Scope.TenantID, request.Scope.RunID, reference.Digest, reference.Size, reference.ContentType)
	metadata, err := scanMetadata(row)
	if err != nil {
		return artifact.Metadata{}, classifyError("artifact sql put metadata", err)
	}
	if err := tx.Commit(); err != nil {
		return artifact.Metadata{}, classifyError("artifact sql put commit", err)
	}
	return metadata, nil
}

func (s *Store) insertBlob(ctx context.Context, tx *sql.Tx, reference artifact.Reference, data []byte, createdAt time.Time) error {
	_, err := tx.ExecContext(ctx, s.query(`INSERT INTO artifact_blobs
(digest, size, content_type, content, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (digest, size, content_type) DO NOTHING`),
		reference.Digest, reference.Size, reference.ContentType, data, formatTime(createdAt))
	if err != nil {
		return classifyError("artifact sql put blob", err)
	}
	return nil
}

func (s *Store) insertEntry(ctx context.Context, tx *sql.Tx, request artifact.PutRequest, reference artifact.Reference, createdAt, expiresAt time.Time) error {
	var expiry any
	if !expiresAt.IsZero() {
		expiry = formatTime(expiresAt)
	}
	_, err := tx.ExecContext(ctx, s.query(`INSERT INTO artifact_entries
(tenant_id, run_id, digest, size, content_type, workflow_id, node_id, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (tenant_id, run_id, digest, size, content_type) DO NOTHING`),
		request.Scope.TenantID, request.Scope.RunID, reference.Digest, reference.Size,
		reference.ContentType, request.WorkflowID, request.NodeID,
		formatTime(createdAt), expiry)
	if err != nil {
		return classifyError("artifact sql put entry", err)
	}
	return nil
}

func (s *Store) deleteOrphan(ctx context.Context, tx *sql.Tx, reference artifact.Reference) error {
	_, err := tx.ExecContext(ctx, s.query(`DELETE FROM artifact_blobs
WHERE digest = ? AND size = ? AND content_type = ?
  AND NOT EXISTS (
    SELECT 1 FROM artifact_entries AS e
    WHERE e.digest = artifact_blobs.digest
      AND e.size = artifact_blobs.size
      AND e.content_type = artifact_blobs.content_type
  )`), reference.Digest, reference.Size, reference.ContentType)
	if err != nil {
		return classifyError("artifact sql delete blob", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMetadata(row scanner) (artifact.Metadata, error) {
	var metadata artifact.Metadata
	var createdRaw, expiresRaw any
	err := row.Scan(
		&metadata.Reference.Digest, &metadata.Reference.Size, &metadata.Reference.ContentType,
		&metadata.Scope.TenantID, &metadata.Scope.RunID, &metadata.WorkflowID, &metadata.NodeID,
		&createdRaw, &expiresRaw,
	)
	if err != nil {
		return artifact.Metadata{}, err
	}
	metadata.CreatedAt, err = parseTime(createdRaw)
	if err != nil {
		return artifact.Metadata{}, fmt.Errorf("parse created_at: %w", err)
	}
	if expiresRaw != nil {
		metadata.ExpiresAt, err = parseTime(expiresRaw)
		if err != nil {
			return artifact.Metadata{}, fmt.Errorf("parse expires_at: %w", err)
		}
	}
	return metadata, nil
}

func parseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return time.Parse(time.RFC3339Nano, typed)
	case []byte:
		return time.Parse(time.RFC3339Nano, string(typed))
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", value)
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(databaseTimeFormat)
}

func validateConfig(config Config) (map[string]struct{}, error) {
	if config.MaxSizeBytes < 0 {
		return nil, errors.New("artifact sql store: max size must not be negative")
	}
	if config.DefaultRetention < 0 {
		return nil, errors.New("artifact sql store: default retention must not be negative")
	}
	if err := config.Retention.Validate(); err != nil {
		return nil, fmt.Errorf("artifact sql store: %w", err)
	}
	if config.PruneInterval < 0 {
		return nil, errors.New("artifact sql store: prune interval must not be negative")
	}
	allowed := make(map[string]struct{}, len(config.AllowedContentTypes))
	for _, value := range config.AllowedContentTypes {
		normalized, err := normalizeContentType(value)
		if err != nil {
			return nil, fmt.Errorf("artifact sql store: %w", err)
		}
		allowed[normalized] = struct{}{}
	}
	return allowed, nil
}

func normalizeContentType(value string) (string, error) {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") {
		return "", fmt.Errorf("invalid content type %q", value)
	}
	return mime.FormatMediaType(strings.ToLower(mediaType), params), nil
}

func readContent(content io.Reader, maxSize int64) ([]byte, error) {
	reader := content
	if maxSize > 0 && maxSize < math.MaxInt64 {
		reader = io.LimitReader(content, maxSize+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("artifact sql put: read content: %w", err)
	}
	if maxSize > 0 && int64(len(data)) > maxSize {
		return nil, fmt.Errorf("artifact sql put: %w: content exceeds %d bytes", artifact.ErrPolicyRejected, maxSize)
	}
	return data, nil
}

func validateLookup(ctx context.Context, scope artifact.Scope, reference artifact.Reference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	return reference.Validate()
}

func contentMatches(reference artifact.Reference, data []byte) bool {
	if int64(len(data)) != reference.Size {
		return false
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]) == reference.Digest
}

func (s *Store) contentTypeAllowed(contentType string) bool {
	if len(s.allowedTypes) == 0 {
		return true
	}
	_, ok := s.allowedTypes[contentType]
	return ok
}

func (s *Store) query(query string) string {
	if s.dialect == DialectPostgres {
		return sqldialect.Rebind(query)
	}
	return query
}

func classifyError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, artifact.ErrNotFound)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%s: %w: %v", operation, artifact.ErrUnavailable, err)
}

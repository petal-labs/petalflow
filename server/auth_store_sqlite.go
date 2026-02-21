package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const authSQLiteSchema = `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	email TEXT NOT NULL UNIQUE,
	name TEXT,
	password_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	token TEXT NOT NULL UNIQUE,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`

// AuthSQLiteStore persists user and session records in SQLite.
type AuthSQLiteStore struct {
	db *sql.DB
}

// NewAuthSQLiteStore creates a new SQLite-backed auth store using an existing database connection.
func NewAuthSQLiteStore(db *sql.DB) (*AuthSQLiteStore, error) {
	if db == nil {
		return nil, errors.New("auth sqlite store: db is nil")
	}

	if _, err := db.Exec(authSQLiteSchema); err != nil {
		return nil, fmt.Errorf("auth sqlite store create schema: %w", err)
	}

	return &AuthSQLiteStore{db: db}, nil
}

// CreateUser adds a new user record.
func (s *AuthSQLiteStore) CreateUser(ctx context.Context, rec UserRecord) error {
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO users (id, email, name, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		rec.ID,
		rec.Email,
		nullIfEmpty(rec.Name),
		rec.PasswordHash,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isAuthSQLiteUniqueViolation(err) {
			return ErrUserExists
		}
		return fmt.Errorf("auth sqlite store create user: %w", err)
	}
	return nil
}

// GetUserByEmail retrieves a user by email address.
func (s *AuthSQLiteStore) GetUserByEmail(ctx context.Context, email string) (UserRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, email, name, password_hash, created_at, updated_at
FROM users
WHERE email = ?`, strings.ToLower(email))

	rec, err := scanUserRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserRecord{}, false, nil
		}
		return UserRecord{}, false, err
	}
	return rec, true, nil
}

// GetUserByID retrieves a user by ID.
func (s *AuthSQLiteStore) GetUserByID(ctx context.Context, id string) (UserRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, email, name, password_hash, created_at, updated_at
FROM users
WHERE id = ?`, id)

	rec, err := scanUserRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserRecord{}, false, nil
		}
		return UserRecord{}, false, err
	}
	return rec, true, nil
}

// UpdateUser modifies an existing user record.
func (s *AuthSQLiteStore) UpdateUser(ctx context.Context, rec UserRecord) error {
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE users
SET email = ?, name = ?, password_hash = ?, updated_at = ?
WHERE id = ?`,
		strings.ToLower(rec.Email),
		nullIfEmpty(rec.Name),
		rec.PasswordHash,
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		rec.ID,
	)
	if err != nil {
		return fmt.Errorf("auth sqlite store update user: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth sqlite store update user affected rows: %w", err)
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// CreateSession creates a new session for a user.
func (s *AuthSQLiteStore) CreateSession(ctx context.Context, sess SessionRecord) error {
	now := time.Now().UTC()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (id, user_id, token, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)`,
		sess.ID,
		sess.UserID,
		sess.Token,
		sess.ExpiresAt.UTC().Format(time.RFC3339Nano),
		sess.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("auth sqlite store create session: %w", err)
	}
	return nil
}

// GetSessionByToken retrieves a session by token.
func (s *AuthSQLiteStore) GetSessionByToken(ctx context.Context, token string) (SessionRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, token, expires_at, created_at
FROM sessions
WHERE token = ?`, token)

	sess, err := scanSessionRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionRecord{}, false, nil
		}
		return SessionRecord{}, false, err
	}

	// Check if session is expired
	if sess.ExpiresAt.Before(time.Now().UTC()) {
		return SessionRecord{}, false, ErrSessionExpired
	}

	return sess, true, nil
}

// DeleteSession removes a session by ID.
func (s *AuthSQLiteStore) DeleteSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("auth sqlite store delete session: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth sqlite store delete session affected rows: %w", err)
	}
	if affected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// DeleteUserSessions removes all sessions for a user.
func (s *AuthSQLiteStore) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("auth sqlite store delete user sessions: %w", err)
	}
	return nil
}

// CleanExpiredSessions removes all expired sessions.
func (s *AuthSQLiteStore) CleanExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("auth sqlite store clean expired sessions: %w", err)
	}
	return nil
}

// Close is a no-op since we share the database connection.
func (s *AuthSQLiteStore) Close() error {
	return nil
}

type userScanner interface {
	Scan(dest ...any) error
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanUserRecord(scanner userScanner) (UserRecord, error) {
	var (
		id           string
		email        string
		name         sql.NullString
		passwordHash string
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(&id, &email, &name, &passwordHash, &createdAt, &updatedAt); err != nil {
		return UserRecord{}, err
	}

	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return UserRecord{}, fmt.Errorf("auth sqlite store parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return UserRecord{}, fmt.Errorf("auth sqlite store parse updated_at: %w", err)
	}

	return UserRecord{
		ID:           id,
		Email:        email,
		Name:         name.String,
		PasswordHash: passwordHash,
		CreatedAt:    created,
		UpdatedAt:    updated,
	}, nil
}

func scanSessionRecord(scanner sessionScanner) (SessionRecord, error) {
	var (
		id        string
		userID    string
		token     string
		expiresAt string
		createdAt string
	)
	if err := scanner.Scan(&id, &userID, &token, &expiresAt, &createdAt); err != nil {
		return SessionRecord{}, err
	}

	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("auth sqlite store parse expires_at: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("auth sqlite store parse created_at: %w", err)
	}

	return SessionRecord{
		ID:        id,
		UserID:    userID,
		Token:     token,
		ExpiresAt: expires,
		CreatedAt: created,
	}, nil
}

func isAuthSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: users.id") ||
		strings.Contains(msg, "UNIQUE constraint failed: users.email")
}

var _ AuthStore = (*AuthSQLiteStore)(nil)

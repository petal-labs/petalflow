package server

import (
	"context"
	"errors"
	"time"
)

// UserRecord represents a stored user account.
type UserRecord struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name,omitempty"`
	PasswordHash string    `json:"-"` // Never expose in JSON
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SessionRecord represents an active user session.
type SessionRecord struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Token     string    `json:"-"` // The actual token
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Sentinel errors for auth store operations.
var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserExists        = errors.New("user already exists")
	ErrSessionNotFound   = errors.New("session not found")
	ErrSessionExpired    = errors.New("session expired")
	ErrInvalidCredential = errors.New("invalid credentials")
)

// AuthStore defines the interface for user and session persistence.
type AuthStore interface {
	// CreateUser adds a new user record.
	CreateUser(ctx context.Context, rec UserRecord) error

	// GetUserByEmail retrieves a user by email address.
	GetUserByEmail(ctx context.Context, email string) (UserRecord, bool, error)

	// GetUserByID retrieves a user by ID.
	GetUserByID(ctx context.Context, id string) (UserRecord, bool, error)

	// UpdateUser modifies an existing user record.
	UpdateUser(ctx context.Context, rec UserRecord) error

	// CreateSession creates a new session for a user.
	CreateSession(ctx context.Context, sess SessionRecord) error

	// GetSession retrieves a session by token.
	GetSessionByToken(ctx context.Context, token string) (SessionRecord, bool, error)

	// DeleteSession removes a session by ID.
	DeleteSession(ctx context.Context, id string) error

	// DeleteUserSessions removes all sessions for a user.
	DeleteUserSessions(ctx context.Context, userID string) error

	// CleanExpiredSessions removes all expired sessions.
	CleanExpiredSessions(ctx context.Context) error
}

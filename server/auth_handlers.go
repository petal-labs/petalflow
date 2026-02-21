package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	// SessionDuration defines how long a session is valid.
	SessionDuration = 7 * 24 * time.Hour // 7 days

	// AuthCookieName is the name of the session cookie.
	AuthCookieName = "petalflow_session"
)

// LoginRequest is the JSON body for POST /api/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is the JSON response for POST /api/auth/login.
type LoginResponse struct {
	User  *UserResponse `json:"user"`
	Token string        `json:"token"`
}

// UserResponse is the public user data returned in auth responses.
type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// RegisterRequest is the JSON body for POST /api/auth/register.
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

// handleLogin authenticates a user and creates a session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.authStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "auth store not configured")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	// Validate required fields
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "email is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "password is required")
		return
	}

	// Get user by email
	user, ok, err := s.authStore.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		return
	}

	// Generate session token
	token, err := generateSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", "failed to generate session token")
		return
	}

	// Create session
	now := time.Now().UTC()
	sess := SessionRecord{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: now.Add(SessionDuration),
		CreatedAt: now,
	}

	if err := s.authStore.CreateSession(r.Context(), sess); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, LoginResponse{
		User: &UserResponse{
			ID:        user.ID,
			Email:     user.Email,
			Name:      user.Name,
			CreatedAt: user.CreatedAt,
		},
		Token: token,
	})
}

// handleLogout invalidates the current session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.authStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "auth store not configured")
		return
	}

	token := extractSessionToken(r)
	if token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Get session
	sess, ok, err := s.authStore.GetSessionByToken(r.Context(), token)
	if err != nil && !errors.Is(err, ErrSessionExpired) {
		s.logger.Warn("logout session lookup failed", "error", err)
	}

	// Delete session if found
	if ok {
		if err := s.authStore.DeleteSession(r.Context(), sess.ID); err != nil {
			s.logger.Warn("logout session delete failed", "error", err)
		}
	}

	// Clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current authenticated user.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.authStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "auth store not configured")
		return
	}

	token := extractSessionToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "no session token provided")
		return
	}

	// Get session
	sess, ok, err := s.authStore.GetSessionByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) {
			writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED", "session has expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session token")
		return
	}

	// Get user
	user, ok, err := s.authStore.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "user not found")
		return
	}

	writeJSON(w, http.StatusOK, UserResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt,
	})
}

// handleRegister creates a new user account.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.authStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "auth store not configured")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	// Validate required fields
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "email is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "password is required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "password must be at least 8 characters")
		return
	}

	// Check if user exists
	_, exists, err := s.authStore.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "USER_EXISTS", "a user with this email already exists")
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "HASH_ERROR", "failed to hash password")
		return
	}

	// Create user
	now := time.Now().UTC()
	user := UserRecord{
		ID:           uuid.New().String(),
		Email:        strings.ToLower(req.Email),
		Name:         req.Name,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.authStore.CreateUser(r.Context(), user); err != nil {
		if errors.Is(err, ErrUserExists) {
			writeError(w, http.StatusConflict, "USER_EXISTS", "a user with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Generate session token
	token, err := generateSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", "failed to generate session token")
		return
	}

	// Create session
	sess := SessionRecord{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: now.Add(SessionDuration),
		CreatedAt: now,
	}

	if err := s.authStore.CreateSession(r.Context(), sess); err != nil {
		s.logger.Warn("failed to create session after registration", "user_id", user.ID, "error", err)
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusCreated, LoginResponse{
		User: &UserResponse{
			ID:        user.ID,
			Email:     user.Email,
			Name:      user.Name,
			CreatedAt: user.CreatedAt,
		},
		Token: token,
	})
}

// extractSessionToken extracts the session token from the request.
// It checks the Authorization header first, then the cookie.
func extractSessionToken(r *http.Request) string {
	// Check Authorization header (Bearer token)
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Check cookie
	cookie, err := r.Cookie(AuthCookieName)
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}

	return ""
}

// generateSessionToken creates a cryptographically secure random token.
func generateSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

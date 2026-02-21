package server

import (
	"context"
	"errors"
	"time"
)

// ProviderType represents the LLM provider type.
type ProviderType string

const (
	ProviderTypeAnthropic ProviderType = "anthropic"
	ProviderTypeOpenAI    ProviderType = "openai"
	ProviderTypeGoogle    ProviderType = "google"
	ProviderTypeOllama    ProviderType = "ollama"
)

// ProviderStatus represents the connection status of a provider.
type ProviderStatus string

const (
	ProviderStatusConnected    ProviderStatus = "connected"
	ProviderStatusDisconnected ProviderStatus = "disconnected"
	ProviderStatusError        ProviderStatus = "error"
)

// ProviderRecord represents a stored LLM provider configuration.
type ProviderRecord struct {
	ID           string         `json:"id"`
	Type         ProviderType   `json:"type"`
	Name         string         `json:"name"`
	DefaultModel string         `json:"default_model,omitempty"`
	Status       ProviderStatus `json:"status,omitempty"`
	APIKeyHash   string         `json:"-"` // Never expose in JSON
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Sentinel errors for provider store operations.
var (
	ErrProviderNotFound = errors.New("provider not found")
	ErrProviderExists   = errors.New("provider already exists")
)

// ProviderStore defines the interface for provider persistence.
type ProviderStore interface {
	// List returns all provider records.
	List(ctx context.Context) ([]ProviderRecord, error)

	// Get retrieves a provider by ID.
	Get(ctx context.Context, id string) (ProviderRecord, bool, error)

	// Create adds a new provider record.
	Create(ctx context.Context, rec ProviderRecord) error

	// Update modifies an existing provider record.
	Update(ctx context.Context, rec ProviderRecord) error

	// Delete removes a provider by ID.
	Delete(ctx context.Context, id string) error

	// GetAPIKey retrieves the stored API key for a provider.
	// This is separate to avoid accidentally exposing keys.
	GetAPIKey(ctx context.Context, id string) (string, error)

	// SetAPIKey stores an API key for a provider.
	SetAPIKey(ctx context.Context, id string, apiKey string) error
}

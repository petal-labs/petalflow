package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// CreateProviderRequest is the JSON body for POST /api/providers.
type CreateProviderRequest struct {
	Type         ProviderType `json:"type"`
	Name         string       `json:"name"`
	DefaultModel string       `json:"default_model,omitempty"`
	APIKey       string       `json:"api_key,omitempty"`
}

// UpdateProviderRequest is the JSON body for PUT /api/providers/{id}.
type UpdateProviderRequest struct {
	Name         *string `json:"name,omitempty"`
	DefaultModel *string `json:"default_model,omitempty"`
	APIKey       *string `json:"api_key,omitempty"`
}

// TestProviderResponse is the response for POST /api/providers/{id}/test.
type TestProviderResponse struct {
	Success bool     `json:"success"`
	Models  []string `json:"models,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// handleListProviders returns all providers.
func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	records, err := s.providerStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Return empty array instead of null
	if records == nil {
		records = []ProviderRecord{}
	}

	writeJSON(w, http.StatusOK, records)
}

// handleGetProvider returns a single provider by ID.
func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	id := r.PathValue("id")
	rec, ok, err := s.providerStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleCreateProvider creates a new provider.
func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	var req CreateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	// Validate required fields
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "type is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}

	// Validate provider type
	switch req.Type {
	case ProviderTypeAnthropic, ProviderTypeOpenAI, ProviderTypeGoogle, ProviderTypeOllama:
		// valid
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			fmt.Sprintf("invalid provider type %q; must be one of: anthropic, openai, google, ollama", req.Type))
		return
	}

	now := time.Now().UTC()
	status := ProviderStatusDisconnected
	if req.APIKey != "" {
		status = ProviderStatusConnected
	}

	rec := ProviderRecord{
		ID:           uuid.New().String(),
		Type:         req.Type,
		Name:         req.Name,
		DefaultModel: req.DefaultModel,
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.providerStore.Create(r.Context(), rec); err != nil {
		if errors.Is(err, ErrProviderExists) {
			writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("provider %q already exists", rec.ID))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Keep runtime provider map aligned with configured provider types.
	s.ensureRuntimeProviderType(req.Type)
	if req.APIKey != "" {
		s.setRuntimeProviderAPIKey(req.Type, req.APIKey)
	}

	// Store API key separately if provided
	if req.APIKey != "" {
		if err := s.providerStore.SetAPIKey(r.Context(), rec.ID, req.APIKey); err != nil {
			s.logger.Warn("failed to store API key", "provider_id", rec.ID, "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, rec)
}

// handleUpdateProvider updates an existing provider.
func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	id := r.PathValue("id")
	rec, ok, err := s.providerStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", id))
		return
	}

	var req UpdateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	// Apply partial updates
	if req.Name != nil {
		rec.Name = *req.Name
	}
	if req.DefaultModel != nil {
		rec.DefaultModel = *req.DefaultModel
	}

	rec.UpdatedAt = time.Now().UTC()

	if err := s.providerStore.Update(r.Context(), rec); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	s.ensureRuntimeProviderType(rec.Type)

	// Update API key if provided
	if req.APIKey != nil {
		s.setRuntimeProviderAPIKey(rec.Type, *req.APIKey)
		if err := s.providerStore.SetAPIKey(r.Context(), rec.ID, *req.APIKey); err != nil {
			s.logger.Warn("failed to update API key", "provider_id", rec.ID, "error", err)
		}
		if *req.APIKey != "" {
			rec.Status = ProviderStatusConnected
		} else {
			rec.Status = ProviderStatusDisconnected
		}
	}

	writeJSON(w, http.StatusOK, rec)
}

// handleDeleteProvider deletes a provider by ID.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	id := r.PathValue("id")
	if err := s.providerStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestProvider tests connectivity to a provider.
func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	if s.providerStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "provider store not configured")
		return
	}

	id := r.PathValue("id")
	rec, ok, err := s.providerStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("provider %q not found", id))
		return
	}

	// Check if API key is configured
	apiKeyHash, err := s.providerStore.GetAPIKey(r.Context(), id)
	if err != nil && !errors.Is(err, ErrProviderNotFound) {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	resp := TestProviderResponse{
		Success: apiKeyHash != "",
	}

	if apiKeyHash == "" {
		resp.Error = "no API key configured"
	} else {
		// Return some default models based on provider type
		resp.Models = getDefaultModels(rec.Type)
	}

	writeJSON(w, http.StatusOK, resp)
}

// getDefaultModels returns default model names for a provider type.
func getDefaultModels(provType ProviderType) []string {
	switch provType {
	case ProviderTypeAnthropic:
		return []string{
			"claude-sonnet-4-20250514",
			"claude-opus-4-20250514",
			"claude-3-5-haiku-20241022",
			"claude-3-5-sonnet-20241022",
		}
	case ProviderTypeOpenAI:
		return []string{
			"gpt-4o",
			"gpt-4o-mini",
			"o1",
			"o1-mini",
			"o3-mini",
			"gpt-4-turbo",
		}
	case ProviderTypeGoogle:
		return []string{
			"gemini-2.0-flash",
			"gemini-2.0-pro",
			"gemini-1.5-pro",
			"gemini-1.5-flash",
		}
	case ProviderTypeOllama:
		return []string{
			"llama3.3",
			"llama3.2",
			"llama3.1",
			"mistral",
			"codellama",
			"qwen2.5",
		}
	default:
		return nil
	}
}

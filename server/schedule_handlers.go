package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type workflowScheduleRequest struct {
	Cron    string         `json:"cron,omitempty"`
	Enabled *bool          `json:"enabled,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Options *RunReqOptions `json:"options,omitempty"`
}

func (s *Server) handleListWorkflowSchedules(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	if s.scheduleStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "workflow schedules are not configured")
		return
	}
	if !s.workflowExists(r, workflowID, w) {
		return
	}

	schedules, err := s.scheduleStore.ListSchedules(r.Context(), workflowID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if s.security.RequireAuth {
		filtered := schedules[:0]
		for _, schedule := range schedules {
			if s.ownsTenant(r, schedule.TenantID) {
				filtered = append(filtered, schedule)
			}
		}
		schedules = filtered
	}
	writeJSON(w, http.StatusOK, schedules)
}

func (s *Server) handleCreateWorkflowSchedule(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	if s.scheduleStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "workflow schedules are not configured")
		return
	}
	if !s.workflowExists(r, workflowID, w) {
		return
	}

	var req workflowScheduleRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	now := time.Now().UTC()
	schedule := WorkflowSchedule{
		ID:         uuid.NewString(),
		TenantID:   s.tenantID(r),
		WorkflowID: workflowID,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	updated, err := applyScheduleRequest(schedule, req, true, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE", err.Error())
		return
	}

	if err := s.scheduleStore.CreateSchedule(r.Context(), updated); err != nil {
		if errors.Is(err, ErrWorkflowScheduleExists) {
			writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("schedule %q already exists", updated.ID))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

func (s *Server) handleGetWorkflowSchedule(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	if s.scheduleStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "workflow schedules are not configured")
		return
	}
	if !s.workflowExists(r, workflowID, w) {
		return
	}

	schedule, found, err := s.scheduleStore.GetSchedule(r.Context(), workflowID, scheduleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("schedule %q not found", scheduleID))
		return
	}
	if !s.ownsTenant(r, schedule.TenantID) {
		writeHiddenResource(w)
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) handleUpdateWorkflowSchedule(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	if s.scheduleStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "workflow schedules are not configured")
		return
	}
	if !s.workflowExists(r, workflowID, w) {
		return
	}

	existing, found, err := s.scheduleStore.GetSchedule(r.Context(), workflowID, scheduleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("schedule %q not found", scheduleID))
		return
	}
	if !s.ownsTenant(r, existing.TenantID) {
		writeHiddenResource(w)
		return
	}

	var req workflowScheduleRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	now := time.Now().UTC()
	next, err := applyScheduleRequest(existing, req, false, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE", err.Error())
		return
	}
	next.UpdatedAt = now

	if err := s.scheduleStore.UpdateSchedule(r.Context(), next); err != nil {
		if errors.Is(err, ErrWorkflowScheduleNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("schedule %q not found", scheduleID))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Server) handleDeleteWorkflowSchedule(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	if s.scheduleStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "workflow schedules are not configured")
		return
	}
	if !s.workflowExists(r, workflowID, w) {
		return
	}
	existing, found, err := s.scheduleStore.GetSchedule(r.Context(), workflowID, scheduleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !found || !s.ownsTenant(r, existing.TenantID) {
		writeHiddenResource(w)
		return
	}

	if err := s.scheduleStore.DeleteSchedule(r.Context(), workflowID, scheduleID); err != nil {
		if errors.Is(err, ErrWorkflowScheduleNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("schedule %q not found", scheduleID))
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workflowExists(r *http.Request, workflowID string, w http.ResponseWriter) bool {
	record, found, err := s.store.Get(r.Context(), workflowID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return false
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", workflowID))
		return false
	}
	if !s.ownsTenant(r, record.TenantID) {
		writeHiddenResource(w)
		return false
	}
	return true
}

func applyScheduleRequest(base WorkflowSchedule, req workflowScheduleRequest, creating bool, now time.Time) (WorkflowSchedule, error) {
	currentCron := base.Cron
	wasEnabled := base.Enabled

	if cleanCron := strings.TrimSpace(req.Cron); cleanCron != "" {
		base.Cron = cleanCron
	}
	if req.Enabled != nil {
		base.Enabled = *req.Enabled
	}
	if req.Input != nil {
		base.Input = req.Input
	}
	if req.Options != nil {
		base.Options = *req.Options
	}

	if strings.TrimSpace(base.Cron) == "" {
		return WorkflowSchedule{}, fmt.Errorf("cron is required")
	}
	if base.Options.Stream {
		return WorkflowSchedule{}, fmt.Errorf("options.stream is not supported for scheduled runs")
	}
	if strings.TrimSpace(base.Options.Timeout) != "" {
		if _, err := time.ParseDuration(base.Options.Timeout); err != nil {
			return WorkflowSchedule{}, fmt.Errorf("options.timeout: %w", err)
		}
	}
	if _, err := buildRunHumanHandler(base.Options.Human); err != nil {
		return WorkflowSchedule{}, err
	}
	if _, err := parseCronExpressionUTC(base.Cron); err != nil {
		return WorkflowSchedule{}, err
	}

	cronChanged := strings.TrimSpace(currentCron) != "" && currentCron != base.Cron
	if base.Enabled && (creating || cronChanged || (!wasEnabled && base.Enabled) || base.NextRunAt.IsZero()) {
		nextRunAt, err := nextCronRunUTC(base.Cron, now.UTC())
		if err != nil {
			return WorkflowSchedule{}, err
		}
		base.NextRunAt = nextRunAt
	}

	return base, nil
}

func decodeJSONBody(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}

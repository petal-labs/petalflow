package server

import (
	"strings"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

func (s *Server) markRunActive(runID string) {
	id := strings.TrimSpace(runID)
	if id == "" {
		return
	}
	s.activeRunsMu.Lock()
	s.activeRuns[id] = struct{}{}
	s.activeRunsMu.Unlock()
}

func (s *Server) markRunInactive(runID string) {
	id := strings.TrimSpace(runID)
	if id == "" {
		return
	}
	s.activeRunsMu.Lock()
	delete(s.activeRuns, id)
	s.activeRunsMu.Unlock()
}

func (s *Server) isRunActive(runID string) bool {
	id := strings.TrimSpace(runID)
	if id == "" {
		return false
	}
	s.activeRunsMu.RLock()
	_, ok := s.activeRuns[id]
	s.activeRunsMu.RUnlock()
	return ok
}

func (s *Server) reconcileRunSummary(summary RunHistoryResponse, events []runtime.Event) RunHistoryResponse {
	if !strings.EqualFold(strings.TrimSpace(summary.Status), "running") {
		return summary
	}
	if s.isRunActive(summary.RunID) {
		return summary
	}

	summary.Status = "failed"
	if summary.CompletedAt == nil {
		last := latestEventTime(events)
		if last.IsZero() {
			last = summary.StartedAt
		}
		completedAt := last.UTC()
		summary.CompletedAt = &completedAt
	}
	if summary.DurationMs == 0 && summary.CompletedAt != nil {
		if delta := summary.CompletedAt.Sub(summary.StartedAt); delta > 0 {
			summary.DurationMs = delta.Milliseconds()
		}
	}
	return summary
}

func latestEventTime(events []runtime.Event) time.Time {
	var latest time.Time
	for _, event := range events {
		if event.Time.After(latest) {
			latest = event.Time
		}
	}
	return latest
}

package server

import (
	"testing"
	"time"
)

func TestParseCronExpressionUTC_Valid(t *testing.T) {
	schedule, err := parseCronExpressionUTC("*/5 * * * *")
	if err != nil {
		t.Fatalf("parseCronExpressionUTC error: %v", err)
	}

	next := schedule.Next(time.Date(2026, 2, 20, 10, 2, 0, 0, time.UTC))
	want := time.Date(2026, 2, 20, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next=%s, want=%s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestParseCronExpressionUTC_RejectsTimezonePrefixes(t *testing.T) {
	for _, expr := range []string{
		"CRON_TZ=America/Los_Angeles * * * * *",
		"TZ=UTC * * * * *",
	} {
		if _, err := parseCronExpressionUTC(expr); err == nil {
			t.Fatalf("parseCronExpressionUTC(%q) expected error", expr)
		}
	}
}

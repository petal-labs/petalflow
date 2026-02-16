package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var standardCronParser = cron.NewParser(
	cron.Minute |
		cron.Hour |
		cron.Dom |
		cron.Month |
		cron.Dow,
)

func nextCronRunUTC(expr string, now time.Time) (time.Time, error) {
	schedule, err := parseCronExpressionUTC(expr)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(now.UTC()), nil
}

func parseCronExpressionUTC(expr string) (cron.Schedule, error) {
	clean := strings.TrimSpace(expr)
	if clean == "" {
		return nil, fmt.Errorf("cron expression is required")
	}

	upper := strings.ToUpper(clean)
	if strings.Contains(upper, "CRON_TZ=") || strings.Contains(upper, "TZ=") {
		return nil, fmt.Errorf("cron expression must be UTC-only (timezone prefixes are not allowed)")
	}

	schedule, err := standardCronParser.Parse(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	return schedule, nil
}

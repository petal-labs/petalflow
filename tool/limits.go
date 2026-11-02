package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Invocation concurrency/rate limit errors on the invocation path.
var (
	// ErrToolConcurrencyLimit marks a rejection because the configured
	// concurrency budget for the tool or invocation scope is saturated.
	ErrToolConcurrencyLimit = fmt.Errorf("tool: concurrency limit reached")
	// ErrToolRateLimited marks a rejection because the tool's invocation
	// rate budget is exhausted for the current window.
	ErrToolRateLimited = fmt.Errorf("tool: invocation rate limit reached")
)

// Invocation limit error codes.
const (
	// ToolErrorCodeConcurrencyLimit is returned when the configured
	// concurrency budget is saturated.
	ToolErrorCodeConcurrencyLimit = "TOOL_CONCURRENCY_LIMIT"
	// ToolErrorCodeRateLimited is returned when the invocation rate
	// budget is exhausted for the current window.
	ToolErrorCodeRateLimited = "TOOL_RATE_LIMITED"
)

// InvocationLimits bounds how many tool invocations may run concurrently
// and how quickly they may be issued. Zero/negative values mean
// unbounded for that dimension; nil limits mean fully unlimited so
// embedded deployments stay unfenced until they opt in.
//
// Limits are enforced at the invocation gate before adapters engage, so
// retries and parallel branches are bounded as well: every attempt
// counts against the rate budget and only the configured concurrency
// runs at once.
type InvocationLimits struct {
	// MaxConcurrentPerTool caps in-flight invocations per tool name;
	// ToolConcurrency overrides for specific tools.
	MaxConcurrentPerTool int
	ToolConcurrency      map[string]int
	// MaxPerMinutePerTool caps dispatch starts per tool per rolling
	// minute; ToolRatePerMinute overrides for specific tools.
	MaxPerMinutePerTool int
	ToolRatePerMinute   map[string]int
	// MaxConcurrentPerScope caps in-flight invocations per invocation
	// scope (a workflow run, or the daemon's default scope). This is
	// the workflow-facing concurrency budget.
	MaxConcurrentPerScope int
}

func (l InvocationLimits) concurrencyFor(toolName string) int {
	if override, ok := l.ToolConcurrency[toolName]; ok {
		return override
	}
	return l.MaxConcurrentPerTool
}

func (l InvocationLimits) rateFor(toolName string) int {
	if override, ok := l.ToolRatePerMinute[toolName]; ok {
		return override
	}
	return l.MaxPerMinutePerTool
}

// Validate rejects malformed limits (negative ceilings).
func (l InvocationLimits) Validate() error {
	checks := []struct {
		name  string
		value int
	}{
		{"max_concurrent_per_tool", l.MaxConcurrentPerTool},
		{"max_per_minute_per_tool", l.MaxPerMinutePerTool},
		{"max_concurrent_per_scope", l.MaxConcurrentPerScope},
	}
	for _, check := range checks {
		if check.value < 0 {
			return fmt.Errorf("tool invocation limit %s is negative", check.name)
		}
	}
	for name, value := range l.ToolConcurrency {
		if value < 0 {
			return fmt.Errorf("tool invocation limit tool_concurrency[%s] is negative", name)
		}
	}
	for name, value := range l.ToolRatePerMinute {
		if value < 0 {
			return fmt.Errorf("tool invocation limit tool_rate_per_minute[%s] is negative", name)
		}
	}
	return nil
}

const rateWindow = time.Minute

// invocationGovernor enforces InvocationLimits at the invocation gate.
// Concurrency slots are checked-then-incurred under one lock so the
// budget cannot be bypassed by parallel branches; rate budgets are
// counted with a rolling window so retries count too.
type invocationGovernor struct {
	limits InvocationLimits

	mu             sync.Mutex
	active         map[string]int
	rateStarts     map[string][]time.Time
	activeSessions map[string]int
}

func newInvocationGovernor(limits *InvocationLimits) *invocationGovernor {
	if limits == nil {
		return nil
	}
	return &invocationGovernor{
		limits:         *limits,
		active:         make(map[string]int),
		rateStarts:     make(map[string][]time.Time),
		activeSessions: make(map[string]int),
	}
}

// invocationScopeKey identifies the concurrency scope of an invocation
// context (run-scope for workflow runs, daemon default otherwise).
type invocationScopeKey struct{}

// ContextWithInvocationScope tags an invocation context with its scope
// id (typically the workflow run ID). Scope ids bound the
// per-workflow concurrency budget.
func ContextWithInvocationScope(ctx context.Context, scopeID string) context.Context {
	return context.WithValue(ctx, invocationScopeKey{}, scopeID)
}

// InvocationScopeFromContext returns the scope id, or the shared daemon
// scope when none is set.
func InvocationScopeFromContext(ctx context.Context) string {
	if scope, ok := ctx.Value(invocationScopeKey{}).(string); ok && scope != "" {
		return "run:" + scope
	}
	return "scope:default"
}

// acquire checks and holds the invocation budgets; the returned release
// function must always be called. Errors are typed:
// ErrToolRateLimited / ErrToolConcurrencyLimit.
func (g *invocationGovernor) acquire(toolName string, ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()

	// Rate budget for the tool.
	if limit := g.limits.rateFor(toolName); limit > 0 {
		key := "rate:tool:" + toolName
		starts := pruneWindow(g.rateStarts[key], now)
		if len(starts) >= limit {
			g.rateStarts[key] = starts
			return nil, fmt.Errorf("%w: tool %q issued %d invocations in the last minute (limit %d)", ErrToolRateLimited, toolName, len(starts), limit)
		}
		starts = append(starts, now)
		g.rateStarts[key] = starts
	}

	// Concurrency for the tool.
	if limit := g.limits.concurrencyFor(toolName); limit > 0 {
		key := "concurrency:tool:" + toolName
		if g.active[key] >= limit {
			return nil, fmt.Errorf("%w: tool %q has %d in-flight invocations (limit %d)", ErrToolConcurrencyLimit, toolName, g.active[key], limit)
		}
		g.active[key]++
		releaseKeys := []string{key}

		// Concurrency for the invocation scope (per workflow run).
		if scopeLimit := g.limits.MaxConcurrentPerScope; scopeLimit > 0 {
			scopeKey := "concurrency:" + InvocationScopeFromContext(ctx)
			if g.active[scopeKey] >= scopeLimit {
				return nil, fmt.Errorf("%w: scope has %d in-flight invocations (limit %d)", ErrToolConcurrencyLimit, g.active[scopeKey], scopeLimit)
			}
			g.active[scopeKey]++
			releaseKeys = append(releaseKeys, scopeKey)
		}

		return func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			for _, key := range releaseKeys {
				if g.active[key] > 0 {
					g.active[key]--
				}
				if g.active[key] == 0 {
					delete(g.active, key)
				}
			}
		}, nil
	}

	// No tool concurrency limit: the scope limit still applies alone.
	if scopeLimit := g.limits.MaxConcurrentPerScope; scopeLimit > 0 {
		scopeKey := "concurrency:" + InvocationScopeFromContext(ctx)
		if g.active[scopeKey] >= scopeLimit {
			return nil, fmt.Errorf("%w: scope has %d in-flight invocations (limit %d)", ErrToolConcurrencyLimit, g.active[scopeKey], scopeLimit)
		}
		g.active[scopeKey]++
		scopeOnly := scopeKey
		return func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.active[scopeOnly] > 0 {
				g.active[scopeOnly]--
			}
			if g.active[scopeOnly] == 0 {
				delete(g.active, scopeOnly)
			}
		}, nil
	}

	return func() {}, nil
}

func pruneWindow(starts []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-rateWindow)
	kept := starts[:0]
	for _, start := range starts {
		if start.After(cutoff) {
			kept = append(kept, start)
		}
	}
	return kept
}

// LimitErrorCode maps an invocation-limits rejection to its typed code.
func LimitErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrToolRateLimited):
		return ToolErrorCodeRateLimited
	case errors.Is(err, ErrToolConcurrencyLimit):
		return ToolErrorCodeConcurrencyLimit
	default:
		return ""
	}
}

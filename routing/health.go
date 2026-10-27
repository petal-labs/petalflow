package routing

import (
	"strings"
	"sync"
	"time"
)

// ModelTarget is one entry in a fallback chain.
type ModelTarget struct {
	// Provider is the provider name (must be resolvable by the factory).
	Provider string
	// Model is the model identifier on that provider.
	Model string
	// MaxAttempts bounds retries within this target per request before
	// falling through to the next target (default: 1). Negative values are
	// malformed and clamped to the default with a warning.
	MaxAttempts int
}

// HealthConfig configures health-aware routing behavior.
type HealthConfig struct {
	// Cooldown is the base cooldown applied after the first failure.
	// Zero defaults to 30s; negative values are malformed and clamp to the
	// default.
	Cooldown time.Duration

	// MaxCooldown bounds the exponential growth of cooldowns.
	// Zero defaults to 5m; negative values are malformed and clamp.
	MaxCooldown time.Duration
}

// Default cooldown values.
const (
	defaultBaseCooldown = 30 * time.Second
	defaultMaxCooldown  = 5 * time.Minute
)

// baseCooldownOrDefault resolves the configured base cooldown.
func (c HealthConfig) baseCooldownOrDefault() time.Duration {
	if c.Cooldown < 0 {
		return defaultBaseCooldown
	}
	if c.Cooldown == 0 {
		return defaultBaseCooldown
	}
	return c.Cooldown
}

// maxCooldownOrDefault resolves the configured cooldown ceiling.
func (c HealthConfig) maxCooldownOrDefault() time.Duration {
	if c.MaxCooldown < 0 {
		return defaultMaxCooldown
	}
	if c.MaxCooldown == 0 {
		return defaultMaxCooldown
	}
	return c.MaxCooldown
}

// HealthTracker records recent model outcomes and marks degraded
// provider/model pairs as unhealthy for a bounded, exponentially growing
// cooldown. Recovery is by cooldown expiry (a probe), never permanent
// blacklisting. Safe for concurrent use.
type HealthTracker struct {
	mu     sync.Mutex
	models map[string]*healthState
	base   time.Duration
	max    time.Duration
	// Now provides the current time (injectable for tests).
	Now func() time.Time
}

type healthState struct {
	consecutiveFailures int
	cooldownUntil       time.Time
}

// NewHealthTracker creates a tracker with the given configuration.
func NewHealthTracker(cfg HealthConfig) *HealthTracker {
	return &HealthTracker{
		models: make(map[string]*healthState),
		base:   cfg.baseCooldownOrDefault(),
		max:    cfg.maxCooldownOrDefault(),
		Now:    time.Now,
	}
}

// key normalizes a provider/model pair.
func healthKey(provider, model string) string {
	return strings.ToLower(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}

// RecordSuccess resets the failure streak for a model.
func (t *HealthTracker) RecordSuccess(provider, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.models, healthKey(provider, model))
}

// RecordFailure registers a failure and starts (or extends) the cooldown.
func (t *HealthTracker) RecordFailure(provider, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := healthKey(provider, model)
	state := t.models[key]
	if state == nil {
		state = &healthState{}
		t.models[key] = state
	}
	state.consecutiveFailures++

	now := t.Now()
	cooldown := t.base
	// Exponential growth capped at the ceiling.
	for i := 1; i < state.consecutiveFailures && cooldown < t.max; i++ {
		cooldown *= 2
	}
	if cooldown > t.max {
		cooldown = t.max
	}
	state.cooldownUntil = now.Add(cooldown)
}

// Available reports whether a model is routable (not in cooldown).
func (t *HealthTracker) Available(provider, model string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.models[healthKey(provider, model)]
	if state == nil {
		return true
	}
	return !t.Now().Before(state.cooldownUntil)
}

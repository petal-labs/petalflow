package routing

import (
	"testing"
	"time"
)

func TestHealthTrackerCooldownAndRecovery(t *testing.T) {
	now := time.Now()
	tracker := NewHealthTracker(HealthConfig{})
	tracker.Now = func() time.Time { return now }

	if !tracker.Available("openai", "gpt-4o") {
		t.Fatal("fresh model should be available")
	}

	// First failure: 30s cooldown.
	tracker.RecordFailure("openai", "gpt-4o")
	if tracker.Available("openai", "gpt-4o") {
		t.Fatal("model should be in cooldown after failure")
	}

	now = now.Add(31 * time.Second)
	if !tracker.Available("openai", "gpt-4o") {
		t.Fatal("cooldown should expire (recovery probing)")
	}

	// Success resets the streak.
	tracker.RecordSuccess("openai", "gpt-4o")
	now = now.Add(1 * time.Second)
	if !tracker.Available("openai", "gpt-4o") {
		t.Error("success should clear cooldown state")
	}
}

func TestHealthTrackerExponentialBackoffWithCap(t *testing.T) {
	now := time.Now()
	tracker := NewHealthTracker(HealthConfig{Cooldown: 10 * time.Second, MaxCooldown: 40 * time.Second})
	tracker.Now = func() time.Time { return now }

	// Consecutive failures double the cooldown up to the cap: 10, 20, 40, 40...
	remaining := func() time.Duration {
		tracker.RecordFailure("p", "m")
		state := tracker.models[healthKey("p", "m")]
		return state.cooldownUntil.Sub(now)
	}

	if got := remaining(); got != 10*time.Second {
		t.Errorf("first cooldown = %v, want 10s", got)
	}
	if got := remaining(); got != 20*time.Second {
		t.Errorf("second cooldown = %v, want 20s", got)
	}
	if got := remaining(); got != 40*time.Second {
		t.Errorf("third cooldown = %v, want 40s (cap)", got)
	}
	if got := remaining(); got != 40*time.Second {
		t.Errorf("fourth cooldown = %v, want 40s (cap)", got)
	}
}

func TestHealthTrackerMalformedConfigClamps(t *testing.T) {
	tracker := NewHealthTracker(HealthConfig{Cooldown: -5, MaxCooldown: -100})
	if tracker.base != defaultBaseCooldown || tracker.max != defaultMaxCooldown {
		t.Errorf("negative config should clamp to defaults: base=%v max=%v", tracker.base, tracker.max)
	}
}

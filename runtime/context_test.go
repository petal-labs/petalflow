package runtime

import (
	"context"
	"testing"
)

func TestContextWithEmitter_RoundTrip(t *testing.T) {
	var called bool
	emitter := EventEmitter(func(e Event) { called = true })

	ctx := ContextWithEmitter(context.Background(), emitter)
	got := EmitterFromContext(ctx)

	got(Event{})
	if !called {
		t.Error("emitter from context was not the one we stored")
	}
}

func TestEmitterFromContext_NoEmitter(t *testing.T) {
	got := EmitterFromContext(context.Background())
	// Should return a no-op that doesn't panic
	got(Event{}) // should not panic
}

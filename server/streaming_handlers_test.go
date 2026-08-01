package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamWithoutSubscription_ReturnsOnContextCancel(t *testing.T) {
	rec := httptest.NewRecorder()
	writer, ok := newSSEWriter(rec)
	if !ok {
		t.Fatal("httptest recorder should support flushing")
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error) // never fires; the run never completes

	returned := make(chan struct{})
	go func() {
		(&Server{}).streamWithoutSubscription(ctx, writer, doneCh, "run-1")
		close(returned)
	}()

	cancel()

	select {
	case <-returned:
		// Good: returned promptly after cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("streamWithoutSubscription did not return after context cancellation")
	}
}

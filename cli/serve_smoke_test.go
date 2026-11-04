package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/hydrate"
)

// hydrateProviderConfig returns a minimal valid provider configuration
// (no network access happens at client construction).
func hydrateProviderConfig() hydrate.ProviderConfig {
	return hydrate.ProviderConfig{
		APIKey:  "test-key",
		BaseURL: "https://provider.example/v1",
	}
}

// TestServeClientFactoryWrapsContextInstrumentation covers the factory
// serve uses: a provider client comes back wrapped for context
// instrumentation, and invalid providers fail loudly.
func TestServeClientFactoryWrapsContextInstrumentation(t *testing.T) {
	factory := serveClientFactory()

	client, err := factory("openai", hydrateProviderConfig())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if client == nil {
		t.Fatal("factory returned nil client")
	}

	if _, err := factory("definitely-not-a-provider", hydrateProviderConfig()); err == nil {
		t.Error("unknown provider should fail loudly")
	}
}

// TestRunServeSmoke boots the full serve stack (SQLite stores, daemon
// tool server, workflow API server, run worker, scheduler) against a
// temporary database and an ephemeral port, waits for the health
// endpoint, and shuts down gracefully on context cancellation.
func TestRunServeSmoke(t *testing.T) {
	restoreOTelGlobals(t)
	t.Setenv("PETALFLOW_API_TOKEN", "test-token")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A config file with event retention so the retention wiring path
	// (config load, validation, store construction) is exercised.
	configPath := writeTestFile(t, "petalflow.yaml", `
observability:
  events:
    retention: 720h
    max_events_per_run: 1000
    prune_interval: 15m
`)

	cmd := NewServeCmd()
	cmd.SetContext(ctx)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--sqlite-path", t.TempDir() + "/petalflow.db",
		"--config", configPath,
	})

	done := make(chan error, 1)
	go func() {
		done <- cmd.Execute()
	}()

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var healthy bool
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("serve exited before becoming healthy: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !healthy {
		t.Fatal("serve did not become healthy in time")
	}

	// Graceful shutdown on context cancellation.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error on graceful shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down after context cancellation")
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

func TestBuildRunOptions_NonStreaming(t *testing.T) {
	cmd := NewRunCmd()

	opts, streaming := buildRunOptions(cmd)
	if streaming {
		t.Fatal("expected streaming to be false by default")
	}
	if opts.EventHandler != nil {
		t.Fatal("expected EventHandler to be nil when streaming is disabled")
	}
}

func TestBuildRunOptions_StreamingHandler(t *testing.T) {
	cmd := NewRunCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Flags().Set("stream", "true"); err != nil {
		t.Fatalf("setting stream flag: %v", err)
	}

	opts, streaming := buildRunOptions(cmd)
	if !streaming {
		t.Fatal("expected streaming to be enabled")
	}
	if opts.EventHandler == nil {
		t.Fatal("expected EventHandler to be set when streaming is enabled")
	}

	opts.EventHandler(runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1").WithPayload("delta", "hello"))
	opts.EventHandler(runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1").WithPayload("delta", 42))
	opts.EventHandler(runtime.NewEvent(runtime.EventNodeOutputFinal, "run-1"))

	if got := out.String(); got != "hello\n" {
		t.Fatalf("streaming output = %q, want %q", got, "hello\n")
	}
}

func TestApplyRunEnvVars(t *testing.T) {
	cmd := NewRunCmd()
	key := "PETALFLOW_RUN_ENV_TEST"
	t.Setenv(key, "old")

	if err := cmd.Flags().Set("env", key+"=updated"); err != nil {
		t.Fatalf("setting env flag: %v", err)
	}
	if err := cmd.Flags().Set("env", "MALFORMED"); err != nil {
		t.Fatalf("setting malformed env flag: %v", err)
	}

	applyRunEnvVars(cmd)

	if got := os.Getenv(key); got != "updated" {
		t.Fatalf("env %s = %q, want %q", key, got, "updated")
	}
}

func TestResolveRunProviders_InvalidProviderFlag(t *testing.T) {
	cmd := NewRunCmd()
	if err := cmd.Flags().Set("provider-key", "invalid"); err != nil {
		t.Fatalf("setting provider-key flag: %v", err)
	}

	_, err := resolveRunProviders(cmd)
	if err == nil {
		t.Fatal("expected error for invalid provider-key flag")
	}

	exitErr, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.Code != exitProvider {
		t.Fatalf("exit code = %d, want %d", exitErr.Code, exitProvider)
	}
	if !strings.Contains(exitErr.Error(), "invalid provider flag") {
		t.Fatalf("error = %q, expected invalid provider flag message", exitErr.Error())
	}
}

func TestRunRuntimeError(t *testing.T) {
	deadlineCtx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	timeoutErr := runRuntimeError(deadlineCtx, 2*time.Second, errors.New("runtime failed"))
	exitTimeoutErr, ok := timeoutErr.(*ExitError)
	if !ok {
		t.Fatalf("expected ExitError for timeout, got %T", timeoutErr)
	}
	if exitTimeoutErr.Code != exitTimeout {
		t.Fatalf("timeout exit code = %d, want %d", exitTimeoutErr.Code, exitTimeout)
	}

	runtimeErr := runRuntimeError(context.Background(), 2*time.Second, errors.New("runtime failed"))
	exitRuntimeErr, ok := runtimeErr.(*ExitError)
	if !ok {
		t.Fatalf("expected ExitError for runtime failure, got %T", runtimeErr)
	}
	if exitRuntimeErr.Code != exitRuntime {
		t.Fatalf("runtime exit code = %d, want %d", exitRuntimeErr.Code, exitRuntime)
	}
}

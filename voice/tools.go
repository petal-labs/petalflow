package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	toolpolicy "github.com/petal-labs/petalflow/tool"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
)

// Voice tool-bridge defaults.
const (
	// DefaultToolTimeout bounds each tool invocation when no limit is
	// configured, so a stalled tool cannot wedge the event loop forever.
	DefaultToolTimeout = 30 * time.Second

	// DefaultToolConcurrency bounds parallel tool executions per session.
	DefaultToolConcurrency = 4

	// DefaultToolArgsMaxBytes bounds the size of model-supplied arguments.
	DefaultToolArgsMaxBytes int64 = 64 << 10 // 64 KiB
)

// ToolBridgeConfig configures a ToolBridge.
type ToolBridgeConfig struct {
	// Registry is the allowlist of tools executable during the session.
	// Tool calls for names absent from the registry are answered with an
	// error result and never executed. Required.
	Registry *core.ToolRegistry

	// Timeout bounds each tool invocation. Zero applies the default (30s).
	Timeout time.Duration

	// Concurrency bounds parallel tool executions per session.
	// Zero applies the default (4).
	Concurrency int

	// ArgsMaxBytes bounds the size of model-supplied tool arguments.
	// Zero applies the default (64 KiB).
	ArgsMaxBytes int64
}

// normalize applies defaults and validates the config.
func (c ToolBridgeConfig) normalize() (ToolBridgeConfig, error) {
	if c.Registry == nil {
		return c, fmt.Errorf("tool bridge requires a tool registry")
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultToolTimeout
	}
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultToolConcurrency
	}
	if c.ArgsMaxBytes <= 0 {
		c.ArgsMaxBytes = DefaultToolArgsMaxBytes
	}
	if c.Timeout < 0 || c.ArgsMaxBytes < 0 {
		return c, fmt.Errorf("tool bridge bounds must be non-negative")
	}
	return c, nil
}

// ToolBridge connects voice session tool_call events to PetalFlow's existing
// tool orchestration and submits tool_result events back to the session.
//
// Safe tool-call semantics:
//   - Allowlist: only tools registered in the configured registry execute;
//     unknown tool names are answered with an error result.
//   - Bounded input: model-supplied arguments larger than ArgsMaxBytes, or
//     arguments that are not a JSON object, are rejected as error results
//     without touching any tool.
//   - Policy gate: when a toolpolicy evaluator is installed in the bridge
//     context, denials and approval requirements fail closed as error
//     results — voice sessions cannot pause for durable approvals, so
//     POLICY_APPROVAL_REQUIRED is reported and the tool never runs.
//   - Bounded execution: parallel executions are capped (Concurrency) and
//     each invocation is bounded by Timeout.
//   - Error results never panic the event loop: tool failures are submitted
//     back as ToolResult{IsError: true} with the error message redacted to
//     the tool's error string.
//
// Replay boundary: session tool calls are live interaction, like
// ReplayAware=false nodes. They always execute live — runtime replay mocks
// (runtime.ToolMockSet) are never consulted — and they are not recorded into
// run replay captures, because sessions live outside run execution.
type ToolBridge struct {
	session *Session
	config  ToolBridgeConfig
	sem     chan struct{}
	wg      sync.WaitGroup
}

// NewToolBridge creates a tool bridge over the session handle.
func NewToolBridge(session *Session, config ToolBridgeConfig) (*ToolBridge, error) {
	config, err := config.normalize()
	if err != nil {
		return nil, err
	}
	return &ToolBridge{
		session: session,
		config:  config,
		sem:     make(chan struct{}, config.Concurrency),
	}, nil
}

// Serve consumes the session's typed event stream until it closes or ctx is
// cancelled, dispatching tool_call events for execution. Serve returns nil
// when the event stream closes (session ended) or ctx is done; it waits for
// in-flight tool executions to settle before returning. The caller remains
// responsible for closing the session handle.
func (b *ToolBridge) Serve(ctx context.Context) error {
	events := b.session.Events()
	for {
		select {
		case <-ctx.Done():
			b.wg.Wait()
			return nil
		case evt, ok := <-events:
			if !ok {
				b.wg.Wait()
				return nil
			}
			if evt.Type != iriscore.VoiceEventToolCall || evt.ToolCall == nil {
				continue
			}
			b.dispatch(ctx, evt)
		}
	}
}

// dispatch runs one tool call under the concurrency bound.
func (b *ToolBridge) dispatch(ctx context.Context, evt iriscore.VoiceEvent) {
	call := evt.ToolCall
	select {
	case <-ctx.Done():
		return
	case b.sem <- struct{}{}:
	}
	b.wg.Add(1)
	go func() {
		defer func() {
			<-b.sem
			b.wg.Done()
		}()
		result := b.execute(ctx, call)
		// Results after shutdown (session closed or ctx done) are dropped:
		// SubmitToolResult fails harmlessly on a closed session.
		submitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = b.session.SubmitToolResult(submitCtx, result)
	}()
}

// execute performs the safe tool-call semantics and always returns a
// ToolResult keyed to the call ID.
func (b *ToolBridge) execute(ctx context.Context, call *iriscore.ToolCall) iriscore.ToolResult {
	fail := func(format string, args ...any) iriscore.ToolResult {
		return iriscore.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf(format, args...),
			IsError: true,
		}
	}

	if call.ID == "" {
		return fail("tool call is missing call_id")
	}

	// Bounded input: reject oversized or non-object arguments before any
	// tool is touched.
	if int64(len(call.Arguments)) > b.config.ArgsMaxBytes {
		return fail("tool %q arguments exceed %d bytes", call.Name, b.config.ArgsMaxBytes)
	}
	var args map[string]any
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return fail("tool %q arguments must be a JSON object: rejected", call.Name)
		}
	}
	if args == nil {
		args = map[string]any{}
	}

	// Allowlist: the registry is the only source of executable tools.
	tool, ok := b.config.Registry.Get(call.Name)
	if !ok {
		return fail("tool %q is not available in this session", call.Name)
	}

	// Policy gate: same evaluator contract as ToolNode. Voice sessions
	// cannot pause for durable approvals, so approval requirements fail
	// closed as well.
	if gateErr := toolpolicy.CheckPolicy(ctx, tool.Name()); gateErr != nil {
		if toolpolicy.IsToolPolicyError(gateErr) {
			return fail("tool %q rejected before invocation: %s", tool.Name(), toolpolicy.PolicyErrorCode(gateErr))
		}
		return fail("tool %q rejected before invocation", tool.Name())
	}

	// Bounded execution.
	callCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
	defer cancel()

	content, err := tool.Invoke(callCtx, args)
	if callCtx.Err() != nil && err == nil {
		return fail("tool %q timed out after %s", tool.Name(), b.config.Timeout)
	}
	if err != nil {
		if callCtx.Err() != nil {
			return fail("tool %q timed out after %s", tool.Name(), b.config.Timeout)
		}
		return fail("tool %q failed: %s", tool.Name(), err)
	}
	if content == nil {
		content = map[string]any{}
	}
	return iriscore.ToolResult{
		CallID:  call.ID,
		Content: content,
	}
}

// Config returns the bridge's effective configuration.
func (b *ToolBridge) Config() ToolBridgeConfig {
	return b.config
}

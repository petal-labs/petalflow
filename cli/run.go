package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/server"
	"github.com/petal-labs/petalflow/tool"
)

// Exit codes per FRD §3.2
const (
	exitSuccess      = 0
	exitValidation   = 1
	exitRuntime      = 2
	exitFileNotFound = 3
	exitInputParse   = 4
	exitProvider     = 5
	exitWrongSchema  = 6
	exitTimeout      = 10
)

// NewRunCmd creates the "run" subcommand.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <file>",
		Short: "Execute a workflow file",
		Args:  cobra.ExactArgs(1),
		RunE:  runRun,
	}

	cmd.Flags().StringP("input", "i", "", "Input data as inline JSON string")
	cmd.Flags().StringP("input-file", "f", "", "Input data from a JSON or YAML file")
	cmd.Flags().StringP("output", "o", "", "Write output envelope to file (default: stdout)")
	cmd.Flags().String("format", "pretty", "Output format: json | text | pretty")
	cmd.Flags().Duration("timeout", 5*time.Minute, "Execution timeout")
	cmd.Flags().Bool("dry-run", false, "Compile and validate only, do not execute")
	cmd.Flags().StringArray("env", nil, "Set environment variable (repeatable)")
	cmd.Flags().StringArray("provider-key", nil, "Set provider API key (repeatable, e.g. --provider-key anthropic=sk-...)")
	cmd.Flags().String("store-path", "", "Path to SQLite store for tool registry (default: ~/.petalflow/petalflow.db)")
	cmd.Flags().Bool("stream", false, "Enable streaming output via SSE to stdout")

	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	explicitStore := hasRunExplicitStore(cmd)
	store, err := resolveToolStore(cmd)
	if err != nil {
		if explicitStore {
			return exitError(exitRuntime, "loading tool store: %v", err)
		}
		store = runNoopToolStore{}
	}
	defer closeToolStore(store)

	if err := syncRunToolNodeTypes(cmd.Context(), store); err != nil {
		return exitError(exitRuntime, "syncing tool node types: %v", err)
	}

	gd, err := loadWorkflowForRun(cmd, filePath)
	if err != nil {
		return err
	}

	// Dry run: just validate and compile, don't execute.
	if isRunDry(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), "Validation and compilation successful.")
		return nil
	}

	providers, err := resolveRunProviders(cmd)
	if err != nil {
		return err
	}

	// Build input envelope before store hydration so input validation errors are
	// deterministic and not masked by external store state.
	env, err := buildInputEnvelope(cmd)
	if err != nil {
		return err
	}

	toolRegistry, err := buildRunToolRegistry(cmd, store)
	if err != nil {
		return err
	}

	execGraph, err := hydrateRunGraph(cmd, gd, providers, toolRegistry)
	if err != nil {
		return err
	}

	applyRunEnvVars(cmd)
	ctx, cancel, timeout := runContext(cmd)
	defer cancel()

	opts, streaming := buildRunOptions(cmd)
	result, err := runtime.NewRuntime().Run(ctx, execGraph, env, opts)
	if err != nil {
		return runRuntimeError(ctx, timeout, err)
	}

	// Skip writeOutput when streaming — output was already printed incrementally.
	if streaming {
		return nil
	}

	// Format and write output.
	return writeOutput(cmd, result)
}

func loadWorkflowForRun(cmd *cobra.Command, filePath string) (*graph.GraphDefinition, error) {
	// Load and compile the workflow (handles both agent and graph schemas).
	gd, _, err := loader.LoadWorkflow(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exitError(exitFileNotFound, "file not found: %s", filePath)
		}
		// Check if it's a validation error.
		if diagErr, ok := err.(*loader.DiagnosticError); ok {
			printDiagnosticsText(cmd.ErrOrStderr(), diagErr.Diagnostics)
			return nil, exitError(exitValidation, "validation failed")
		}
		return nil, exitError(exitValidation, "%v", err)
	}
	return gd, nil
}

func isRunDry(cmd *cobra.Command) bool {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	return dryRun
}

func resolveRunProviders(cmd *cobra.Command) (hydrate.ProviderMap, error) {
	providerFlags, _ := cmd.Flags().GetStringArray("provider-key")
	flagMap, err := hydrate.ParseProviderFlags(providerFlags)
	if err != nil {
		return nil, exitError(exitProvider, "invalid provider flag: %v", err)
	}
	providers, err := hydrate.ResolveProviders(flagMap)
	if err != nil {
		return nil, exitError(exitProvider, "resolving providers: %v", err)
	}
	return providers, nil
}

func buildRunToolRegistry(cmd *cobra.Command, store tool.Store) (*core.ToolRegistry, error) {
	toolRegistry, err := hydrate.BuildActionToolRegistry(cmd.Context(), store)
	if err != nil {
		return nil, exitError(exitRuntime, "building tool registry: %v", err)
	}
	return toolRegistry, nil
}

func hydrateRunGraph(
	cmd *cobra.Command,
	gd *graph.GraphDefinition,
	providers hydrate.ProviderMap,
	toolRegistry *core.ToolRegistry,
) (*graph.BasicGraph, error) {
	// Build executable graph from definition.
	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	},
		hydrate.WithToolRegistry(toolRegistry),
		hydrate.WithHumanHandler(&cliHumanHandler{w: cmd.ErrOrStderr()}),
	)
	execGraph, err := hydrate.HydrateGraph(gd, providers, factory)
	if err != nil {
		return nil, exitError(exitProvider, "hydrating graph: %v", err)
	}
	return execGraph, nil
}

func applyRunEnvVars(cmd *cobra.Command) {
	envVars, _ := cmd.Flags().GetStringArray("env")
	for _, kv := range envVars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			_ = os.Setenv(parts[0], parts[1])
		}
	}
}

func hasRunExplicitStore(cmd *cobra.Command) bool {
	storePath, _ := cmd.Flags().GetString("store-path")
	if strings.TrimSpace(storePath) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("PETALFLOW_SQLITE_PATH")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("PETALFLOW_TOOLS_STORE_PATH")) != "" {
		return true
	}
	return false
}

func closeToolStore(store tool.Store) {
	if closer, ok := store.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func runContext(cmd *cobra.Command) (context.Context, context.CancelFunc, time.Duration) {
	timeout, _ := cmd.Flags().GetDuration("timeout")
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	return ctx, cancel, timeout
}

func buildRunOptions(cmd *cobra.Command) (runtime.RunOptions, bool) {
	opts := runtime.DefaultRunOptions()
	streaming, _ := cmd.Flags().GetBool("stream")
	if streaming {
		opts.EventHandler = runStreamingEventHandler(cmd.OutOrStdout())
	}
	return opts, streaming
}

func runStreamingEventHandler(out io.Writer) runtime.EventHandler {
	return func(e runtime.Event) {
		switch e.Kind {
		case runtime.EventNodeOutputDelta:
			if delta, ok := e.Payload["delta"].(string); ok {
				fmt.Fprint(out, delta)
			}
		case runtime.EventNodeOutputFinal:
			fmt.Fprintln(out)
		}
	}
}

func runRuntimeError(ctx context.Context, timeout time.Duration, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return exitError(exitTimeout, "execution timed out after %s", timeout)
	}
	return exitError(exitRuntime, "execution failed: %v", err)
}

// buildInputEnvelope creates an Envelope from --input or --input-file flags.
func buildInputEnvelope(cmd *cobra.Command) (*core.Envelope, error) {
	inputStr, _ := cmd.Flags().GetString("input")
	inputFile, _ := cmd.Flags().GetString("input-file")

	if inputStr != "" && inputFile != "" {
		return nil, exitError(exitInputParse, "cannot specify both --input and --input-file")
	}

	if inputStr == "" && inputFile == "" {
		return core.NewEnvelope(), nil
	}

	var data []byte
	if inputStr != "" {
		data = []byte(inputStr)
	} else {
		var err error
		data, err = os.ReadFile(inputFile) // #nosec G304 -- path from user CLI flag
		if err != nil {
			return nil, exitError(exitFileNotFound, "reading input file: %v", err)
		}
	}

	var vars map[string]any
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, exitError(exitInputParse, "parsing input JSON: %v", err)
	}

	return server.EnvelopeFromJSON(vars), nil
}

// writeOutput formats and writes the result envelope.
func writeOutput(cmd *cobra.Command, env *core.Envelope) error {
	format, _ := cmd.Flags().GetString("format")
	outputPath, _ := cmd.Flags().GetString("output")

	var output string
	switch format {
	case "json":
		ej := server.EnvelopeToJSON(env)
		data, err := json.MarshalIndent(ej, "", "  ")
		if err != nil {
			return exitError(exitRuntime, "marshaling output: %v", err)
		}
		output = string(data)
	case "text":
		// Just the primary output value
		if env.Vars != nil {
			if v, ok := env.Vars["output"]; ok {
				output = fmt.Sprintf("%v", v)
			}
		}
	case "pretty":
		output = formatPretty(env)
	default:
		return exitError(exitInputParse, "unknown format %q (use json, text, or pretty)", format)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(output+"\n"), 0600); err != nil {
			return exitError(exitRuntime, "writing output file: %v", err)
		}
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), output)
	return nil
}

// formatPretty returns a human-readable summary of the envelope.
func formatPretty(env *core.Envelope) string {
	var sb strings.Builder

	sb.WriteString("=== Output ===\n")
	if env.Vars != nil {
		for k, v := range env.Vars {
			sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
	}

	if len(env.Messages) > 0 {
		sb.WriteString(fmt.Sprintf("\n=== Messages (%d) ===\n", len(env.Messages)))
		for _, m := range env.Messages {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", m.Role, m.Content))
		}
	}

	if len(env.Artifacts) > 0 {
		sb.WriteString(fmt.Sprintf("\n=== Artifacts (%d) ===\n", len(env.Artifacts)))
		for _, a := range env.Artifacts {
			sb.WriteString(fmt.Sprintf("  %s (%s)\n", a.ID, a.Type))
		}
	}

	if env.Trace.RunID != "" {
		sb.WriteString("\n=== Trace ===\n")
		sb.WriteString(fmt.Sprintf("  Run ID: %s\n", env.Trace.RunID))
	}

	return sb.String()
}

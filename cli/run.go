package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/server"
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
	cmd.Flags().Bool("stream", false, "Enable streaming output via SSE to stdout")

	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	// Load and compile the workflow (handles both agent and graph schemas)
	gd, _, err := loader.LoadWorkflow(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return exitError(exitFileNotFound, "file not found: %s", filePath)
		}
		// Check if it's a validation error
		if diagErr, ok := err.(*loader.DiagnosticError); ok {
			printDiagnosticsText(cmd.ErrOrStderr(), diagErr.Diagnostics)
			return exitError(exitValidation, "validation failed")
		}
		return exitError(exitValidation, "%v", err)
	}

	// Dry run: just validate and compile, don't execute
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "Validation and compilation successful.")
		return nil
	}

	// Resolve provider credentials
	providerFlags, _ := cmd.Flags().GetStringArray("provider-key")
	flagMap, err := hydrate.ParseProviderFlags(providerFlags)
	if err != nil {
		return exitError(exitProvider, "invalid provider flag: %v", err)
	}
	providers, err := hydrate.ResolveProviders(flagMap)
	if err != nil {
		return exitError(exitProvider, "resolving providers: %v", err)
	}

	// Hydrate the graph (build executable graph from definition)
	factory := hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})
	execGraph, err := hydrate.HydrateGraph(gd, providers, factory)
	if err != nil {
		return exitError(exitProvider, "hydrating graph: %v", err)
	}

	// Build input envelope
	env, err := buildInputEnvelope(cmd)
	if err != nil {
		return err
	}

	// Set environment variables
	envVars, _ := cmd.Flags().GetStringArray("env")
	for _, kv := range envVars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			_ = os.Setenv(parts[0], parts[1])
		}
	}

	// Create runtime and execute
	timeout, _ := cmd.Flags().GetDuration("timeout")
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()

	streaming, _ := cmd.Flags().GetBool("stream")
	if streaming {
		opts.EventHandler = func(e runtime.Event) {
			switch e.Kind {
			case runtime.EventNodeOutputDelta:
				if delta, ok := e.Payload["delta"].(string); ok {
					fmt.Fprint(cmd.OutOrStdout(), delta)
				}
			case runtime.EventNodeOutputFinal:
				fmt.Fprintln(cmd.OutOrStdout())
			}
		}
	}

	result, err := rt.Run(ctx, execGraph, env, opts)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return exitError(exitTimeout, "execution timed out after %s", timeout)
		}
		return exitError(exitRuntime, "execution failed: %v", err)
	}

	// Skip writeOutput when streaming — output was already printed incrementally
	if streaming {
		return nil
	}

	// Format and write output
	return writeOutput(cmd, result)
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

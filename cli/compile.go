package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/registry"
)

// NewCompileCmd creates the "compile" subcommand.
func NewCompileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compile <file>",
		Short: "Compile agent workflow to graph IR",
		Args:  cobra.ExactArgs(1),
		RunE:  runCompile,
	}

	cmd.Flags().StringP("output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().Bool("pretty", true, "Pretty-print JSON output")
	cmd.Flags().Bool("validate-only", false, "Only run AgentTask validation, don't compile")

	return cmd
}

// runCompile implements the compile pipeline:
//
//	read file → detectSchema → must be agent workflow → parse → validate
//	→ (if --validate-only: print "Valid" and exit 0)
//	→ compile → graph validate → serialize JSON → write output
func runCompile(cmd *cobra.Command, args []string) error {
	filePath := args[0]
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()

	pretty, _ := cmd.Flags().GetBool("pretty")
	validateOnly, _ := cmd.Flags().GetBool("validate-only")
	outputPath, _ := cmd.Flags().GetString("output")

	// Step 1: Read file
	data, err := os.ReadFile(filePath) // #nosec G304 -- path from user CLI arg
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitError(exitFileNotFound, "file not found: %s", filePath)
		}
		return exitError(exitFileNotFound, "reading file: %s", err)
	}

	// Step 2: Detect schema — must be agent workflow
	kind, err := loader.DetectSchema(data, filePath)
	if err != nil {
		return exitError(exitValidation, "schema detection failed: %s", err)
	}
	if kind != loader.SchemaKindAgent {
		return exitError(exitWrongSchema, "compile only accepts agent workflow files")
	}

	// Step 3: Convert YAML to JSON if needed, then parse into AgentWorkflow
	jsonData, err := yamlToJSONIfNeeded(data, filePath)
	if err != nil {
		return exitError(exitValidation, "parsing file: %s", err)
	}

	wf, err := agent.LoadFromBytes(jsonData)
	if err != nil {
		return exitError(exitValidation, "parsing agent workflow: %s", err)
	}

	// Step 4: AgentTask validation
	diags := agent.Validate(wf)
	if graph.HasErrors(diags) {
		printDiagnosticsText(stderr, graph.Errors(diags))
		return exitError(exitValidation, "agent workflow validation failed with %d error(s)", len(graph.Errors(diags)))
	}

	// Step 5: If --validate-only, print "Valid" and exit 0
	if validateOnly {
		fmt.Fprintln(stdout, "Valid")
		return nil
	}

	// Step 6: Compile to GraphDefinition
	gd, err := agent.Compile(wf)
	if err != nil {
		return exitError(exitValidation, "compilation failed: %s", err)
	}

	// Step 7: Graph validation on compiled output
	graphDiags := gd.ValidateWithRegistry(registry.Global())
	if graph.HasErrors(graphDiags) {
		printDiagnosticsText(stderr, graph.Errors(graphDiags))
		return exitError(exitValidation, "compiled graph validation failed with %d error(s)", len(graph.Errors(graphDiags)))
	}

	// Step 8: Serialize GraphDefinition to JSON
	var jsonOut []byte
	if pretty {
		jsonOut, err = json.MarshalIndent(gd, "", "  ")
	} else {
		jsonOut, err = json.Marshal(gd)
	}
	if err != nil {
		return exitError(exitValidation, "serializing graph definition: %s", err)
	}

	// Append trailing newline for clean output
	jsonOut = append(jsonOut, '\n')

	// Step 9: Write to --output or stdout
	if outputPath != "" {
		if err := os.WriteFile(outputPath, jsonOut, 0600); err != nil {
			return fmt.Errorf("writing output file: %w", err)
		}
	} else {
		if _, err := stdout.Write(jsonOut); err != nil {
			return fmt.Errorf("writing to stdout: %w", err)
		}
	}

	return nil
}

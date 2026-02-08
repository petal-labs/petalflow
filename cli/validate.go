package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewValidateCmd creates the "validate" subcommand.
func NewValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a workflow file without executing",
		Args:  cobra.ExactArgs(1),
		RunE:  runValidate,
	}

	cmd.Flags().String("format", "text", "Output format: text | json")
	cmd.Flags().Bool("strict", false, "Treat warnings as errors")

	return cmd
}

func runValidate(cmd *cobra.Command, args []string) error {
	filePath := args[0]
	format, _ := cmd.Flags().GetString("format")
	strict, _ := cmd.Flags().GetBool("strict")
	out := cmd.OutOrStdout()

	// Read the file.
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitError(exitFileNotFound, "file not found: %s", filePath)
		}
		return fmt.Errorf("reading file: %w", err)
	}

	// Detect schema kind.
	kind, err := loader.DetectSchema(data, filePath)
	if err != nil {
		return fmt.Errorf("detecting schema: %w", err)
	}

	var diags []graph.Diagnostic

	switch kind {
	case loader.SchemaKindAgent:
		diags = validateAgentWorkflow(data, filePath)
	case loader.SchemaKindGraph:
		diags = validateGraphIR(data, filePath)
	default:
		return fmt.Errorf("unknown schema kind %q", kind)
	}

	// Print diagnostics in the requested format.
	printValidateDiagnostics(out, diags, format)

	// Determine exit code.
	hasErrs := graph.HasErrors(diags)
	hasWarns := len(graph.Warnings(diags)) > 0

	if hasErrs || (strict && hasWarns) {
		return exitError(exitValidation, "validation failed")
	}

	return nil
}

// validateAgentWorkflow runs the agent-task validator and, if no errors,
// compiles to a GraphDefinition and runs graph validation as a second pass.
func validateAgentWorkflow(data []byte, filePath string) []graph.Diagnostic {
	jsonData, err := yamlToJSONIfNeeded(data, filePath)
	if err != nil {
		return []graph.Diagnostic{{
			Code:     "AT-000",
			Severity: graph.SeverityError,
			Message:  fmt.Sprintf("Failed to parse file: %v", err),
		}}
	}

	wf, err := agent.LoadFromBytes(jsonData)
	if err != nil {
		return []graph.Diagnostic{{
			Code:     "AT-000",
			Severity: graph.SeverityError,
			Message:  fmt.Sprintf("Failed to load agent workflow: %v", err),
		}}
	}

	// First pass: agent-task validation.
	diags := agent.Validate(wf)

	// Second pass: compile and run graph validation (only if no errors).
	if !graph.HasErrors(diags) {
		gd, err := agent.Compile(wf)
		if err != nil {
			diags = append(diags, graph.Diagnostic{
				Code:     "AT-000",
				Severity: graph.SeverityError,
				Message:  fmt.Sprintf("Compilation failed: %v", err),
			})
		} else {
			graphDiags := gd.Validate()
			diags = append(diags, graphDiags...)
		}
	}

	return diags
}

// validateGraphIR parses a Graph IR file and runs graph validation.
func validateGraphIR(data []byte, filePath string) []graph.Diagnostic {
	jsonData, err := yamlToJSONIfNeeded(data, filePath)
	if err != nil {
		return []graph.Diagnostic{{
			Code:     "GR-000",
			Severity: graph.SeverityError,
			Message:  fmt.Sprintf("Failed to parse file: %v", err),
		}}
	}

	var gd graph.GraphDefinition
	if err := json.Unmarshal(jsonData, &gd); err != nil {
		return []graph.Diagnostic{{
			Code:     "GR-000",
			Severity: graph.SeverityError,
			Message:  fmt.Sprintf("Failed to parse graph definition: %v", err),
		}}
	}

	return gd.Validate()
}

// printValidateDiagnostics writes diagnostics to the writer in the requested
// format, followed by a summary line (for text format).
func printValidateDiagnostics(w io.Writer, diags []graph.Diagnostic, format string) {
	if format == "json" {
		printDiagnosticsJSON(w, diags)
		return
	}
	printDiagnosticsText(w, diags)
}

// printDiagnosticsText writes diagnostics as formatted text lines followed by
// a summary. Used by both the validate and run commands.
func printDiagnosticsText(w io.Writer, diags []graph.Diagnostic) {
	for _, d := range diags {
		sev := strings.ToUpper(d.Severity)
		if d.Path != "" {
			fmt.Fprintf(w, "%s [%s]: %s (at %s)\n", sev, d.Code, d.Message, d.Path)
		} else {
			fmt.Fprintf(w, "%s [%s]: %s\n", sev, d.Code, d.Message)
		}
	}

	errs := graph.Errors(diags)
	warns := graph.Warnings(diags)

	switch {
	case len(errs) == 0 && len(warns) == 0:
		fmt.Fprintln(w, "Valid!")
	case len(errs) == 0 && len(warns) > 0:
		fmt.Fprintf(w, "\nValid! (%d %s)\n", len(warns), pluralize("warning", len(warns)))
	default:
		fmt.Fprintf(w, "\n%d %s, %d %s\n",
			len(errs), pluralize("error", len(errs)),
			len(warns), pluralize("warning", len(warns)))
	}
}

func printDiagnosticsJSON(w io.Writer, diags []graph.Diagnostic) {
	// Output an empty array rather than null when there are no diagnostics.
	if diags == nil {
		diags = []graph.Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(diags)
}

// yamlToJSONIfNeeded converts YAML data to JSON if the file path indicates a
// YAML file. JSON files are returned as-is.
func yamlToJSONIfNeeded(data []byte, path string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".yaml" || ext == ".yml" {
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		return json.Marshal(raw)
	}
	return data, nil
}

// pluralize returns the singular or plural form of a word based on count.
func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

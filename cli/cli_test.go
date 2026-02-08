package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newTestRoot creates a fresh cobra root command wired to all subcommands.
// Each test gets an isolated command tree to avoid shared state.
func newTestRoot() *cobra.Command {
	root := &cobra.Command{
		Use:          "petalflow",
		SilenceUsage: true,
	}
	root.AddCommand(NewRunCmd())
	root.AddCommand(NewCompileCmd())
	root.AddCommand(NewValidateCmd())
	return root
}

// executeCommand runs a cobra command with the given args and captures stdout/stderr.
func executeCommand(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// writeTestFile creates a temporary file with the given content and returns its path.
func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validAgentJSON = `{
  "version": "1.0",
  "kind": "agent_workflow",
  "id": "test_cli",
  "name": "Test CLI",
  "agents": {
    "researcher": {
      "role": "Researcher",
      "goal": "Find information",
      "provider": "anthropic",
      "model": "claude-sonnet-4-20250514"
    }
  },
  "tasks": {
    "research": {
      "description": "Research the topic",
      "agent": "researcher",
      "expected_output": "Summary of findings"
    }
  },
  "execution": {
    "strategy": "sequential",
    "task_order": ["research"]
  }
}`

const validGraphJSON = `{
  "id": "test_graph",
  "version": "1.0",
  "nodes": [
    {"id": "a", "type": "noop"},
    {"id": "b", "type": "noop"}
  ],
  "edges": [
    {"source": "a", "sourceHandle": "output", "target": "b", "targetHandle": "input"}
  ],
  "entry": "a"
}`

const invalidAgentJSON = `{
  "version": "1.0",
  "kind": "agent_workflow",
  "id": "bad",
  "agents": {},
  "tasks": {
    "t1": {
      "description": "",
      "agent": "nonexistent",
      "expected_output": ""
    }
  },
  "execution": {
    "strategy": "sequential",
    "task_order": ["t1"]
  }
}`

// --- Validate command tests ---

func TestValidate_ValidAgentJSON(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "validate", path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "Valid") {
		t.Errorf("expected 'Valid' in output, got: %q", stdout)
	}
}

func TestValidate_ValidGraphJSON(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validGraphJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "validate", path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "Valid") {
		t.Errorf("expected 'Valid' in output, got: %q", stdout)
	}
}

func TestValidate_ValidAgentYAML(t *testing.T) {
	yaml := `version: "1.0"
kind: agent_workflow
id: test_yaml
name: Test YAML
agents:
  researcher:
    role: Researcher
    goal: Find information
    provider: anthropic
    model: claude-sonnet-4-20250514
tasks:
  research:
    description: Research the topic
    agent: researcher
    expected_output: Summary of findings
execution:
  strategy: sequential
  task_order:
    - research
`
	path := writeTestFile(t, "workflow.yaml", yaml)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "validate", path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "Valid") {
		t.Errorf("expected 'Valid' in output, got: %q", stdout)
	}
}

func TestValidate_InvalidFile_ShowsDiagnostics(t *testing.T) {
	path := writeTestFile(t, "bad.json", invalidAgentJSON)
	root := newTestRoot()
	stdout, _, _ := executeCommand(root, "validate", path)
	// Should contain error diagnostics
	if !strings.Contains(stdout, "ERROR") {
		t.Errorf("expected error diagnostics, got: %q", stdout)
	}
}

func TestValidate_JSONFormat(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validGraphJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "validate", path, "--format", "json")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// JSON format should produce a JSON array (even if empty)
	if !strings.HasPrefix(strings.TrimSpace(stdout), "[") {
		t.Errorf("expected JSON array output, got: %q", stdout)
	}
}

func TestValidate_FileNotFound(t *testing.T) {
	root := newTestRoot()
	_, _, err := executeCommand(root, "validate", "/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- Compile command tests ---

func TestCompile_AgentWorkflow(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "compile", path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Output should contain JSON with nodes
	if !strings.Contains(stdout, `"nodes"`) {
		t.Errorf("expected compiled JSON with nodes, got: %q", stdout)
	}
	if !strings.Contains(stdout, "research__researcher") {
		t.Errorf("expected node ID 'research__researcher', got: %q", stdout)
	}
}

func TestCompile_ValidateOnly(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "compile", path, "--validate-only")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "Valid") {
		t.Errorf("expected 'Valid' output, got: %q", stdout)
	}
	// Should NOT contain compiled graph
	if strings.Contains(stdout, `"nodes"`) {
		t.Error("--validate-only should not produce compiled output")
	}
}

func TestCompile_WrongSchemaKind(t *testing.T) {
	// Graph IR is not accepted by compile
	path := writeTestFile(t, "workflow.json", validGraphJSON)
	root := newTestRoot()
	_, _, err := executeCommand(root, "compile", path)
	if err == nil {
		t.Fatal("expected error for graph IR input")
	}
	// Should indicate wrong schema kind
	if !strings.Contains(err.Error(), "agent workflow") {
		t.Errorf("error should mention agent workflow, got: %q", err.Error())
	}
}

func TestCompile_FileNotFound(t *testing.T) {
	root := newTestRoot()
	_, _, err := executeCommand(root, "compile", "/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCompile_OutputToFile(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "output.json")

	root := newTestRoot()
	_, _, err := executeCommand(root, "compile", path, "-o", outPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), `"nodes"`) {
		t.Error("output file should contain compiled graph JSON")
	}
}

// --- Run command tests ---

func TestRun_DryRun(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "run", path, "--dry-run")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "successful") {
		t.Errorf("expected success message, got: %q", stdout)
	}
}

func TestRun_FileNotFound(t *testing.T) {
	root := newTestRoot()
	_, _, err := executeCommand(root, "run", "/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRun_InvalidInputJSON(t *testing.T) {
	// Use a graph IR file (no providers needed) to reach the input parsing stage
	path := writeTestFile(t, "workflow.json", validGraphJSON)
	root := newTestRoot()
	_, _, err := executeCommand(root, "run", path, "--input", "{invalid}")
	if err == nil {
		t.Fatal("expected error for invalid input JSON")
	}
	if !strings.Contains(err.Error(), "parsing input JSON") {
		t.Errorf("error should mention input parsing, got: %q", err.Error())
	}
}

func TestRun_BothInputFlags(t *testing.T) {
	path := writeTestFile(t, "workflow.json", validAgentJSON)
	root := newTestRoot()
	_, _, err := executeCommand(root, "run", path, "--input", "{}", "--input-file", "file.json")
	if err == nil {
		t.Fatal("expected error when both --input and --input-file specified")
	}
}

func TestRun_ValidationError(t *testing.T) {
	path := writeTestFile(t, "bad.json", invalidAgentJSON)
	root := newTestRoot()
	_, _, err := executeCommand(root, "run", path)
	if err == nil {
		t.Fatal("expected error for invalid workflow")
	}
}

// --- Root command tests ---

func TestRoot_NoArgs(t *testing.T) {
	root := newTestRoot()
	stdout, _, err := executeCommand(root)
	if err != nil {
		t.Fatalf("root with no args should not error, got: %v", err)
	}
	if !strings.Contains(stdout, "petalflow") {
		t.Errorf("expected help text, got: %q", stdout)
	}
}

func TestRoot_Help(t *testing.T) {
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "--help")
	if err != nil {
		t.Fatalf("--help should not error, got: %v", err)
	}
	if !strings.Contains(stdout, "run") {
		t.Error("help should list 'run' command")
	}
	if !strings.Contains(stdout, "compile") {
		t.Error("help should list 'compile' command")
	}
	if !strings.Contains(stdout, "validate") {
		t.Error("help should list 'validate' command")
	}
}

func TestRun_SubcommandHelp(t *testing.T) {
	root := newTestRoot()
	stdout, _, err := executeCommand(root, "run", "--help")
	if err != nil {
		t.Fatalf("run --help should not error, got: %v", err)
	}
	if !strings.Contains(stdout, "Execute a workflow file") {
		t.Error("run help should show description")
	}
	if !strings.Contains(stdout, "--dry-run") {
		t.Error("run help should show --dry-run flag")
	}
}

package agent

import (
	"testing"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/tool"
)

// validWorkflow returns a minimal valid AgentWorkflow for testing.
func validWorkflow() *AgentWorkflow {
	return &AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "test_workflow",
		Name:    "Test",
		Agents: map[string]Agent{
			"researcher": {
				Role:     "Researcher",
				Goal:     "Research topics",
				Provider: "openai",
				Model:    "gpt-4",
			},
		},
		Tasks: map[string]Task{
			"research": {
				Description:    "Research the topic",
				Agent:          "researcher",
				ExpectedOutput: "Research results",
			},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"research"},
		},
	}
}

func TestValidate_ValidWorkflow(t *testing.T) {
	diags := Validate(validWorkflow())
	if graph.HasErrors(diags) {
		t.Errorf("expected no errors, got: %v", diags)
	}
}

func TestValidate_NilWorkflow(t *testing.T) {
	diags := Validate(nil)
	if !graph.HasErrors(diags) {
		t.Fatal("expected error for nil workflow")
	}
}

// --- AT-001: UNDEFINED_AGENT ---

func TestValidate_AT001_UndefinedAgent(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["research"] = Task{
		Description:    "Research",
		Agent:          "nonexistent",
		ExpectedOutput: "Results",
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-001")
	if found == nil {
		t.Fatal("expected AT-001 for undefined agent reference")
	}
	if found.Path != "tasks.research.agent" {
		t.Errorf("path = %q, want %q", found.Path, "tasks.research.agent")
	}
}

// --- AT-002: INVALID_PROVIDER ---

func TestValidate_AT002_InvalidProvider(t *testing.T) {
	wf := validWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "unknown_provider",
		Model:    "gpt-4",
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-002")
	if found == nil {
		t.Fatal("expected AT-002 for invalid provider")
	}
}

func TestValidate_AT002_ValidProviders(t *testing.T) {
	providers := []string{"anthropic", "openai", "google", "cohere", "mistral", "groq", "ollama"}
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			wf := validWorkflow()
			wf.Agents["researcher"] = Agent{
				Role:     "R",
				Goal:     "G",
				Provider: p,
				Model:    "m",
			}
			diags := Validate(wf)
			found := findDiagCode(diags, "AT-002")
			if found != nil {
				t.Errorf("provider %q should be valid, got AT-002", p)
			}
		})
	}
}

// --- AT-004: UNKNOWN_TOOL ---

func TestValidate_AT004_UnknownTool(t *testing.T) {
	wf := validWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "R",
		Goal:     "G",
		Provider: "openai",
		Model:    "gpt-4",
		Tools:    []string{"totally_fake_tool"},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-004")
	if found == nil {
		t.Fatal("expected AT-004 for unknown tool")
	}
}

func TestValidate_AT004_KnownTool(t *testing.T) {
	wf := validWorkflow()
	// "tool" is registered as a tool in builtins
	wf.Agents["researcher"] = Agent{
		Role:     "R",
		Goal:     "G",
		Provider: "openai",
		Model:    "gpt-4",
		Tools:    []string{"tool"},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-004")
	if found != nil {
		t.Errorf("built-in tool should be valid, got AT-004: %s", found.Message)
	}
}

func TestValidate_AT004_ToolActionAndToolConfig(t *testing.T) {
	const toolName = "phase3_tool_cfg_ok"

	registry.Global().Register(registry.NodeTypeDef{
		Type:     toolName + ".list",
		Category: "tool",
		IsTool:   true,
		ToolMode: "function_call",
		ConfigSchema: map[string]any{
			"tool_config": map[string]tool.FieldSpec{
				"region": {Type: tool.TypeString},
			},
		},
	})

	wf := validWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "R",
		Goal:     "G",
		Provider: "openai",
		Model:    "gpt-4",
		Tools:    []string{toolName + ".list"},
		ToolConfig: map[string]map[string]any{
			toolName: {
				"region": "us-west-2",
			},
		},
	}

	diags := Validate(wf)
	if found := findDiagCode(diags, "AT-004"); found != nil {
		t.Fatalf("expected no AT-004 diagnostics, got: %v", diags)
	}
}

func TestValidate_AT004_UnknownAction(t *testing.T) {
	const toolName = "phase3_tool_missing_action"

	registry.Global().Register(registry.NodeTypeDef{
		Type:     toolName + ".list",
		Category: "tool",
		IsTool:   true,
		ToolMode: "function_call",
		ConfigSchema: map[string]any{
			"tool_config": map[string]tool.FieldSpec{
				"region": {Type: tool.TypeString},
			},
		},
	})

	wf := validWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "R",
		Goal:     "G",
		Provider: "openai",
		Model:    "gpt-4",
		Tools:    []string{toolName + ".download"},
	}

	diags := Validate(wf)
	if findDiagCode(diags, "AT-004") == nil {
		t.Fatalf("expected AT-004 diagnostic for unknown action, got: %v", diags)
	}
}

func TestValidate_AT004_UnknownToolConfigField(t *testing.T) {
	const toolName = "phase3_tool_bad_config"

	registry.Global().Register(registry.NodeTypeDef{
		Type:     toolName + ".list",
		Category: "tool",
		IsTool:   true,
		ToolMode: "function_call",
		ConfigSchema: map[string]any{
			"tool_config": map[string]tool.FieldSpec{
				"region": {Type: tool.TypeString},
			},
		},
	})

	wf := validWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "R",
		Goal:     "G",
		Provider: "openai",
		Model:    "gpt-4",
		Tools:    []string{toolName + ".list"},
		ToolConfig: map[string]map[string]any{
			toolName: {
				"invalid_key": "x",
			},
		},
	}

	diags := Validate(wf)
	if findDiagCode(diags, "AT-004") == nil {
		t.Fatalf("expected AT-004 diagnostic for unknown tool_config field, got: %v", diags)
	}
}

// --- AT-005: INVALID_STRATEGY ---

func TestValidate_AT005_InvalidStrategy(t *testing.T) {
	wf := validWorkflow()
	wf.Execution.Strategy = "round_robin"

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-005")
	if found == nil {
		t.Fatal("expected AT-005 for invalid strategy")
	}
}

func TestValidate_AT005_ValidStrategies(t *testing.T) {
	strategies := []string{"sequential", "parallel", "hierarchical", "custom"}
	for _, s := range strategies {
		t.Run(s, func(t *testing.T) {
			wf := validWorkflow()
			wf.Execution.Strategy = s
			// Ensure strategy-specific requirements are met
			switch s {
			case "sequential":
				wf.Execution.TaskOrder = []string{"research"}
			case "custom":
				wf.Execution.Tasks = map[string]TaskDependencies{
					"research": {DependsOn: []string{}},
				}
			}
			diags := Validate(wf)
			found := findDiagCode(diags, "AT-005")
			if found != nil {
				t.Errorf("strategy %q should be valid, got AT-005", s)
			}
		})
	}
}

// --- AT-006: MISSING_TASK_ORDER ---

func TestValidate_AT006_MissingTaskOrder(t *testing.T) {
	wf := validWorkflow()
	wf.Execution.TaskOrder = nil

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-006")
	if found == nil {
		t.Fatal("expected AT-006 when sequential strategy has no task_order")
	}
}

func TestValidate_AT006_IncompleteTaskOrder(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["write"] = Task{
		Description:    "Write report",
		Agent:          "researcher",
		ExpectedOutput: "Report",
	}
	// task_order only has "research", missing "write"
	wf.Execution.TaskOrder = []string{"research"}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-006")
	if found == nil {
		t.Fatal("expected AT-006 when task_order doesn't list all tasks")
	}
}

func TestValidate_AT006_UndefinedTaskInOrder(t *testing.T) {
	wf := validWorkflow()
	wf.Execution.TaskOrder = []string{"research", "nonexistent"}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-006")
	if found == nil {
		t.Fatal("expected AT-006 when task_order references undefined task")
	}
}

// --- AT-007: CYCLE_DETECTED ---

func TestValidate_AT007_CycleDetected(t *testing.T) {
	wf := &AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "cycle",
		Name:    "Cycle",
		Agents: map[string]Agent{
			"agent": {Role: "R", Goal: "G", Provider: "openai", Model: "m"},
		},
		Tasks: map[string]Task{
			"task_a": {Description: "A", Agent: "agent", ExpectedOutput: "A"},
			"task_b": {Description: "B", Agent: "agent", ExpectedOutput: "B"},
			"task_c": {Description: "C", Agent: "agent", ExpectedOutput: "C"},
		},
		Execution: ExecutionConfig{
			Strategy: "custom",
			Tasks: map[string]TaskDependencies{
				"task_a": {DependsOn: []string{"task_c"}},
				"task_b": {DependsOn: []string{"task_a"}},
				"task_c": {DependsOn: []string{"task_b"}},
			},
		},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-007")
	if found == nil {
		t.Fatal("expected AT-007 for dependency cycle")
	}
}

func TestValidate_AT007_NoCycle(t *testing.T) {
	wf := &AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "dag",
		Name:    "DAG",
		Agents: map[string]Agent{
			"agent": {Role: "R", Goal: "G", Provider: "openai", Model: "m"},
		},
		Tasks: map[string]Task{
			"task_a": {Description: "A", Agent: "agent", ExpectedOutput: "A"},
			"task_b": {Description: "B", Agent: "agent", ExpectedOutput: "B"},
			"task_c": {Description: "C", Agent: "agent", ExpectedOutput: "C"},
		},
		Execution: ExecutionConfig{
			Strategy: "custom",
			Tasks: map[string]TaskDependencies{
				"task_b": {DependsOn: []string{"task_a"}},
				"task_c": {DependsOn: []string{"task_a", "task_b"}},
			},
		},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-007")
	if found != nil {
		t.Errorf("valid DAG should not trigger AT-007, got: %s", found.Message)
	}
}

// --- AT-008: UNRESOLVED_REF ---

func TestValidate_AT008_UnresolvedInputRef(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["research"] = Task{
		Description:    "Research",
		Agent:          "researcher",
		ExpectedOutput: "Results",
		Inputs: map[string]string{
			"data": "{{tasks.missing_task.output}}",
		},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-008")
	if found == nil {
		t.Fatal("expected AT-008 for unresolved task reference")
	}
	if found.Path != "tasks.research.inputs.data" {
		t.Errorf("path = %q, want %q", found.Path, "tasks.research.inputs.data")
	}
}

func TestValidate_AT008_ValidInputRef(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["write"] = Task{
		Description:    "Write",
		Agent:          "researcher",
		ExpectedOutput: "Report",
		Inputs: map[string]string{
			"data": "{{tasks.research.output}}",
		},
	}
	wf.Execution.TaskOrder = []string{"research", "write"}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-008")
	if found != nil {
		t.Errorf("valid ref should not trigger AT-008, got: %s", found.Message)
	}
}

func TestValidate_AT008_UnresolvedContextRef(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["research"] = Task{
		Description:    "Research",
		Agent:          "researcher",
		ExpectedOutput: "Results",
		Context:        []string{"nonexistent_task"},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-008")
	if found == nil {
		t.Fatal("expected AT-008 for unresolved context reference")
	}
}

// --- AT-009: ORPHAN_TASK ---

func TestValidate_AT009_OrphanTask_Custom(t *testing.T) {
	wf := &AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "orphan",
		Name:    "Orphan",
		Agents: map[string]Agent{
			"agent": {Role: "R", Goal: "G", Provider: "openai", Model: "m"},
		},
		Tasks: map[string]Task{
			"task_a": {Description: "A", Agent: "agent", ExpectedOutput: "A"},
			"task_b": {Description: "B", Agent: "agent", ExpectedOutput: "B"},
			"orphan": {Description: "O", Agent: "agent", ExpectedOutput: "O"},
		},
		Execution: ExecutionConfig{
			Strategy: "custom",
			Tasks: map[string]TaskDependencies{
				"task_b": {DependsOn: []string{"task_a"}},
				// "orphan" not referenced at all
			},
		},
	}

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-009")
	if found == nil {
		t.Fatal("expected AT-009 for orphan task in custom strategy")
	}
}

func TestValidate_AT009_NoOrphan_ParallelStrategy(t *testing.T) {
	wf := validWorkflow()
	wf.Execution.Strategy = "parallel"
	wf.Execution.TaskOrder = nil

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-009")
	if found != nil {
		t.Errorf("parallel strategy includes all tasks, no AT-009 expected")
	}
}

// --- AT-010: MISSING_REQUIRED ---

func TestValidate_AT010_MissingAgentFields(t *testing.T) {
	wf := validWorkflow()
	wf.Agents["empty"] = Agent{} // all fields empty
	wf.Tasks["research"] = Task{
		Description:    "Research",
		Agent:          "empty",
		ExpectedOutput: "Results",
	}

	diags := Validate(wf)
	at010s := findAllDiagCode(diags, "AT-010")
	// Should have errors for role, goal, provider, model
	if len(at010s) < 4 {
		t.Errorf("expected at least 4 AT-010 errors for empty agent, got %d: %v", len(at010s), at010s)
	}
}

func TestValidate_AT010_MissingTaskFields(t *testing.T) {
	wf := validWorkflow()
	wf.Tasks["empty"] = Task{} // all fields empty
	wf.Execution.TaskOrder = []string{"research", "empty"}

	diags := Validate(wf)
	at010s := findAllDiagCode(diags, "AT-010")
	// Should have errors for description, agent, expected_output
	count := 0
	for _, d := range at010s {
		if contains(d.Path, "tasks.empty") {
			count++
		}
	}
	if count < 3 {
		t.Errorf("expected at least 3 AT-010 errors for empty task, got %d", count)
	}
}

func TestValidate_AT010_MissingStrategy(t *testing.T) {
	wf := validWorkflow()
	wf.Execution.Strategy = ""

	diags := Validate(wf)
	found := findDiagCode(diags, "AT-010")
	if found == nil {
		t.Fatal("expected AT-010 for missing execution strategy")
	}
}

// --- AT-012: INVALID_ID_FORMAT ---

func TestValidate_AT012_InvalidIDFormat(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"starts with number", "1task"},
		{"starts with uppercase", "Task"},
		{"contains dash", "my-task"},
		{"contains space", "my task"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &AgentWorkflow{
				Version: "1.0",
				Kind:    "agent_workflow",
				ID:      "test",
				Name:    "Test",
				Agents: map[string]Agent{
					tt.id: {Role: "R", Goal: "G", Provider: "openai", Model: "m"},
				},
				Tasks: map[string]Task{
					"task": {Description: "D", Agent: tt.id, ExpectedOutput: "E"},
				},
				Execution: ExecutionConfig{
					Strategy:  "sequential",
					TaskOrder: []string{"task"},
				},
			}
			diags := Validate(wf)
			found := findDiagCode(diags, "AT-012")
			if found == nil {
				t.Errorf("expected AT-012 for ID %q", tt.id)
			}
		})
	}
}

func TestValidate_AT012_ValidIDs(t *testing.T) {
	validIDs := []string{"a", "abc", "my_task", "task_123", "a1b2c3"}
	for _, id := range validIDs {
		t.Run(id, func(t *testing.T) {
			wf := &AgentWorkflow{
				Version: "1.0",
				Kind:    "agent_workflow",
				ID:      "test",
				Name:    "Test",
				Agents: map[string]Agent{
					id: {Role: "R", Goal: "G", Provider: "openai", Model: "m"},
				},
				Tasks: map[string]Task{
					"task": {Description: "D", Agent: id, ExpectedOutput: "E"},
				},
				Execution: ExecutionConfig{
					Strategy:  "sequential",
					TaskOrder: []string{"task"},
				},
			}
			diags := Validate(wf)
			found := findDiagCode(diags, "AT-012")
			if found != nil {
				t.Errorf("ID %q should be valid, got AT-012: %s", id, found.Message)
			}
		})
	}
}

// --- Multiple errors ---

func TestValidate_MultipleErrors(t *testing.T) {
	wf := &AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "bad",
		Name:    "Bad",
		Agents: map[string]Agent{
			"agent": {
				Role:     "", // AT-010
				Goal:     "G",
				Provider: "badprov", // AT-002
				Model:    "m",
				Tools:    []string{"fake_tool"}, // AT-004
			},
		},
		Tasks: map[string]Task{
			"task_a": {
				Description:    "A",
				Agent:          "nobody", // AT-001
				ExpectedOutput: "A",
				Inputs: map[string]string{
					"x": "{{tasks.missing.output}}", // AT-008
				},
			},
		},
		Execution: ExecutionConfig{
			Strategy: "badstrat", // AT-005
		},
	}

	diags := Validate(wf)
	errors := graph.Errors(diags)
	if len(errors) < 5 {
		t.Errorf("expected at least 5 errors, got %d: %v", len(errors), errors)
	}

	// Check specific codes are present
	codes := make(map[string]bool)
	for _, d := range errors {
		codes[d.Code] = true
	}
	expected := []string{"AT-001", "AT-002", "AT-004", "AT-005", "AT-010"}
	for _, code := range expected {
		if !codes[code] {
			t.Errorf("expected error code %s to be present", code)
		}
	}
}

// --- extractTaskRefs ---

func TestExtractTaskRefs(t *testing.T) {
	tests := []struct {
		tmpl string
		want []string
	}{
		{"{{tasks.research.output}}", []string{"research"}},
		{"{{tasks.a.output}} and {{tasks.b.result}}", []string{"a", "b"}},
		{"no refs here", nil},
		{"{{tasks.same.x}} {{tasks.same.y}}", []string{"same"}}, // deduplicated
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.tmpl, func(t *testing.T) {
			got := extractTaskRefs(tt.tmpl)
			if len(got) != len(tt.want) {
				t.Errorf("extractTaskRefs(%q) = %v, want %v", tt.tmpl, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractTaskRefs(%q)[%d] = %q, want %q", tt.tmpl, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- isValidID ---

func TestIsValidID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"a", true},
		{"abc", true},
		{"task_1", true},
		{"my_long_task_id", true},
		{"", false},
		{"1abc", false},
		{"ABC", false},
		{"has-dash", false},
		{"has space", false},
		{"_leading", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := isValidID(tt.id); got != tt.want {
				t.Errorf("isValidID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// --- helpers ---

func findDiagCode(diags []graph.Diagnostic, code string) *graph.Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}

func findAllDiagCode(diags []graph.Diagnostic, code string) []graph.Diagnostic {
	var result []graph.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			result = append(result, d)
		}
	}
	return result
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

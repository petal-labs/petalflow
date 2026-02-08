package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// fullWorkflow returns a fully populated AgentWorkflow for testing.
func fullWorkflow() AgentWorkflow {
	return AgentWorkflow{
		Version: "1.0",
		Kind:    "agent-workflow",
		ID:      "wf-001",
		Name:    "Research Pipeline",
		Agents: map[string]Agent{
			"researcher": {
				Role:      "Senior Researcher",
				Goal:      "Find relevant information",
				Backstory: "Expert in data analysis",
				Provider:  "openai",
				Model:     "gpt-4",
				Tools:     []string{"web_search", "arxiv"},
				Config: map[string]any{
					"temperature": 0.7,
					"max_tokens":  float64(2048),
				},
			},
			"writer": {
				Role:     "Technical Writer",
				Goal:     "Produce clear documentation",
				Provider: "anthropic",
				Model:    "claude-3",
			},
		},
		Tasks: map[string]Task{
			"research": {
				Description:    "Research the topic thoroughly",
				Agent:          "researcher",
				ExpectedOutput: "A detailed research summary",
				OutputKey:      "research_result",
				Inputs: map[string]string{
					"topic": "concurrency patterns",
				},
				Review:  "writer",
				Context: []string{"prior_work"},
			},
			"write_report": {
				Description:    "Write a report from the research",
				Agent:          "writer",
				ExpectedOutput: "A polished report",
			},
		},
		Execution: ExecutionConfig{
			Strategy:      "parallel",
			TaskOrder:     []string{"research", "write_report"},
			MergeStrategy: "concatenate",
			ManagerAgent:  "researcher",
			Tasks: map[string]TaskDependencies{
				"write_report": {DependsOn: []string{"research"}},
			},
		},
	}
}

func TestJSONRoundTrip(t *testing.T) {
	original := fullWorkflow()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AgentWorkflow
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Top-level fields.
	if decoded.Version != original.Version {
		t.Errorf("Version = %q, want %q", decoded.Version, original.Version)
	}
	if decoded.Kind != original.Kind {
		t.Errorf("Kind = %q, want %q", decoded.Kind, original.Kind)
	}
	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Name != original.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, original.Name)
	}

	// Agents.
	if len(decoded.Agents) != len(original.Agents) {
		t.Fatalf("Agents count = %d, want %d", len(decoded.Agents), len(original.Agents))
	}
	researcher := decoded.Agents["researcher"]
	if researcher.Role != "Senior Researcher" {
		t.Errorf("researcher.Role = %q, want %q", researcher.Role, "Senior Researcher")
	}
	if researcher.Goal != "Find relevant information" {
		t.Errorf("researcher.Goal = %q, want %q", researcher.Goal, "Find relevant information")
	}
	if researcher.Backstory != "Expert in data analysis" {
		t.Errorf("researcher.Backstory = %q, want %q", researcher.Backstory, "Expert in data analysis")
	}
	if researcher.Provider != "openai" {
		t.Errorf("researcher.Provider = %q, want %q", researcher.Provider, "openai")
	}
	if researcher.Model != "gpt-4" {
		t.Errorf("researcher.Model = %q, want %q", researcher.Model, "gpt-4")
	}
	if len(researcher.Tools) != 2 {
		t.Fatalf("researcher.Tools count = %d, want 2", len(researcher.Tools))
	}
	if researcher.Tools[0] != "web_search" || researcher.Tools[1] != "arxiv" {
		t.Errorf("researcher.Tools = %v, want [web_search arxiv]", researcher.Tools)
	}
	if researcher.Config["temperature"] != 0.7 {
		t.Errorf("researcher.Config[temperature] = %v, want 0.7", researcher.Config["temperature"])
	}

	writer := decoded.Agents["writer"]
	if writer.Backstory != "" {
		t.Errorf("writer.Backstory = %q, want empty (omitempty)", writer.Backstory)
	}

	// Tasks.
	if len(decoded.Tasks) != len(original.Tasks) {
		t.Fatalf("Tasks count = %d, want %d", len(decoded.Tasks), len(original.Tasks))
	}
	research := decoded.Tasks["research"]
	if research.Description != "Research the topic thoroughly" {
		t.Errorf("research.Description = %q, want %q", research.Description, "Research the topic thoroughly")
	}
	if research.Agent != "researcher" {
		t.Errorf("research.Agent = %q, want %q", research.Agent, "researcher")
	}
	if research.ExpectedOutput != "A detailed research summary" {
		t.Errorf("research.ExpectedOutput mismatch")
	}
	if research.OutputKey != "research_result" {
		t.Errorf("research.OutputKey = %q, want %q", research.OutputKey, "research_result")
	}
	if research.Inputs["topic"] != "concurrency patterns" {
		t.Errorf("research.Inputs[topic] = %q, want %q", research.Inputs["topic"], "concurrency patterns")
	}
	if research.Review != "writer" {
		t.Errorf("research.Review = %q, want %q", research.Review, "writer")
	}
	if len(research.Context) != 1 || research.Context[0] != "prior_work" {
		t.Errorf("research.Context = %v, want [prior_work]", research.Context)
	}

	// Execution config.
	if decoded.Execution.Strategy != "parallel" {
		t.Errorf("Execution.Strategy = %q, want %q", decoded.Execution.Strategy, "parallel")
	}
	if len(decoded.Execution.TaskOrder) != 2 {
		t.Fatalf("Execution.TaskOrder count = %d, want 2", len(decoded.Execution.TaskOrder))
	}
	if decoded.Execution.TaskOrder[0] != "research" || decoded.Execution.TaskOrder[1] != "write_report" {
		t.Errorf("Execution.TaskOrder = %v, want [research write_report]", decoded.Execution.TaskOrder)
	}
	if decoded.Execution.MergeStrategy != "concatenate" {
		t.Errorf("Execution.MergeStrategy = %q, want %q", decoded.Execution.MergeStrategy, "concatenate")
	}
	if decoded.Execution.ManagerAgent != "researcher" {
		t.Errorf("Execution.ManagerAgent = %q, want %q", decoded.Execution.ManagerAgent, "researcher")
	}
	wrDeps := decoded.Execution.Tasks["write_report"]
	if len(wrDeps.DependsOn) != 1 || wrDeps.DependsOn[0] != "research" {
		t.Errorf("Execution.Tasks[write_report].DependsOn = %v, want [research]", wrDeps.DependsOn)
	}
}

func TestOmitemptyFieldsOmittedWhenEmpty(t *testing.T) {
	wf := AgentWorkflow{
		Version: "1.0",
		Kind:    "agent-workflow",
		ID:      "wf-minimal",
		Name:    "Minimal",
		Agents: map[string]Agent{
			"a1": {
				Role:     "Analyst",
				Goal:     "Analyze data",
				Provider: "openai",
				Model:    "gpt-4",
				// Backstory, Tools, Config all empty.
			},
		},
		Tasks: map[string]Task{
			"t1": {
				Description:    "Do analysis",
				Agent:          "a1",
				ExpectedOutput: "Results",
				// OutputKey, Inputs, Review, Context all empty.
			},
		},
		Execution: ExecutionConfig{
			Strategy: "sequential",
			// TaskOrder, MergeStrategy, ManagerAgent, Tasks all empty.
		},
	}

	data, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)

	omitted := []string{
		"backstory",
		"tools",
		"config",
		"output_key",
		"inputs",
		"review",
		"context",
		"task_order",
		"merge_strategy",
		"manager_agent",
	}
	for _, key := range omitted {
		// Check that the JSON key is not present.
		needle := `"` + key + `"`
		for i := 0; i < len(raw)-len(needle)+1; i++ {
			if raw[i:i+len(needle)] == needle {
				t.Errorf("expected key %q to be omitted from JSON, but found it", key)
				break
			}
		}
	}

	// Required fields must be present.
	required := []string{"version", "kind", "id", "name", "agents", "tasks", "execution", "strategy"}
	for _, key := range required {
		needle := `"` + key + `"`
		found := false
		for i := 0; i < len(raw)-len(needle)+1; i++ {
			if raw[i:i+len(needle)] == needle {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected key %q to be present in JSON, but not found", key)
		}
	}
}

func TestLoadFromBytes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(t *testing.T, wf *AgentWorkflow)
	}{
		{
			name: "valid full workflow",
			input: `{
				"version": "1.0",
				"kind": "agent-workflow",
				"id": "wf-test",
				"name": "Test Workflow",
				"agents": {
					"coder": {
						"role": "Developer",
						"goal": "Write code",
						"provider": "openai",
						"model": "gpt-4"
					}
				},
				"tasks": {
					"implement": {
						"description": "Implement the feature",
						"agent": "coder",
						"expected_output": "Working code"
					}
				},
				"execution": {
					"strategy": "sequential",
					"task_order": ["implement"]
				}
			}`,
			wantErr: false,
			check: func(t *testing.T, wf *AgentWorkflow) {
				t.Helper()
				if wf.Version != "1.0" {
					t.Errorf("Version = %q, want %q", wf.Version, "1.0")
				}
				if wf.Kind != "agent-workflow" {
					t.Errorf("Kind = %q, want %q", wf.Kind, "agent-workflow")
				}
				if wf.ID != "wf-test" {
					t.Errorf("ID = %q, want %q", wf.ID, "wf-test")
				}
				coder, ok := wf.Agents["coder"]
				if !ok {
					t.Fatal("agent 'coder' not found")
				}
				if coder.Role != "Developer" {
					t.Errorf("coder.Role = %q, want %q", coder.Role, "Developer")
				}
				impl, ok := wf.Tasks["implement"]
				if !ok {
					t.Fatal("task 'implement' not found")
				}
				if impl.Agent != "coder" {
					t.Errorf("implement.Agent = %q, want %q", impl.Agent, "coder")
				}
				if wf.Execution.Strategy != "sequential" {
					t.Errorf("Execution.Strategy = %q, want %q", wf.Execution.Strategy, "sequential")
				}
				if len(wf.Execution.TaskOrder) != 1 || wf.Execution.TaskOrder[0] != "implement" {
					t.Errorf("Execution.TaskOrder = %v, want [implement]", wf.Execution.TaskOrder)
				}
			},
		},
		{
			name:    "invalid JSON",
			input:   `{not valid json`,
			wantErr: true,
		},
		{
			name:    "empty object",
			input:   `{}`,
			wantErr: false,
			check: func(t *testing.T, wf *AgentWorkflow) {
				t.Helper()
				if wf.Version != "" {
					t.Errorf("Version = %q, want empty", wf.Version)
				}
				if wf.Agents != nil {
					t.Errorf("Agents = %v, want nil", wf.Agents)
				}
				if wf.Tasks != nil {
					t.Errorf("Tasks = %v, want nil", wf.Tasks)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := LoadFromBytes([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, wf)
			}
		})
	}
}

func TestLoadFromFileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestMapKeysPreserved(t *testing.T) {
	input := `{
		"version": "1.0",
		"kind": "agent-workflow",
		"id": "wf-keys",
		"name": "Key Preservation",
		"agents": {
			"agent-alpha": {
				"role": "Alpha",
				"goal": "Be first",
				"provider": "openai",
				"model": "gpt-4"
			},
			"agent-beta": {
				"role": "Beta",
				"goal": "Be second",
				"provider": "anthropic",
				"model": "claude-3"
			},
			"agent-gamma": {
				"role": "Gamma",
				"goal": "Be third",
				"provider": "openai",
				"model": "gpt-3.5"
			}
		},
		"tasks": {
			"task-one": {
				"description": "First task",
				"agent": "agent-alpha",
				"expected_output": "Output 1",
				"inputs": {
					"input-a": "value-a",
					"input-b": "value-b"
				}
			},
			"task-two": {
				"description": "Second task",
				"agent": "agent-beta",
				"expected_output": "Output 2"
			}
		},
		"execution": {
			"strategy": "parallel",
			"tasks": {
				"task-two": {
					"depends_on": ["task-one"]
				}
			}
		}
	}`

	wf, err := LoadFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}

	// Verify all agent keys are present.
	agentKeys := []string{"agent-alpha", "agent-beta", "agent-gamma"}
	for _, key := range agentKeys {
		if _, ok := wf.Agents[key]; !ok {
			t.Errorf("agent key %q not found in Agents map", key)
		}
	}
	if len(wf.Agents) != 3 {
		t.Errorf("Agents count = %d, want 3", len(wf.Agents))
	}

	// Verify all task keys are present.
	taskKeys := []string{"task-one", "task-two"}
	for _, key := range taskKeys {
		if _, ok := wf.Tasks[key]; !ok {
			t.Errorf("task key %q not found in Tasks map", key)
		}
	}
	if len(wf.Tasks) != 2 {
		t.Errorf("Tasks count = %d, want 2", len(wf.Tasks))
	}

	// Verify input keys on task-one.
	t1 := wf.Tasks["task-one"]
	if t1.Inputs["input-a"] != "value-a" {
		t.Errorf("task-one.Inputs[input-a] = %q, want %q", t1.Inputs["input-a"], "value-a")
	}
	if t1.Inputs["input-b"] != "value-b" {
		t.Errorf("task-one.Inputs[input-b] = %q, want %q", t1.Inputs["input-b"], "value-b")
	}

	// Verify execution task dependency keys.
	depEntry, ok := wf.Execution.Tasks["task-two"]
	if !ok {
		t.Fatal("execution tasks key 'task-two' not found")
	}
	if len(depEntry.DependsOn) != 1 || depEntry.DependsOn[0] != "task-one" {
		t.Errorf("task-two.DependsOn = %v, want [task-one]", depEntry.DependsOn)
	}
}

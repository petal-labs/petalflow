package agent

import (
	"encoding/json"
	"fmt"
	"os"
)

// AgentWorkflow is the top-level Agent/Task schema. It defines agents, tasks,
// and execution configuration for AI agent workflows in a friendly JSON format.
type AgentWorkflow struct {
	Version   string           `json:"version"`
	Kind      string           `json:"kind"`
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Agents    map[string]Agent `json:"agents"`
	Tasks     map[string]Task  `json:"tasks"`
	Execution ExecutionConfig  `json:"execution"`
}

// Agent describes an AI agent with its role, provider, model, and optional tools.
type Agent struct {
	Role      string         `json:"role"`
	Goal      string         `json:"goal"`
	Backstory string         `json:"backstory,omitempty"`
	Provider  string         `json:"provider"`
	Model     string         `json:"model"`
	Tools     []string       `json:"tools,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
}

// Task describes a unit of work assigned to an agent.
type Task struct {
	Description    string            `json:"description"`
	Agent          string            `json:"agent"`
	ExpectedOutput string            `json:"expected_output"`
	OutputKey      string            `json:"output_key,omitempty"`
	Inputs         map[string]string `json:"inputs,omitempty"`
	Review         string            `json:"review,omitempty"`
	Context        []string          `json:"context,omitempty"`
}

// ExecutionConfig controls how tasks are executed: sequentially, in parallel,
// or via a manager agent.
type ExecutionConfig struct {
	Strategy      string                     `json:"strategy"`
	TaskOrder     []string                   `json:"task_order,omitempty"`
	MergeStrategy string                     `json:"merge_strategy,omitempty"`
	ManagerAgent  string                     `json:"manager_agent,omitempty"`
	Tasks         map[string]TaskDependencies `json:"tasks,omitempty"`
}

// TaskDependencies declares the tasks that must complete before a given task
// can start.
type TaskDependencies struct {
	DependsOn []string `json:"depends_on"`
}

// LoadFromFile reads and parses an Agent/Task JSON file at the given path.
func LoadFromFile(path string) (*AgentWorkflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agent workflow file: %w", err)
	}
	return LoadFromBytes(data)
}

// LoadFromBytes parses Agent/Task JSON from bytes into an AgentWorkflow.
func LoadFromBytes(data []byte) (*AgentWorkflow, error) {
	var wf AgentWorkflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parsing agent workflow JSON: %w", err)
	}
	return &wf, nil
}

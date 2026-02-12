package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/graph"
)

// compileAgentWorkflowDefinition compiles agent workflows from either:
// 1) canonical server schema (agents/tasks maps), or
// 2) UI editor schema (agents/tasks arrays).
func compileAgentWorkflowDefinition(defBytes []byte, workflowID, workflowName string) (*graph.GraphDefinition, error) {
	if wf, err := agent.LoadFromBytes(defBytes); err == nil {
		return agent.Compile(wf)
	}

	wf, err := loadUIAgentWorkflow(defBytes, workflowID, workflowName)
	if err != nil {
		return nil, err
	}
	return agent.Compile(wf)
}

type uiAgentWorkflowDefinition struct {
	Version   string                `json:"version"`
	Kind      string                `json:"kind"`
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Agents    []uiAgentDefinition   `json:"agents"`
	Tasks     []uiTaskDefinition    `json:"tasks"`
	Execution uiExecutionDefinition `json:"execution"`
}

type uiAgentDefinition struct {
	ID          string   `json:"id"`
	Role        string   `json:"role"`
	Goal        string   `json:"goal"`
	Backstory   string   `json:"backstory"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	Tools       []string `json:"tools"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

type uiTaskDefinition struct {
	ID                 string            `json:"id"`
	Description        string            `json:"description"`
	Agent              string            `json:"agent"`
	ExpectedOutput     string            `json:"expected_output"`
	OutputKey          string            `json:"output_key"`
	Inputs             map[string]string `json:"inputs"`
	Context            []string          `json:"context"`
	HumanReview        bool              `json:"human_review"`
	ReviewInstructions string            `json:"review_instructions"`
}

type uiExecutionDefinition struct {
	Strategy      string              `json:"strategy"`
	TaskOrder     []string            `json:"task_order"`
	MergeStrategy string              `json:"merge_strategy"`
	ManagerAgent  string              `json:"manager_agent"`
	Dependencies  map[string][]string `json:"dependencies"`
}

func loadUIAgentWorkflow(defBytes []byte, workflowID, workflowName string) (*agent.AgentWorkflow, error) {
	var src uiAgentWorkflowDefinition
	if err := json.Unmarshal(defBytes, &src); err != nil {
		return nil, fmt.Errorf("server: parse ui agent workflow definition: %w", err)
	}

	result := &agent.AgentWorkflow{
		Version:   firstNonEmpty(strings.TrimSpace(src.Version), "1.0"),
		Kind:      firstNonEmpty(strings.TrimSpace(src.Kind), "agent_workflow"),
		ID:        firstNonEmpty(strings.TrimSpace(src.ID), strings.TrimSpace(workflowID)),
		Name:      firstNonEmpty(strings.TrimSpace(src.Name), strings.TrimSpace(workflowName)),
		Agents:    map[string]agent.Agent{},
		Tasks:     map[string]agent.Task{},
		Execution: agent.ExecutionConfig{},
	}

	usedAgents := make(map[string]struct{}, len(src.Agents))
	agentIDMap := make(map[string]string, len(src.Agents))
	agentOrder := make([]string, 0, len(src.Agents))
	for i, ag := range src.Agents {
		agentID := uniqueUIID(ag.ID, fmt.Sprintf("agent_%d", i+1), usedAgents)
		if raw := strings.TrimSpace(ag.ID); raw != "" {
			agentIDMap[raw] = agentID
		}
		agentOrder = append(agentOrder, agentID)

		config := map[string]any{}
		if ag.Temperature != nil {
			config["temperature"] = *ag.Temperature
		}
		if ag.MaxTokens != nil {
			config["max_tokens"] = *ag.MaxTokens
		}
		if len(config) == 0 {
			config = nil
		}

		result.Agents[agentID] = agent.Agent{
			Role:      strings.TrimSpace(ag.Role),
			Goal:      strings.TrimSpace(ag.Goal),
			Backstory: strings.TrimSpace(ag.Backstory),
			Provider:  strings.TrimSpace(ag.Provider),
			Model:     strings.TrimSpace(ag.Model),
			Tools:     append([]string(nil), ag.Tools...),
			Config:    config,
		}
	}

	usedTasks := make(map[string]struct{}, len(src.Tasks))
	taskIDMap := make(map[string]string, len(src.Tasks))
	taskOrder := make([]string, 0, len(src.Tasks))
	for i, task := range src.Tasks {
		taskID := uniqueUIID(task.ID, fmt.Sprintf("task_%d", i+1), usedTasks)
		if raw := strings.TrimSpace(task.ID); raw != "" {
			taskIDMap[raw] = taskID
		}
		taskOrder = append(taskOrder, taskID)

		agentID := strings.TrimSpace(task.Agent)
		if mapped, ok := agentIDMap[agentID]; ok {
			agentID = mapped
		}

		review := ""
		if task.HumanReview {
			review = "human"
		}

		contextRefs := make([]string, 0, len(task.Context))
		for _, ref := range task.Context {
			normalized := normalizeTaskRef(ref, taskIDMap)
			if normalized != "" {
				contextRefs = append(contextRefs, normalized)
			}
		}

		result.Tasks[taskID] = agent.Task{
			Description:    strings.TrimSpace(task.Description),
			Agent:          agentID,
			ExpectedOutput: strings.TrimSpace(task.ExpectedOutput),
			OutputKey:      strings.TrimSpace(task.OutputKey),
			Inputs:         cloneStringMap(task.Inputs),
			Review:         review,
			Context:        contextRefs,
		}
	}

	strategy := strings.TrimSpace(src.Execution.Strategy)
	if strategy == "" {
		strategy = "sequential"
	}
	result.Execution.Strategy = strategy
	result.Execution.MergeStrategy = strings.TrimSpace(src.Execution.MergeStrategy)

	switch strategy {
	case "sequential":
		if len(src.Execution.TaskOrder) > 0 {
			result.Execution.TaskOrder = make([]string, 0, len(src.Execution.TaskOrder))
			for _, id := range src.Execution.TaskOrder {
				normalized := normalizeTaskRef(id, taskIDMap)
				if normalized != "" {
					result.Execution.TaskOrder = append(result.Execution.TaskOrder, normalized)
				}
			}
		}
		if len(result.Execution.TaskOrder) == 0 {
			result.Execution.TaskOrder = append([]string(nil), taskOrder...)
		}
	case "custom":
		if len(src.Execution.Dependencies) > 0 {
			result.Execution.Tasks = make(map[string]agent.TaskDependencies, len(src.Execution.Dependencies))
			for rawTaskID, rawDeps := range src.Execution.Dependencies {
				taskID := normalizeTaskRef(rawTaskID, taskIDMap)
				if taskID == "" {
					continue
				}
				deps := make([]string, 0, len(rawDeps))
				for _, dep := range rawDeps {
					normalizedDep := normalizeTaskRef(dep, taskIDMap)
					if normalizedDep != "" {
						deps = append(deps, normalizedDep)
					}
				}
				result.Execution.Tasks[taskID] = agent.TaskDependencies{DependsOn: deps}
			}
			for _, taskID := range taskOrder {
				if _, ok := result.Execution.Tasks[taskID]; !ok {
					result.Execution.Tasks[taskID] = agent.TaskDependencies{}
				}
			}
		}
	case "hierarchical":
		managerAgent := strings.TrimSpace(src.Execution.ManagerAgent)
		if mapped, ok := agentIDMap[managerAgent]; ok {
			managerAgent = mapped
		}
		if managerAgent == "" && len(taskOrder) > 0 {
			managerAgent = result.Tasks[taskOrder[0]].Agent
		}
		if managerAgent == "" && len(agentOrder) > 0 {
			managerAgent = agentOrder[0]
		}
		result.Execution.ManagerAgent = managerAgent
	}

	if result.ID == "" {
		result.ID = "workflow"
	}
	return result, nil
}

func uniqueUIID(rawID, fallback string, used map[string]struct{}) string {
	base := strings.TrimSpace(rawID)
	if base == "" {
		base = fallback
	}
	id := base
	for i := 2; ; i++ {
		if _, exists := used[id]; !exists {
			used[id] = struct{}{}
			return id
		}
		id = fmt.Sprintf("%s_%d", base, i)
	}
}

func normalizeTaskRef(ref string, ids map[string]string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return ""
	}
	if normalized, ok := ids[trimmed]; ok {
		return normalized
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

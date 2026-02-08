package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
)

// validStrategies lists the valid execution strategies.
var validStrategies = map[string]bool{
	"sequential":   true,
	"parallel":     true,
	"hierarchical": true,
	"custom":       true,
}

// validProviders lists the known LLM provider names.
var validProviders = map[string]bool{
	"anthropic": true,
	"openai":    true,
	"google":    true,
	"cohere":    true,
	"mistral":   true,
	"groq":      true,
	"ollama":    true,
}

// idPattern matches valid slug-format identifiers: lowercase letter, then lowercase
// letters/digits/underscores, max 64 characters.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const maxIDLength = 64

// Validate checks the AgentWorkflow for structural errors and returns
// a list of diagnostics (errors and warnings).
func Validate(wf *AgentWorkflow) []graph.Diagnostic {
	var diags []graph.Diagnostic

	if wf == nil {
		diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED", "workflow is nil", ""))
		return diags
	}

	// AT-012: Validate ID formats for agents and tasks
	for id := range wf.Agents {
		if !isValidID(id) {
			diags = append(diags, errDiag("AT-012", "INVALID_ID_FORMAT",
				fmt.Sprintf("Agent ID %q must match [a-z][a-z0-9_]* and be at most %d characters", id, maxIDLength),
				fmt.Sprintf("agents.%s", id)))
		}
	}
	for id := range wf.Tasks {
		if !isValidID(id) {
			diags = append(diags, errDiag("AT-012", "INVALID_ID_FORMAT",
				fmt.Sprintf("Task ID %q must match [a-z][a-z0-9_]* and be at most %d characters", id, maxIDLength),
				fmt.Sprintf("tasks.%s", id)))
		}
	}

	// AT-010: Check required fields on agents
	for id, ag := range wf.Agents {
		path := fmt.Sprintf("agents.%s", id)
		if ag.Role == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Agent %q is missing required field \"role\"", id), path+".role"))
		}
		if ag.Goal == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Agent %q is missing required field \"goal\"", id), path+".goal"))
		}
		if ag.Provider == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Agent %q is missing required field \"provider\"", id), path+".provider"))
		}
		if ag.Model == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Agent %q is missing required field \"model\"", id), path+".model"))
		}

		// AT-002: Validate provider
		if ag.Provider != "" && !validProviders[ag.Provider] {
			diags = append(diags, errDiag("AT-002", "INVALID_PROVIDER",
				fmt.Sprintf("Agent %q has invalid provider %q", id, ag.Provider), path+".provider"))
		}

		// AT-003: Validate model (non-empty, already checked by AT-010)
		// This rule exists for completeness but AT-010 covers the empty case.

		// AT-004: Validate tools exist in registry
		reg := registry.Global()
		for i, toolID := range ag.Tools {
			if !reg.HasTool(toolID) {
				diags = append(diags, errDiag("AT-004", "UNKNOWN_TOOL",
					fmt.Sprintf("Agent %q references unknown tool %q", id, toolID),
					fmt.Sprintf("%s.tools[%d]", path, i)))
			}
		}
	}

	// AT-010: Check required fields on tasks
	for id, task := range wf.Tasks {
		path := fmt.Sprintf("tasks.%s", id)
		if task.Description == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Task %q is missing required field \"description\"", id), path+".description"))
		}
		if task.Agent == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Task %q is missing required field \"agent\"", id), path+".agent"))
		}
		if task.ExpectedOutput == "" {
			diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
				fmt.Sprintf("Task %q is missing required field \"expected_output\"", id), path+".expected_output"))
		}

		// AT-001: Task agent must reference a defined agent
		if task.Agent != "" {
			if _, ok := wf.Agents[task.Agent]; !ok {
				diags = append(diags, errDiag("AT-001", "UNDEFINED_AGENT",
					fmt.Sprintf("Task %q references undefined agent %q", id, task.Agent),
					path+".agent"))
			}
		}

		// AT-008: Validate input template references
		for param, tmpl := range task.Inputs {
			refs := extractTaskRefs(tmpl)
			for _, ref := range refs {
				if _, ok := wf.Tasks[ref]; !ok {
					diags = append(diags, errDiag("AT-008", "UNRESOLVED_REF",
						fmt.Sprintf("Unresolved reference %q in task %q", tmpl, id),
						fmt.Sprintf("%s.inputs.%s", path, param)))
				}
			}
		}

		// AT-008: Validate context references
		for i, ctxRef := range task.Context {
			if _, ok := wf.Tasks[ctxRef]; !ok {
				diags = append(diags, errDiag("AT-008", "UNRESOLVED_REF",
					fmt.Sprintf("Task %q context references undefined task %q", id, ctxRef),
					fmt.Sprintf("%s.context[%d]", path, i)))
			}
		}
	}

	// AT-005: Validate execution strategy
	strategy := wf.Execution.Strategy
	if strategy == "" {
		diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
			"Execution strategy is required", "execution.strategy"))
	} else if !validStrategies[strategy] {
		diags = append(diags, errDiag("AT-005", "INVALID_STRATEGY",
			fmt.Sprintf("Invalid execution strategy %q (must be sequential, parallel, hierarchical, or custom)", strategy),
			"execution.strategy"))
	}

	// Strategy-specific validation
	switch strategy {
	case "sequential":
		diags = append(diags, validateSequential(wf)...)
	case "custom":
		diags = append(diags, validateCustom(wf)...)
	}

	// AT-009: Every defined task must appear in the execution block
	diags = append(diags, validateOrphanTasks(wf)...)

	return diags
}

// validateSequential checks sequential-strategy-specific rules.
func validateSequential(wf *AgentWorkflow) []graph.Diagnostic {
	var diags []graph.Diagnostic

	// AT-006: Sequential strategy requires task_order with all task IDs
	if len(wf.Execution.TaskOrder) == 0 {
		diags = append(diags, errDiag("AT-006", "MISSING_TASK_ORDER",
			"Sequential strategy requires \"task_order\" listing all task IDs",
			"execution.task_order"))
		return diags
	}

	// Check that all tasks appear in task_order
	ordered := make(map[string]bool, len(wf.Execution.TaskOrder))
	for _, id := range wf.Execution.TaskOrder {
		ordered[id] = true
		if _, ok := wf.Tasks[id]; !ok {
			diags = append(diags, errDiag("AT-006", "MISSING_TASK_ORDER",
				fmt.Sprintf("task_order references undefined task %q", id),
				"execution.task_order"))
		}
	}
	for id := range wf.Tasks {
		if !ordered[id] {
			diags = append(diags, errDiag("AT-006", "MISSING_TASK_ORDER",
				fmt.Sprintf("Task %q is not listed in task_order", id),
				"execution.task_order"))
		}
	}

	return diags
}

// validateCustom checks custom-strategy-specific rules.
func validateCustom(wf *AgentWorkflow) []graph.Diagnostic {
	var diags []graph.Diagnostic

	// AT-007: Custom strategy depends_on must form a valid DAG
	// Build adjacency for cycle detection
	if wf.Execution.Tasks == nil {
		return diags
	}

	// Build in-degree and adjacency
	inDegree := make(map[string]int)
	successors := make(map[string][]string)
	allTasks := make(map[string]bool)

	for id := range wf.Tasks {
		allTasks[id] = true
		inDegree[id] = 0
	}

	for id, deps := range wf.Execution.Tasks {
		for _, dep := range deps.DependsOn {
			if !allTasks[dep] {
				diags = append(diags, errDiag("AT-008", "UNRESOLVED_REF",
					fmt.Sprintf("Task %q depends_on references undefined task %q", id, dep),
					fmt.Sprintf("execution.tasks.%s.depends_on", id)))
				continue
			}
			successors[dep] = append(successors[dep], id)
			inDegree[id]++
		}
	}

	// Kahn's algorithm for cycle detection
	queue := make([]string, 0)
	for id := range allTasks {
		if inDegree[id] == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++
		for _, succ := range successors[current] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	if visited < len(allTasks) {
		var cycleNodes []string
		for id := range allTasks {
			if inDegree[id] > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		diags = append(diags, errDiag("AT-007", "CYCLE_DETECTED",
			fmt.Sprintf("Dependency cycle detected involving tasks: %v", cycleNodes),
			"execution.tasks"))
	}

	return diags
}

// validateOrphanTasks checks that every defined task appears in the execution block.
func validateOrphanTasks(wf *AgentWorkflow) []graph.Diagnostic {
	var diags []graph.Diagnostic

	switch wf.Execution.Strategy {
	case "sequential":
		// Already checked by AT-006 in validateSequential
		return diags
	case "parallel":
		// All tasks implicitly included
		return diags
	case "hierarchical":
		// All tasks implicitly included
		return diags
	case "custom":
		if wf.Execution.Tasks == nil {
			// All tasks are orphans
			for id := range wf.Tasks {
				diags = append(diags, errDiag("AT-009", "ORPHAN_TASK",
					fmt.Sprintf("Task %q is not referenced in execution block", id),
					fmt.Sprintf("tasks.%s", id)))
			}
			return diags
		}
		// Check that every task is either in execution.tasks or referenced as a dependency
		referenced := make(map[string]bool)
		for id, deps := range wf.Execution.Tasks {
			referenced[id] = true
			for _, dep := range deps.DependsOn {
				referenced[dep] = true
			}
		}
		for id := range wf.Tasks {
			if !referenced[id] {
				diags = append(diags, errDiag("AT-009", "ORPHAN_TASK",
					fmt.Sprintf("Task %q is not referenced in execution block", id),
					fmt.Sprintf("tasks.%s", id)))
			}
		}
	}

	return diags
}

// extractTaskRefs finds all {{tasks.X.output}} references in a template string.
func extractTaskRefs(tmpl string) []string {
	var refs []string
	// Match patterns like {{tasks.some_task.output}} or {{tasks.some_task.something}}
	re := regexp.MustCompile(`\{\{tasks\.([a-zA-Z0-9_]+)\.[^}]+\}\}`)
	matches := re.FindAllStringSubmatch(tmpl, -1)
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) >= 2 && !seen[match[1]] {
			refs = append(refs, match[1])
			seen[match[1]] = true
		}
	}
	return refs
}

// isValidID checks if an identifier matches slug format: [a-z][a-z0-9_]*, max 64 chars.
func isValidID(id string) bool {
	if len(id) == 0 || len(id) > maxIDLength {
		return false
	}
	return idPattern.MatchString(id)
}

// errDiag is a helper to create an error-severity diagnostic.
func errDiag(code, _, message, path string) graph.Diagnostic {
	_ = strings.TrimSpace(message) // ensure no stray whitespace
	return graph.Diagnostic{
		Code:     code,
		Severity: graph.SeverityError,
		Message:  message,
		Path:     path,
	}
}

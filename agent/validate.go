package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/tool"
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
	if wf == nil {
		return []graph.Diagnostic{
			errDiag("AT-010", "MISSING_REQUIRED", "workflow is nil", ""),
		}
	}

	diags := make([]graph.Diagnostic, 0)

	diags = append(diags, validateIDFormats(wf)...)
	diags = append(diags, validateAgents(wf, registry.Global())...)
	diags = append(diags, validateTasks(wf)...)

	strategy, strategyDiags := validateExecutionStrategy(wf)
	diags = append(diags, strategyDiags...)

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

func validateIDFormats(wf *AgentWorkflow) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-012: Validate ID formats for agents and tasks.
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

	return diags
}

func validateAgents(wf *AgentWorkflow, reg *registry.Registry) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	for id, ag := range wf.Agents {
		path := fmt.Sprintf("agents.%s", id)
		diags = append(diags, validateAgentRequiredFields(id, path, ag)...)

		toolRefsByName, toolDiags := validateAgentTools(id, path, ag.Tools, reg)
		diags = append(diags, toolDiags...)
		diags = append(diags, validateAgentToolConfig(id, path, ag.ToolConfig, toolRefsByName, reg)...)
	}

	return diags
}

func validateAgentRequiredFields(id string, path string, ag Agent) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-010: Check required fields on agents.
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

	// AT-002: Validate provider.
	if ag.Provider != "" && !validProviders[ag.Provider] {
		diags = append(diags, errDiag("AT-002", "INVALID_PROVIDER",
			fmt.Sprintf("Agent %q has invalid provider %q", id, ag.Provider), path+".provider"))
	}

	return diags
}

func validateAgentTools(
	agentID string,
	path string,
	tools []string,
	reg *registry.Registry,
) (map[string][]string, []graph.Diagnostic) {
	toolRefsByName := make(map[string][]string)
	diags := make([]graph.Diagnostic, 0)

	// AT-004: Validate tools exist in registry.
	for i, toolID := range tools {
		toolRef := strings.TrimSpace(toolID)
		toolName, actionName, hasAction, valid := parseToolReference(toolRef)

		if hasAction && !valid {
			diags = append(diags, errDiag("AT-004", "INVALID_TOOL_REFERENCE",
				fmt.Sprintf("Agent %q tool reference %q must use tool_name.action_name", agentID, toolID),
				fmt.Sprintf("%s.tools[%d]", path, i)))
			continue
		}

		if hasAction {
			def, ok := reg.Get(toolRef)
			if !ok || !def.IsTool {
				if hasAnyActionForTool(reg, toolName) {
					diags = append(diags, errDiag("AT-004", "UNKNOWN_ACTION",
						fmt.Sprintf("Agent %q references unknown action %q on tool %q", agentID, actionName, toolName),
						fmt.Sprintf("%s.tools[%d]", path, i)))
				} else {
					diags = append(diags, errDiag("AT-004", "UNKNOWN_TOOL",
						fmt.Sprintf("Agent %q references unknown tool %q", agentID, toolName),
						fmt.Sprintf("%s.tools[%d]", path, i)))
				}
				continue
			}
			toolRefsByName[toolName] = append(toolRefsByName[toolName], toolRef)
			continue
		}

		if reg.HasTool(toolRef) {
			toolRefsByName[toolRef] = append(toolRefsByName[toolRef], toolRef)
			continue
		}

		actionRefs := toolActionReferencesForTool(reg, toolRef)
		if len(actionRefs) > 0 {
			toolRefsByName[toolRef] = append(toolRefsByName[toolRef], actionRefs...)
			continue
		}

		if !reg.HasTool(toolRef) {
			diags = append(diags, errDiag("AT-004", "UNKNOWN_TOOL",
				fmt.Sprintf("Agent %q references unknown tool %q", agentID, toolID),
				fmt.Sprintf("%s.tools[%d]", path, i)))
			continue
		}
	}

	return toolRefsByName, diags
}

func validateAgentToolConfig(
	agentID string,
	path string,
	toolConfig map[string]map[string]any,
	toolRefsByName map[string][]string,
	reg *registry.Registry,
) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-004: Validate agent-level tool_config overrides against declared fields.
	for toolName, overrides := range toolConfig {
		if len(overrides) == 0 {
			continue
		}

		referenced := toolRefsByName[toolName]
		if len(referenced) == 0 && reg.HasTool(toolName) {
			referenced = []string{toolName}
		}
		if len(referenced) == 0 {
			diags = append(diags, errDiag("AT-004", "UNKNOWN_TOOL",
				fmt.Sprintf("Agent %q tool_config references unknown tool %q", agentID, toolName),
				fmt.Sprintf("%s.tool_config.%s", path, toolName)))
			continue
		}

		allowedKeys := make(map[string]struct{})
		for _, ref := range referenced {
			def, ok := reg.Get(ref)
			if !ok {
				continue
			}
			for key := range extractToolConfigFieldNames(def.ConfigSchema) {
				allowedKeys[key] = struct{}{}
			}
		}

		for key := range overrides {
			if _, ok := allowedKeys[key]; ok {
				continue
			}
			diags = append(diags, errDiag("AT-004", "UNKNOWN_TOOL_CONFIG_FIELD",
				fmt.Sprintf("Agent %q tool_config field %q is not declared for tool %q", agentID, key, toolName),
				fmt.Sprintf("%s.tool_config.%s.%s", path, toolName, key)))
		}
	}

	return diags
}

func validateTasks(wf *AgentWorkflow) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-010: Check required fields on tasks.
	for id, task := range wf.Tasks {
		path := fmt.Sprintf("tasks.%s", id)
		diags = append(diags, validateTaskRequiredFields(id, path, task)...)
		diags = append(diags, validateTaskAgentReference(wf, id, path, task)...)
		diags = append(diags, validateTaskInputReferences(wf, id, path, task)...)
		diags = append(diags, validateTaskContextReferences(wf, id, path, task)...)
	}

	return diags
}

func validateTaskRequiredFields(id string, path string, task Task) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

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

	return diags
}

func validateTaskAgentReference(wf *AgentWorkflow, id string, path string, task Task) []graph.Diagnostic {
	if task.Agent == "" {
		return nil
	}
	if _, ok := wf.Agents[task.Agent]; ok {
		return nil
	}
	return []graph.Diagnostic{
		errDiag("AT-001", "UNDEFINED_AGENT",
			fmt.Sprintf("Task %q references undefined agent %q", id, task.Agent),
			path+".agent"),
	}
}

func validateTaskInputReferences(wf *AgentWorkflow, id string, path string, task Task) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-008: Validate input template references.
	for param, tmpl := range task.Inputs {
		refs := extractTaskRefs(tmpl)
		for _, ref := range refs {
			if _, ok := wf.Tasks[ref]; ok {
				continue
			}
			diags = append(diags, errDiag("AT-008", "UNRESOLVED_REF",
				fmt.Sprintf("Unresolved reference %q in task %q", tmpl, id),
				fmt.Sprintf("%s.inputs.%s", path, param)))
		}
	}

	return diags
}

func validateTaskContextReferences(wf *AgentWorkflow, id string, path string, task Task) []graph.Diagnostic {
	diags := make([]graph.Diagnostic, 0)

	// AT-008: Validate context references.
	for i, ctxRef := range task.Context {
		if _, ok := wf.Tasks[ctxRef]; ok {
			continue
		}
		diags = append(diags, errDiag("AT-008", "UNRESOLVED_REF",
			fmt.Sprintf("Task %q context references undefined task %q", id, ctxRef),
			fmt.Sprintf("%s.context[%d]", path, i)))
	}

	return diags
}

func validateExecutionStrategy(wf *AgentWorkflow) (string, []graph.Diagnostic) {
	diags := make([]graph.Diagnostic, 0)
	strategy := wf.Execution.Strategy

	// AT-005: Validate execution strategy.
	if strategy == "" {
		diags = append(diags, errDiag("AT-010", "MISSING_REQUIRED",
			"Execution strategy is required", "execution.strategy"))
		return strategy, diags
	}
	if !validStrategies[strategy] {
		diags = append(diags, errDiag("AT-005", "INVALID_STRATEGY",
			fmt.Sprintf("Invalid execution strategy %q (must be sequential, parallel, hierarchical, or custom)", strategy),
			"execution.strategy"))
	}

	return strategy, diags
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

	// AT-013: Validate condition expressions parse successfully
	exprValidator := graph.GetExprValidator()
	for id, deps := range wf.Execution.Tasks {
		if deps.Condition == "" {
			continue
		}
		if exprValidator != nil {
			if err := exprValidator(deps.Condition); err != nil {
				diags = append(diags, errDiag("AT-013", "INVALID_CONDITION",
					fmt.Sprintf("Task %q has invalid condition expression: %v", id, err),
					fmt.Sprintf("execution.tasks.%s.condition", id)))
			}
		}
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

func parseToolReference(ref string) (toolName string, actionName string, hasAction bool, valid bool) {
	if !strings.Contains(ref, ".") {
		return strings.TrimSpace(ref), "", false, true
	}

	parts := strings.SplitN(ref, ".", 2)
	toolName = strings.TrimSpace(parts[0])
	actionName = strings.TrimSpace(parts[1])
	if toolName == "" || actionName == "" {
		return toolName, actionName, true, false
	}
	return toolName, actionName, true, true
}

func hasAnyActionForTool(reg *registry.Registry, toolName string) bool {
	return len(toolActionReferencesForTool(reg, toolName)) > 0
}

func toolActionReferencesForTool(reg *registry.Registry, toolName string) []string {
	prefix := strings.TrimSpace(toolName) + "."
	if prefix == "." {
		return nil
	}

	refs := make([]string, 0)
	for _, def := range reg.All() {
		if !def.IsTool {
			continue
		}
		if strings.HasPrefix(def.Type, prefix) {
			refs = append(refs, def.Type)
		}
	}
	sort.Strings(refs)
	return refs
}

func extractToolConfigFieldNames(schema any) map[string]struct{} {
	out := make(map[string]struct{})
	switch typed := schema.(type) {
	case map[string]tool.FieldSpec:
		for key := range typed {
			out[key] = struct{}{}
		}
	case map[string]any:
		configRaw, ok := typed["tool_config"]
		if !ok {
			return out
		}

		switch config := configRaw.(type) {
		case map[string]tool.FieldSpec:
			for key := range config {
				out[key] = struct{}{}
			}
		case map[string]any:
			for key := range config {
				out[key] = struct{}{}
			}
		}
	}
	return out
}

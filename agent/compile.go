package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
)

const compilerVersion = "0.1.0"

// Compile transforms an AgentWorkflow into a GraphDefinition.
// It maps agents and tasks to graph nodes and edges based on the
// execution strategy. Tool duality is resolved via the global registry.
func Compile(wf *AgentWorkflow) (*graph.GraphDefinition, error) {
	if wf == nil {
		return nil, fmt.Errorf("workflow is nil")
	}

	gd := newCompiledGraphDefinition(wf)

	// Build nodes for each task + its assigned agent.
	// Track task start/output nodes for edge wiring.
	// taskStartNodeIDs: first node that must run for the task.
	// taskNodeIDs: final node considered the task output.
	taskStartNodeIDs := make(map[string]string)
	taskNodeIDs := make(map[string]string)

	// Deterministic iteration order over tasks
	taskIDs := sortedKeys(wf.Tasks)
	if err := compileTaskNodes(gd, wf, registry.Global(), taskIDs, taskStartNodeIDs, taskNodeIDs); err != nil {
		return nil, err
	}

	wireTaskReferenceEdges(gd, wf, taskIDs, taskStartNodeIDs, taskNodeIDs)
	if err := compileExecutionStrategy(gd, wf, taskStartNodeIDs, taskNodeIDs); err != nil {
		return nil, err
	}
	sortEdges(gd)

	return gd, nil
}

func newCompiledGraphDefinition(wf *AgentWorkflow) *graph.GraphDefinition {
	return &graph.GraphDefinition{
		ID:      wf.ID,
		Version: wf.Version,
		Metadata: map[string]string{
			"source_kind":      "agent_workflow",
			"source_version":   wf.Version,
			"compiled_at":      time.Now().UTC().Format(time.RFC3339),
			"compiler_version": compilerVersion,
		},
	}
}

func compileTaskNodes(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	reg *registry.Registry,
	taskIDs []string,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) error {
	for _, taskID := range taskIDs {
		task := wf.Tasks[taskID]
		ag, ok := wf.Agents[task.Agent]
		if !ok {
			return fmt.Errorf("task %q references undefined agent %q", taskID, task.Agent)
		}
		compileTaskNode(gd, reg, taskID, task, ag, taskStartNodeIDs, taskNodeIDs)
	}

	return nil
}

func compileTaskNode(
	gd *graph.GraphDefinition,
	reg *registry.Registry,
	taskID string,
	task Task,
	ag Agent,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) {
	nodeID := taskID + "__" + task.Agent
	taskStartNodeIDs[taskID] = nodeID
	taskNodeIDs[taskID] = nodeID

	fcTools, prevStandaloneNodeID := compileTaskTools(
		gd,
		reg,
		taskID,
		ag,
		taskStartNodeIDs,
	)
	if prevStandaloneNodeID != "" {
		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       prevStandaloneNodeID,
			SourceHandle: "output",
			Target:       nodeID,
			TargetHandle: "context",
		})
	}

	gd.Nodes = append(gd.Nodes, graph.NodeDef{
		ID:     nodeID,
		Type:   "llm_prompt",
		Config: buildTaskLLMConfig(task, ag, taskNodeIDs, fcTools),
	})
	appendTaskHumanReviewNode(gd, taskID, task, nodeID, taskNodeIDs)
}

func compileTaskTools(
	gd *graph.GraphDefinition,
	reg *registry.Registry,
	taskID string,
	ag Agent,
	taskStartNodeIDs map[string]string,
) ([]string, string) {
	var fcTools []string
	var firstStandaloneNodeID string
	var prevStandaloneNodeID string

	for _, toolID := range ag.Tools {
		toolRef := strings.TrimSpace(toolID)
		resolvedRefs := []string{toolRef}
		toolName, _, hasAction, _ := parseToolReference(toolRef)
		if !hasAction {
			if actionRefs := toolActionReferencesForTool(reg, toolName); len(actionRefs) > 0 {
				resolvedRefs = actionRefs
			}
		}

		for _, resolvedRef := range resolvedRefs {
			mode := reg.ToolMode(resolvedRef)
			if mode == "" {
				if def, ok := reg.Get(resolvedRef); ok && def.IsTool {
					mode = inferredToolMode(def)
				}
			}

			switch mode {
			case "function_call":
				fcTools = append(fcTools, resolvedRef)
			case "standalone":
				toolNodeID := taskID + "__" + resolvedRef
				gd.Nodes = append(gd.Nodes, graph.NodeDef{
					ID:     toolNodeID,
					Type:   resolvedRef,
					Config: buildStandaloneToolNodeConfig(reg, resolvedRef, ag),
				})

				if firstStandaloneNodeID == "" {
					firstStandaloneNodeID = toolNodeID
				}
				if prevStandaloneNodeID != "" {
					gd.Edges = append(gd.Edges, graph.EdgeDef{
						Source:       prevStandaloneNodeID,
						SourceHandle: "output",
						Target:       toolNodeID,
						TargetHandle: "input",
					})
				}
				prevStandaloneNodeID = toolNodeID
				taskStartNodeIDs[taskID] = firstStandaloneNodeID
			}
		}
	}

	return fcTools, prevStandaloneNodeID
}

func buildStandaloneToolNodeConfig(reg *registry.Registry, toolRef string, ag Agent) map[string]any {
	toolNodeConfig := map[string]any{}

	if def, ok := reg.Get(toolRef); ok && len(def.Ports.Inputs) > 0 {
		if argsTemplate := defaultArgsTemplate(def.Ports.Inputs); len(argsTemplate) > 0 {
			toolNodeConfig["args_template"] = argsTemplate
		}
	}

	if toolName, actionName, hasAction, valid := parseToolReference(toolRef); hasAction && valid {
		toolNodeConfig["tool_name"] = toolName
		toolNodeConfig["action_name"] = actionName
		if overrides, ok := ag.ToolConfig[toolName]; ok && len(overrides) > 0 {
			toolNodeConfig["tool_config"] = cloneAnyMap(overrides)
		}
	}

	return toolNodeConfig
}

func buildTaskLLMConfig(
	task Task,
	ag Agent,
	taskNodeIDs map[string]string,
	fcTools []string,
) map[string]any {
	config := map[string]any{
		"system_prompt":   buildSystemPrompt(ag, task),
		"prompt_template": rewriteTemplate(task.Description, taskNodeIDs),
		"provider":        ag.Provider,
		"model":           ag.Model,
	}

	if len(fcTools) > 0 {
		config["tools"] = fcTools
	}
	if len(ag.ToolConfig) > 0 {
		config["tool_config"] = cloneToolConfig(ag.ToolConfig)
	}
	if ag.Config != nil {
		if temp, ok := ag.Config["temperature"]; ok {
			config["temperature"] = temp
		}
		if maxTok, ok := ag.Config["max_tokens"]; ok {
			config["max_tokens"] = maxTok
		}
	}
	if task.OutputKey != "" {
		config["output_key"] = task.OutputKey
	}

	return config
}

func appendTaskHumanReviewNode(
	gd *graph.GraphDefinition,
	taskID string,
	task Task,
	nodeID string,
	taskNodeIDs map[string]string,
) {
	if task.Review != "human" {
		return
	}

	hitlID := nodeID + "__hitl"
	gd.Nodes = append(gd.Nodes, graph.NodeDef{
		ID:   hitlID,
		Type: "human",
		Config: map[string]any{
			"mode":    "approval",
			"task_id": taskID,
		},
	})
	gd.Edges = append(gd.Edges, graph.EdgeDef{
		Source:       nodeID,
		SourceHandle: "output",
		Target:       hitlID,
		TargetHandle: "input",
	})
	// Update taskNodeIDs to point to the hitl gate as the "output" node
	// so downstream edges connect to the hitl gate output.
	taskNodeIDs[taskID] = hitlID
}

func wireTaskReferenceEdges(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	taskIDs []string,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) {
	for _, taskID := range taskIDs {
		task := wf.Tasks[taskID]
		// If task has HITL, the actual LLM node is before the gate.
		dstNodeID := taskStartNodeIDs[taskID]
		if dstNodeID == "" {
			dstNodeID = taskID + "__" + task.Agent
		}

		for param, tmpl := range task.Inputs {
			refs := compileExtractTaskRefs(tmpl)
			for _, ref := range refs {
				srcNode, ok := taskNodeIDs[ref]
				if !ok {
					continue // validation should have caught this
				}
				gd.Edges = append(gd.Edges, graph.EdgeDef{
					Source:       srcNode,
					SourceHandle: "output",
					Target:       dstNodeID,
					TargetHandle: param,
				})
			}
		}

		// Context references also generate edges.
		for _, ctxRef := range task.Context {
			srcNode, ok := taskNodeIDs[ctxRef]
			if !ok {
				continue
			}
			gd.Edges = append(gd.Edges, graph.EdgeDef{
				Source:       srcNode,
				SourceHandle: "output",
				Target:       dstNodeID,
				TargetHandle: "context",
			})
		}
	}
}

func compileExecutionStrategy(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) error {
	switch wf.Execution.Strategy {
	case "sequential":
		return compileSequential(gd, wf, taskStartNodeIDs, taskNodeIDs)
	case "parallel":
		return compileParallel(gd, wf, taskStartNodeIDs, taskNodeIDs)
	case "hierarchical":
		return compileHierarchical(gd, wf, taskStartNodeIDs, taskNodeIDs)
	case "custom":
		return compileCustom(gd, wf, taskStartNodeIDs, taskNodeIDs)
	default:
		return fmt.Errorf("unsupported execution strategy %q", wf.Execution.Strategy)
	}
}

func sortEdges(gd *graph.GraphDefinition) {
	sort.Slice(gd.Edges, func(i, j int) bool {
		ei, ej := gd.Edges[i], gd.Edges[j]
		if ei.Source != ej.Source {
			return ei.Source < ej.Source
		}
		if ei.Target != ej.Target {
			return ei.Target < ej.Target
		}
		if ei.SourceHandle != ej.SourceHandle {
			return ei.SourceHandle < ej.SourceHandle
		}
		return ei.TargetHandle < ej.TargetHandle
	})
}

// compileSequential wires tasks in the order specified by TaskOrder.
func compileSequential(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) error {
	order := wf.Execution.TaskOrder
	if len(order) == 0 {
		return fmt.Errorf("sequential strategy requires task_order")
	}

	// Set entry to the first task's node
	gd.Entry = taskStartNodeIDs[order[0]]

	// Chain edges: task[0] -> task[1] -> task[2] -> ...
	for i := 0; i < len(order)-1; i++ {
		srcNode, ok := taskNodeIDs[order[i]]
		if !ok {
			return fmt.Errorf("task_order references unknown task %q", order[i])
		}
		if _, ok := taskStartNodeIDs[order[i+1]]; !ok {
			return fmt.Errorf("task_order references unknown task %q", order[i+1])
		}
		dstStartNode := taskStartNodeIDs[order[i+1]]

		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       srcNode,
			SourceHandle: "output",
			Target:       dstStartNode,
			TargetHandle: "input",
		})
	}

	return nil
}

// compileParallel leaves all task nodes unconnected and appends a MergeNode.
func compileParallel(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	_ map[string]string,
	taskNodeIDs map[string]string,
) error {
	mergeID := wf.ID + "__merge"
	mergeConfig := map[string]any{}
	if wf.Execution.MergeStrategy != "" {
		mergeConfig["strategy"] = wf.Execution.MergeStrategy
	}

	gd.Nodes = append(gd.Nodes, graph.NodeDef{
		ID:     mergeID,
		Type:   "merge",
		Config: mergeConfig,
	})

	// Wire each task's output node to the merge node
	taskIDs := sortedKeys(wf.Tasks)
	for _, taskID := range taskIDs {
		srcNode := taskNodeIDs[taskID]
		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       srcNode,
			SourceHandle: "output",
			Target:       mergeID,
			TargetHandle: "input",
		})
	}

	return nil
}

// compileHierarchical creates a manager agent node with edges to/from worker nodes.
func compileHierarchical(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) error {
	managerAgentID := wf.Execution.ManagerAgent
	if managerAgentID == "" {
		return fmt.Errorf("hierarchical strategy requires manager_agent")
	}

	ag, ok := wf.Agents[managerAgentID]
	if !ok {
		return fmt.Errorf("manager_agent %q is not defined in agents", managerAgentID)
	}

	managerNodeID := wf.ID + "__manager__" + managerAgentID
	gd.Nodes = append(gd.Nodes, graph.NodeDef{
		ID:   managerNodeID,
		Type: "llm_router",
		Config: map[string]any{
			"system_prompt": fmt.Sprintf("You are a %s.\n\nGoal: %s", ag.Role, ag.Goal),
			"provider":      ag.Provider,
			"model":         ag.Model,
		},
	})

	gd.Entry = managerNodeID

	// Wire manager -> each worker and worker -> manager
	taskIDs := sortedKeys(wf.Tasks)
	for _, taskID := range taskIDs {
		workerStartNode := taskStartNodeIDs[taskID]
		workerNode := taskNodeIDs[taskID]
		// Manager dispatches to worker
		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       managerNodeID,
			SourceHandle: "output",
			Target:       workerStartNode,
			TargetHandle: "input",
		})
		// Worker reports back to manager
		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       workerNode,
			SourceHandle: "output",
			Target:       managerNodeID,
			TargetHandle: "input",
		})
	}

	return nil
}

// compileCustom wires edges based on depends_on declarations.
// When a task dependency has a condition expression, a conditional node is
// inserted between the source and destination to enable branching.
func compileCustom(
	gd *graph.GraphDefinition,
	wf *AgentWorkflow,
	taskStartNodeIDs map[string]string,
	taskNodeIDs map[string]string,
) error {
	if wf.Execution.Tasks == nil {
		// No explicit dependencies — all tasks are independent entry points
		return nil
	}

	// Group tasks by their dependencies to detect when multiple tasks share
	// a source and use conditions — these produce a single conditional node.
	// condKey -> list of (taskID, condition)
	type condTarget struct {
		taskID    string
		condition string
	}
	condGroups := make(map[string][]condTarget) // key: sorted depIDs

	execTaskIDs := sortedKeys(wf.Execution.Tasks)

	for _, taskID := range execTaskIDs {
		deps := wf.Execution.Tasks[taskID]
		if deps.Condition == "" {
			continue
		}
		// Build a key from the dependency set
		for _, depID := range deps.DependsOn {
			key := depID + "__cond__" + taskID
			condGroups[key] = append(condGroups[key], condTarget{
				taskID:    taskID,
				condition: deps.Condition,
			})
		}
	}

	for _, taskID := range execTaskIDs {
		deps := wf.Execution.Tasks[taskID]
		dstNode, ok := taskNodeIDs[taskID]
		if !ok {
			continue
		}
		dstStartNode := taskStartNodeIDs[taskID]

		if deps.Condition != "" {
			// Wire through conditional nodes
			for _, depID := range deps.DependsOn {
				srcNode, ok := taskNodeIDs[depID]
				if !ok {
					continue
				}

				condNodeID := depID + "__cond__" + taskID
				conditionTarget := dstStartNode

				// Check if this conditional node already exists
				condExists := false
				for _, n := range gd.Nodes {
					if n.ID == condNodeID {
						condExists = true
						break
					}
				}

				if !condExists {
					// Rewrite condition expression to use envelope var names
					rewrittenExpr := rewriteConditionExpr(deps.Condition, taskNodeIDs)

					gd.Nodes = append(gd.Nodes, graph.NodeDef{
						ID:   condNodeID,
						Type: "conditional",
						Config: map[string]any{
							"conditions": []any{
								map[string]any{
									"name":       conditionTarget,
									"expression": rewrittenExpr,
								},
							},
							"default":          "_skip",
							"evaluation_order": "first_match",
							"pass_through":     true,
						},
					})
				}

				// Wire: src -> conditional
				gd.Edges = append(gd.Edges, graph.EdgeDef{
					Source:       srcNode,
					SourceHandle: "output",
					Target:       condNodeID,
					TargetHandle: "input",
				})

				// Wire: conditional -> dst
				gd.Edges = append(gd.Edges, graph.EdgeDef{
					Source:       condNodeID,
					SourceHandle: conditionTarget,
					Target:       dstStartNode,
					TargetHandle: "input",
				})
			}
		} else {
			// Direct wiring (no condition)
			for _, depID := range deps.DependsOn {
				srcNode, ok := taskNodeIDs[depID]
				if !ok {
					continue
				}
				gd.Edges = append(gd.Edges, graph.EdgeDef{
					Source:       srcNode,
					SourceHandle: "output",
					Target:       dstStartNode,
					TargetHandle: "input",
				})
			}
		}
		_ = dstNode // used for HITL-aware node lookup
	}

	// Set entry to the first task with no depends_on (i.e. a root in the DAG).
	taskIDs := sortedKeys(wf.Tasks)
	for _, taskID := range taskIDs {
		deps, hasDeps := wf.Execution.Tasks[taskID]
		if !hasDeps || len(deps.DependsOn) == 0 {
			gd.Entry = taskStartNodeIDs[taskID]
			break
		}
	}

	_ = condGroups // reserved for future multi-branch merging

	return nil
}

// rewriteConditionExpr rewrites agent-schema condition expressions into
// expressions that reference envelope variable names.
//
// Rewrites:
//
//	tasks.TASK.output.FIELD → NODEID_output.FIELD
//	tasks.TASK.output       → NODEID_output
func rewriteConditionExpr(expr string, taskNodeIDs map[string]string) string {
	return condExprPattern.ReplaceAllStringFunc(expr, func(match string) string {
		sub := condExprPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		taskID := sub[1]
		nodeID, ok := taskNodeIDs[taskID]
		if !ok {
			return match
		}
		rest := sub[2] // e.g. ".confidence" or ""
		return nodeID + "_output" + rest
	})
}

var condExprPattern = regexp.MustCompile(`tasks\.([a-zA-Z0-9_]+)\.output(\.[a-zA-Z0-9_.]*)?`)

// buildSystemPrompt constructs the system prompt for an LLM node from agent and task fields.
func buildSystemPrompt(ag Agent, task Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are a %s.", ag.Role))
	sb.WriteString(fmt.Sprintf("\n\nGoal: %s", ag.Goal))
	if ag.Backstory != "" {
		sb.WriteString(fmt.Sprintf("\n\nBackstory: %s", ag.Backstory))
	}
	sb.WriteString(fmt.Sprintf("\n\nExpected output: %s", task.ExpectedOutput))
	return sb.String()
}

// rewriteTemplate converts agent-schema template placeholders into valid
// Go text/template syntax that matches the runtime envelope data layout.
//
// Rewrites:
//
//	{{input.X}}            → {{.X}}               (input vars are in the flat Vars map)
//	{{tasks.TASK.output}}  → {{.NODEID_output}}   (using the compiled node ID)
func rewriteTemplate(tmpl string, taskNodeIDs map[string]string) string {
	// {{input.FIELD}} → {{.FIELD}}
	s := inputRefPattern.ReplaceAllString(tmpl, "{{.${1}}}")

	// {{tasks.TASKID.output}} → {{.NODEID_output}}
	s = taskRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := taskRefPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		taskID := sub[1]
		nodeID, ok := taskNodeIDs[taskID]
		if !ok {
			return match // leave unresolved; validation catches it
		}
		return "{{." + nodeID + "_output}}"
	})

	return s
}

var (
	inputRefPattern = regexp.MustCompile(`\{\{input\.([a-zA-Z0-9_.]+)\}\}`)
	taskRefPattern  = regexp.MustCompile(`\{\{tasks\.([a-zA-Z0-9_]+)\.output\}\}`)
)

// compileExtractTaskRefs finds all {{tasks.X.output}} references in a template string.
var compileRefPattern = regexp.MustCompile(`\{\{tasks\.([a-zA-Z0-9_]+)\.[^}]+\}\}`)

func compileExtractTaskRefs(tmpl string) []string {
	var refs []string
	matches := compileRefPattern.FindAllStringSubmatch(tmpl, -1)
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) >= 2 && !seen[match[1]] {
			refs = append(refs, match[1])
			seen[match[1]] = true
		}
	}
	return refs
}

func inferredToolMode(def registry.NodeTypeDef) string {
	if strings.TrimSpace(def.ToolMode) != "" {
		return def.ToolMode
	}
	if hasBytesPort(def.Ports.Inputs) || hasBytesPort(def.Ports.Outputs) {
		return "standalone"
	}
	return "function_call"
}

func hasBytesPort(ports []registry.PortDef) bool {
	for _, port := range ports {
		if port.Type == "bytes" {
			return true
		}
	}
	return false
}

func defaultArgsTemplate(inputs []registry.PortDef) map[string]string {
	if len(inputs) == 0 {
		return nil
	}

	out := make(map[string]string, len(inputs))
	for _, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			continue
		}
		out[name] = name
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneToolConfig(in map[string]map[string]any) map[string]map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(in))
	for toolName, fields := range in {
		out[toolName] = cloneAnyMap(fields)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// sortedKeys returns the keys of a map in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

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

	gd := &graph.GraphDefinition{
		ID:      wf.ID,
		Version: wf.Version,
		Metadata: map[string]string{
			"source_kind":      "agent_workflow",
			"source_version":   wf.Version,
			"compiled_at":      time.Now().UTC().Format(time.RFC3339),
			"compiler_version": compilerVersion,
		},
	}

	reg := registry.Global()

	// Build nodes for each task + its assigned agent.
	// Track task->nodeID mapping for edge wiring.
	taskNodeIDs := make(map[string]string)

	// Deterministic iteration order over tasks
	taskIDs := sortedKeys(wf.Tasks)

	for _, taskID := range taskIDs {
		task := wf.Tasks[taskID]
		ag, ok := wf.Agents[task.Agent]
		if !ok {
			return nil, fmt.Errorf("task %q references undefined agent %q", taskID, task.Agent)
		}

		nodeID := taskID + "__" + task.Agent
		taskNodeIDs[taskID] = nodeID

		// Build system prompt from agent fields
		systemPrompt := buildSystemPrompt(ag, task)

		// Separate tools by mode
		var fcTools []string
		for _, toolID := range ag.Tools {
			mode := reg.ToolMode(toolID)
			switch mode {
			case "function_call":
				fcTools = append(fcTools, toolID)
			case "standalone":
				// Create a standalone tool node wired before the agent node
				toolNodeID := taskID + "__" + toolID
				gd.Nodes = append(gd.Nodes, graph.NodeDef{
					ID:   toolNodeID,
					Type: toolID,
				})
				gd.Edges = append(gd.Edges, graph.EdgeDef{
					Source:       toolNodeID,
					SourceHandle: "output",
					Target:       nodeID,
					TargetHandle: "context",
				})
			}
		}

		// Build config for the LLM node
		config := map[string]any{
			"system_prompt":   systemPrompt,
			"prompt_template": rewriteTemplate(task.Description, taskNodeIDs),
			"provider":        ag.Provider,
			"model":           ag.Model,
		}
		if len(fcTools) > 0 {
			config["tools"] = fcTools
		}
		if ag.Config != nil {
			if temp, ok := ag.Config["temperature"]; ok {
				config["temperature"] = temp
			}
			if maxTok, ok := ag.Config["max_tokens"]; ok {
				config["max_tokens"] = maxTok
			}
		}

		// Output key from task
		if task.OutputKey != "" {
			config["output_key"] = task.OutputKey
		}

		gd.Nodes = append(gd.Nodes, graph.NodeDef{
			ID:     nodeID,
			Type:   "llm_prompt",
			Config: config,
		})

		// Human-in-the-loop: review:"human" -> append a hitl_gate node
		if task.Review == "human" {
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
	}

	// Wire edges based on input references ({{tasks.X.output}})
	for _, taskID := range taskIDs {
		task := wf.Tasks[taskID]
		// If task has HITL, the actual LLM node is before the gate
		llmNodeID := taskID + "__" + task.Agent

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
					Target:       llmNodeID,
					TargetHandle: param,
				})
			}
		}

		// Context references also generate edges
		for _, ctxRef := range task.Context {
			srcNode, ok := taskNodeIDs[ctxRef]
			if !ok {
				continue
			}
			gd.Edges = append(gd.Edges, graph.EdgeDef{
				Source:       srcNode,
				SourceHandle: "output",
				Target:       llmNodeID,
				TargetHandle: "context",
			})
		}
	}

	// Wire execution strategy edges
	switch wf.Execution.Strategy {
	case "sequential":
		if err := compileSequential(gd, wf, taskNodeIDs); err != nil {
			return nil, err
		}
	case "parallel":
		if err := compileParallel(gd, wf, taskNodeIDs); err != nil {
			return nil, err
		}
	case "hierarchical":
		if err := compileHierarchical(gd, wf, taskNodeIDs); err != nil {
			return nil, err
		}
	case "custom":
		if err := compileCustom(gd, wf, taskNodeIDs); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported execution strategy %q", wf.Execution.Strategy)
	}

	return gd, nil
}

// compileSequential wires tasks in the order specified by TaskOrder.
func compileSequential(gd *graph.GraphDefinition, wf *AgentWorkflow, taskNodeIDs map[string]string) error {
	order := wf.Execution.TaskOrder
	if len(order) == 0 {
		return fmt.Errorf("sequential strategy requires task_order")
	}

	// Set entry to the first task's node
	gd.Entry = taskNodeIDs[order[0]]

	// Chain edges: task[0] -> task[1] -> task[2] -> ...
	for i := 0; i < len(order)-1; i++ {
		srcNode, ok := taskNodeIDs[order[i]]
		if !ok {
			return fmt.Errorf("task_order references unknown task %q", order[i])
		}
		if _, ok := taskNodeIDs[order[i+1]]; !ok {
			return fmt.Errorf("task_order references unknown task %q", order[i+1])
		}
		// Get the actual LLM node for the target (not HITL gate)
		dstTask := wf.Tasks[order[i+1]]
		dstLLMNode := order[i+1] + "__" + dstTask.Agent

		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       srcNode,
			SourceHandle: "output",
			Target:       dstLLMNode,
			TargetHandle: "input",
		})
	}

	return nil
}

// compileParallel leaves all task nodes unconnected and appends a MergeNode.
func compileParallel(gd *graph.GraphDefinition, wf *AgentWorkflow, taskNodeIDs map[string]string) error {
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
func compileHierarchical(gd *graph.GraphDefinition, wf *AgentWorkflow, taskNodeIDs map[string]string) error {
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
		workerNode := taskNodeIDs[taskID]
		// Manager dispatches to worker
		gd.Edges = append(gd.Edges, graph.EdgeDef{
			Source:       managerNodeID,
			SourceHandle: "output",
			Target:       workerNode,
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
func compileCustom(gd *graph.GraphDefinition, wf *AgentWorkflow, taskNodeIDs map[string]string) error {
	if wf.Execution.Tasks == nil {
		// No explicit dependencies — all tasks are independent entry points
		return nil
	}

	for taskID, deps := range wf.Execution.Tasks {
		dstNode, ok := taskNodeIDs[taskID]
		if !ok {
			continue
		}
		dstTask := wf.Tasks[taskID]
		dstLLMNode := taskID + "__" + dstTask.Agent

		for _, depID := range deps.DependsOn {
			srcNode, ok := taskNodeIDs[depID]
			if !ok {
				continue
			}
			gd.Edges = append(gd.Edges, graph.EdgeDef{
				Source:       srcNode,
				SourceHandle: "output",
				Target:       dstLLMNode,
				TargetHandle: "input",
			})
		}
		_ = dstNode // used for HITL-aware node lookup
	}

	// Set entry to nodes with no inbound strategy edges
	hasInbound := make(map[string]bool)
	for _, deps := range wf.Execution.Tasks {
		for _, dep := range deps.DependsOn {
			if nodeID, ok := taskNodeIDs[dep]; ok {
				_ = nodeID
			}
		}
	}
	// Find entry: first task with no depends_on
	taskIDs := sortedKeys(wf.Tasks)
	for _, taskID := range taskIDs {
		deps, hasDeps := wf.Execution.Tasks[taskID]
		if !hasDeps || len(deps.DependsOn) == 0 {
			if !hasInbound[taskID] {
				gd.Entry = taskNodeIDs[taskID]
				break
			}
		}
	}

	return nil
}

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

// sortedKeys returns the keys of a map in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

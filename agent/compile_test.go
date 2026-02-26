package agent

import (
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
)

// minimalWorkflow returns a valid AgentWorkflow for testing.
func minimalWorkflow() *AgentWorkflow {
	return &AgentWorkflow{
		ID:      "test_wf",
		Version: "1.0",
		Kind:    "agent_workflow",
		Agents: map[string]Agent{
			"researcher": {
				Role:     "Senior Researcher",
				Goal:     "Find relevant information",
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
			},
		},
		Tasks: map[string]Task{
			"research": {
				Description:    "Research the topic",
				Agent:          "researcher",
				ExpectedOutput: "A summary of findings",
			},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"research"},
		},
	}
}

func TestCompile_NilWorkflow(t *testing.T) {
	_, err := Compile(nil)
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error = %q, want to contain 'nil'", err.Error())
	}
}

func TestCompile_Metadata(t *testing.T) {
	wf := minimalWorkflow()
	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if gd.Kind != "graph" {
		t.Errorf("kind = %q, want %q", gd.Kind, "graph")
	}
	if gd.SchemaVersion != "1.0.0" {
		t.Errorf("schema_version = %q, want %q", gd.SchemaVersion, "1.0.0")
	}
	if gd.Metadata["source_kind"] != "agent_workflow" {
		t.Errorf("source_kind = %q, want %q", gd.Metadata["source_kind"], "agent_workflow")
	}
	if gd.Metadata["source_version"] != "1.0" {
		t.Errorf("source_version = %q, want %q", gd.Metadata["source_version"], "1.0")
	}
	if gd.Metadata["source_schema_version"] != "legacy" {
		t.Errorf("source_schema_version = %q, want %q", gd.Metadata["source_schema_version"], "legacy")
	}
	if gd.Metadata["compiler_version"] != compilerVersion {
		t.Errorf("compiler_version = %q, want %q", gd.Metadata["compiler_version"], compilerVersion)
	}
	if gd.Metadata["compiled_at"] == "" {
		t.Error("compiled_at should not be empty")
	}
}

func TestCompile_MetadataSourceSchemaVersion(t *testing.T) {
	wf := minimalWorkflow()
	wf.SchemaVersion = "1.2.3"

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if gd.Metadata["source_schema_version"] != "1.2.3" {
		t.Errorf("source_schema_version = %q, want %q", gd.Metadata["source_schema_version"], "1.2.3")
	}
}

func TestCompile_Sequential_SingleTask(t *testing.T) {
	wf := minimalWorkflow()
	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if gd.ID != "test_wf" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_wf")
	}

	// Should have exactly one llm_prompt node
	if len(gd.Nodes) != 1 {
		t.Fatalf("Nodes count = %d, want 1", len(gd.Nodes))
	}

	node := gd.Nodes[0]
	if node.ID != "research__researcher" {
		t.Errorf("Node ID = %q, want %q", node.ID, "research__researcher")
	}
	if node.Type != "llm_prompt" {
		t.Errorf("Node Type = %q, want %q", node.Type, "llm_prompt")
	}

	// Check entry
	if gd.Entry != "research__researcher" {
		t.Errorf("Entry = %q, want %q", gd.Entry, "research__researcher")
	}
}

func TestCompile_Sequential_MultipleTask(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "multi_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"researcher": {Role: "Researcher", Goal: "Research", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"writer":     {Role: "Writer", Goal: "Write", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"research": {Description: "Research topic", Agent: "researcher", ExpectedOutput: "Findings"},
			"write":    {Description: "Write report", Agent: "writer", ExpectedOutput: "Report"},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"research", "write"},
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Two LLM nodes
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	// Entry is the first task's node
	if gd.Entry != "research__researcher" {
		t.Errorf("Entry = %q, want %q", gd.Entry, "research__researcher")
	}

	// Should have a chain edge: research node -> write node
	found := false
	for _, e := range gd.Edges {
		if e.Source == "research__researcher" && e.Target == "write__writer" {
			found = true
			if e.SourceHandle != "output" {
				t.Errorf("edge sourceHandle = %q, want %q", e.SourceHandle, "output")
			}
			if e.TargetHandle != "input" {
				t.Errorf("edge targetHandle = %q, want %q", e.TargetHandle, "input")
			}
		}
	}
	if !found {
		t.Error("expected sequential chain edge from research to write")
	}
}

func TestCompile_Parallel(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "par_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"a1": {Role: "Agent 1", Goal: "Goal 1", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"a2": {Role: "Agent 2", Goal: "Goal 2", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"task1": {Description: "Task 1", Agent: "a1", ExpectedOutput: "Output 1"},
			"task2": {Description: "Task 2", Agent: "a2", ExpectedOutput: "Output 2"},
		},
		Execution: ExecutionConfig{
			Strategy:      "parallel",
			MergeStrategy: "concat",
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Two task nodes + one merge node
	if len(gd.Nodes) != 3 {
		t.Fatalf("Nodes count = %d, want 3", len(gd.Nodes))
	}

	// Find merge node
	var mergeNode *graph.NodeDef
	for i := range gd.Nodes {
		if gd.Nodes[i].Type == "merge" {
			mergeNode = &gd.Nodes[i]
			break
		}
	}
	if mergeNode == nil {
		t.Fatal("expected a merge node")
	}
	if mergeNode.ID != "par_wf__merge" {
		t.Errorf("Merge node ID = %q, want %q", mergeNode.ID, "par_wf__merge")
	}
	if mergeNode.Config["strategy"] != "concat" {
		t.Errorf("Merge strategy = %v, want %q", mergeNode.Config["strategy"], "concat")
	}

	// Each task node should have an edge to the merge node
	mergeEdges := 0
	for _, e := range gd.Edges {
		if e.Target == "par_wf__merge" {
			mergeEdges++
		}
	}
	if mergeEdges != 2 {
		t.Errorf("edges to merge node = %d, want 2", mergeEdges)
	}
}

func TestCompile_Hierarchical(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "hier_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"manager": {Role: "Manager", Goal: "Coordinate", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"worker":  {Role: "Worker", Goal: "Execute", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"work": {Description: "Do work", Agent: "worker", ExpectedOutput: "Results"},
		},
		Execution: ExecutionConfig{
			Strategy:     "hierarchical",
			ManagerAgent: "manager",
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// One task node + one manager node
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	// Manager node
	var managerNode *graph.NodeDef
	for i := range gd.Nodes {
		if gd.Nodes[i].Type == "llm_router" {
			managerNode = &gd.Nodes[i]
			break
		}
	}
	if managerNode == nil {
		t.Fatal("expected a manager (llm_router) node")
	}
	if gd.Entry != managerNode.ID {
		t.Errorf("Entry = %q, want %q", gd.Entry, managerNode.ID)
	}

	// Should have bidirectional edges: manager <-> worker
	var managerToWorker, workerToManager bool
	workerNodeID := "work__worker"
	for _, e := range gd.Edges {
		if e.Source == managerNode.ID && e.Target == workerNodeID {
			managerToWorker = true
		}
		if e.Source == workerNodeID && e.Target == managerNode.ID {
			workerToManager = true
		}
	}
	if !managerToWorker {
		t.Error("expected edge from manager to worker")
	}
	if !workerToManager {
		t.Error("expected edge from worker to manager")
	}
}

func TestCompile_Hierarchical_MissingManager(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "hier_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"worker": {Role: "Worker", Goal: "Execute", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"work": {Description: "Do work", Agent: "worker", ExpectedOutput: "Results"},
		},
		Execution: ExecutionConfig{
			Strategy:     "hierarchical",
			ManagerAgent: "",
		},
	}

	_, err := Compile(wf)
	if err == nil {
		t.Fatal("expected error for missing manager_agent")
	}
}

func TestCompile_Custom(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "custom_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"a1": {Role: "Agent 1", Goal: "Goal 1", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"a2": {Role: "Agent 2", Goal: "Goal 2", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"a3": {Role: "Agent 3", Goal: "Goal 3", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"task_a": {Description: "Task A", Agent: "a1", ExpectedOutput: "A"},
			"task_b": {Description: "Task B", Agent: "a2", ExpectedOutput: "B"},
			"task_c": {Description: "Task C", Agent: "a3", ExpectedOutput: "C"},
		},
		Execution: ExecutionConfig{
			Strategy: "custom",
			Tasks: map[string]TaskDependencies{
				"task_a": {DependsOn: []string{}},
				"task_b": {DependsOn: []string{"task_a"}},
				"task_c": {DependsOn: []string{"task_a", "task_b"}},
			},
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Three task nodes
	if len(gd.Nodes) != 3 {
		t.Fatalf("Nodes count = %d, want 3", len(gd.Nodes))
	}

	// Entry should be task_a (no dependencies)
	if gd.Entry != "task_a__a1" {
		t.Errorf("Entry = %q, want %q", gd.Entry, "task_a__a1")
	}

	// Check edges from depends_on
	edgeExists := func(src, dst string) bool {
		for _, e := range gd.Edges {
			if e.Source == src && e.Target == dst {
				return true
			}
		}
		return false
	}

	// task_b depends on task_a
	if !edgeExists("task_a__a1", "task_b__a2") {
		t.Error("expected edge from task_a to task_b")
	}
	// task_c depends on task_a and task_b
	if !edgeExists("task_a__a1", "task_c__a3") {
		t.Error("expected edge from task_a to task_c")
	}
	if !edgeExists("task_b__a2", "task_c__a3") {
		t.Error("expected edge from task_b to task_c")
	}
}

func TestCompile_SystemPrompt(t *testing.T) {
	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:      "Senior Researcher",
		Goal:      "Find relevant information",
		Backstory: "Has 20 years of experience",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4-20250514",
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	node := gd.Nodes[0]
	sp, ok := node.Config["system_prompt"].(string)
	if !ok {
		t.Fatal("system_prompt missing from config")
	}

	if !strings.Contains(sp, "Senior Researcher") {
		t.Error("system_prompt should contain role")
	}
	if !strings.Contains(sp, "Find relevant information") {
		t.Error("system_prompt should contain goal")
	}
	if !strings.Contains(sp, "20 years of experience") {
		t.Error("system_prompt should contain backstory")
	}
	if !strings.Contains(sp, "A summary of findings") {
		t.Error("system_prompt should contain expected_output")
	}
}

func TestCompile_SystemPrompt_NoBackstory(t *testing.T) {
	wf := minimalWorkflow()
	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	sp := gd.Nodes[0].Config["system_prompt"].(string)
	if strings.Contains(sp, "Backstory") {
		t.Error("system_prompt should not contain Backstory when empty")
	}
}

func TestCompile_AgentConfig(t *testing.T) {
	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Config: map[string]any{
			"temperature": 0.7,
			"max_tokens":  4096,
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	node := gd.Nodes[0]
	if node.Config["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", node.Config["temperature"])
	}
	if node.Config["max_tokens"] != 4096 {
		t.Errorf("max_tokens = %v, want 4096", node.Config["max_tokens"])
	}
	if node.Config["provider"] != "anthropic" {
		t.Errorf("provider = %v, want %q", node.Config["provider"], "anthropic")
	}
	if node.Config["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("model = %v, want %q", node.Config["model"], "claude-sonnet-4-20250514")
	}
}

func TestCompile_ToolDuality_FunctionCall(t *testing.T) {
	// Register a function_call tool
	reg := registry.Global()
	_ = reg // tools are registered via builtins; we need a function_call one
	// The global registry has "tool" as standalone. Let's register a function_call tool.
	registry.Global().Register(registry.NodeTypeDef{
		Type:        "web_search",
		IsTool:      true,
		ToolMode:    "function_call",
		Category:    "tool",
		DisplayName: "Web Search",
		Description: "Search the web",
		Ports: registry.PortSchema{
			Inputs:  []registry.PortDef{{Name: "input", Type: "string", Required: true}},
			Outputs: []registry.PortDef{{Name: "output", Type: "string"}},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"web_search"},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Should have exactly 1 node (the LLM node with tools in config)
	if len(gd.Nodes) != 1 {
		t.Fatalf("Nodes count = %d, want 1", len(gd.Nodes))
	}

	tools, ok := gd.Nodes[0].Config["tools"].([]string)
	if !ok {
		t.Fatal("tools config should be []string")
	}
	if len(tools) != 1 || tools[0] != "web_search" {
		t.Errorf("tools = %v, want [web_search]", tools)
	}
}

func TestCompile_ToolDuality_Standalone(t *testing.T) {
	// Register a standalone tool
	registry.Global().Register(registry.NodeTypeDef{
		Type:        "pdf_extract",
		IsTool:      true,
		ToolMode:    "standalone",
		Category:    "tool",
		DisplayName: "PDF Extract",
		Description: "Extract text from PDF",
		Ports: registry.PortSchema{
			Inputs:  []registry.PortDef{{Name: "input", Type: "object", Required: true}},
			Outputs: []registry.PortDef{{Name: "output", Type: "object"}},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"pdf_extract"},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Should have 2 nodes: standalone tool + LLM node
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	// First node should be the standalone tool
	toolNode := gd.Nodes[0]
	if toolNode.ID != "research__pdf_extract" {
		t.Errorf("Tool node ID = %q, want %q", toolNode.ID, "research__pdf_extract")
	}
	if toolNode.Type != "pdf_extract" {
		t.Errorf("Tool node Type = %q, want %q", toolNode.Type, "pdf_extract")
	}

	// There should be an edge from tool -> LLM node
	found := false
	for _, e := range gd.Edges {
		if e.Source == "research__pdf_extract" && e.Target == "research__researcher" {
			found = true
			if e.SourceHandle != "output" {
				t.Errorf("edge sourceHandle = %q, want %q", e.SourceHandle, "output")
			}
			if e.TargetHandle != "context" {
				t.Errorf("edge targetHandle = %q, want %q", e.TargetHandle, "context")
			}
		}
	}
	if !found {
		t.Error("expected edge from standalone tool to LLM node")
	}

	// LLM node should NOT have tools in config (standalone tools are separate nodes)
	llmNode := gd.Nodes[1]
	if _, ok := llmNode.Config["tools"]; ok {
		t.Error("LLM node should not have tools config for standalone tools")
	}
}

func TestCompile_InputReferences(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "ref_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"researcher": {Role: "Researcher", Goal: "Research", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"writer":     {Role: "Writer", Goal: "Write", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"research": {Description: "Research topic", Agent: "researcher", ExpectedOutput: "Findings"},
			"write": {
				Description:    "Write report",
				Agent:          "writer",
				ExpectedOutput: "Report",
				Inputs: map[string]string{
					"research_data": "{{tasks.research.output}}",
				},
			},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"research", "write"},
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Should have an input reference edge
	found := false
	for _, e := range gd.Edges {
		if e.Source == "research__researcher" && e.Target == "write__writer" && e.TargetHandle == "research_data" {
			found = true
		}
	}
	if !found {
		t.Error("expected input reference edge from research to write with handle 'research_data'")
	}
}

func TestCompile_ContextReferences(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "ctx_wf",
		Version: "1.0",
		Agents: map[string]Agent{
			"researcher": {Role: "Researcher", Goal: "Research", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"writer":     {Role: "Writer", Goal: "Write", Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		},
		Tasks: map[string]Task{
			"research": {Description: "Research topic", Agent: "researcher", ExpectedOutput: "Findings"},
			"write": {
				Description:    "Write report",
				Agent:          "writer",
				ExpectedOutput: "Report",
				Context:        []string{"research"},
			},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"research", "write"},
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Should have a context edge
	found := false
	for _, e := range gd.Edges {
		if e.Source == "research__researcher" && e.Target == "write__writer" && e.TargetHandle == "context" {
			found = true
		}
	}
	if !found {
		t.Error("expected context edge from research to write with handle 'context'")
	}
}

func TestCompile_HITL(t *testing.T) {
	wf := minimalWorkflow()
	wf.Tasks["research"] = Task{
		Description:    "Research topic",
		Agent:          "researcher",
		ExpectedOutput: "Findings",
		Review:         "human",
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Should have 2 nodes: LLM + HITL gate
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	// Find HITL node
	var hitlNode *graph.NodeDef
	for i := range gd.Nodes {
		if gd.Nodes[i].Type == "human" {
			hitlNode = &gd.Nodes[i]
			break
		}
	}
	if hitlNode == nil {
		t.Fatal("expected a human (HITL) node")
	}

	expectedHITLID := "research__researcher__hitl"
	if hitlNode.ID != expectedHITLID {
		t.Errorf("HITL node ID = %q, want %q", hitlNode.ID, expectedHITLID)
	}

	// Edge from LLM to HITL
	found := false
	for _, e := range gd.Edges {
		if e.Source == "research__researcher" && e.Target == expectedHITLID {
			found = true
		}
	}
	if !found {
		t.Error("expected edge from LLM node to HITL gate")
	}

	// Entry should be the task start node so the LLM runs before HITL review.
	if gd.Entry != "research__researcher" {
		t.Errorf("Entry = %q, want %q", gd.Entry, "research__researcher")
	}
}

func TestCompile_OutputKey(t *testing.T) {
	wf := minimalWorkflow()
	wf.Tasks["research"] = Task{
		Description:    "Research topic",
		Agent:          "researcher",
		ExpectedOutput: "Findings",
		OutputKey:      "research_result",
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if gd.Nodes[0].Config["output_key"] != "research_result" {
		t.Errorf("output_key = %v, want %q", gd.Nodes[0].Config["output_key"], "research_result")
	}
}

func TestCompile_UndefinedAgent(t *testing.T) {
	wf := &AgentWorkflow{
		ID:      "bad_wf",
		Version: "1.0",
		Agents:  map[string]Agent{},
		Tasks: map[string]Task{
			"task1": {Description: "Do stuff", Agent: "nonexistent", ExpectedOutput: "Stuff"},
		},
		Execution: ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"task1"},
		},
	}

	_, err := Compile(wf)
	if err == nil {
		t.Fatal("expected error for undefined agent reference")
	}
	if !strings.Contains(err.Error(), "undefined agent") {
		t.Errorf("error = %q, want to contain 'undefined agent'", err.Error())
	}
}

func TestCompile_UnsupportedStrategy(t *testing.T) {
	wf := minimalWorkflow()
	wf.Execution.Strategy = "magic"

	_, err := Compile(wf)
	if err == nil {
		t.Fatal("expected error for unsupported strategy")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error = %q, want to contain 'unsupported'", err.Error())
	}
}

func TestCompile_Sequential_MissingTaskOrder(t *testing.T) {
	wf := minimalWorkflow()
	wf.Execution.TaskOrder = nil

	_, err := Compile(wf)
	if err == nil {
		t.Fatal("expected error for missing task_order")
	}
}

func TestCompile_PromptTemplate(t *testing.T) {
	wf := minimalWorkflow()
	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	pt := gd.Nodes[0].Config["prompt_template"]
	if pt != "Research the topic" {
		t.Errorf("prompt_template = %v, want %q", pt, "Research the topic")
	}
}

func TestCompile_MixedTools(t *testing.T) {
	// Register both tool modes
	registry.Global().Register(registry.NodeTypeDef{
		Type:        "search_api",
		IsTool:      true,
		ToolMode:    "function_call",
		Category:    "tool",
		DisplayName: "Search API",
		Description: "API search",
		Ports: registry.PortSchema{
			Outputs: []registry.PortDef{{Name: "output", Type: "string"}},
		},
	})
	registry.Global().Register(registry.NodeTypeDef{
		Type:        "data_loader",
		IsTool:      true,
		ToolMode:    "standalone",
		Category:    "tool",
		DisplayName: "Data Loader",
		Description: "Load data",
		Ports: registry.PortSchema{
			Outputs: []registry.PortDef{{Name: "output", Type: "object"}},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"search_api", "data_loader"},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// 1 standalone tool node + 1 LLM node = 2 nodes
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	// LLM node should have search_api in tools config
	var llmNode graph.NodeDef
	for _, n := range gd.Nodes {
		if n.Type == "llm_prompt" {
			llmNode = n
			break
		}
	}
	tools, ok := llmNode.Config["tools"].([]string)
	if !ok || len(tools) != 1 || tools[0] != "search_api" {
		t.Errorf("tools = %v, want [search_api]", tools)
	}

	// data_loader should be a standalone node
	var standaloneNode graph.NodeDef
	for _, n := range gd.Nodes {
		if n.Type == "data_loader" {
			standaloneNode = n
			break
		}
	}
	if standaloneNode.ID != "research__data_loader" {
		t.Errorf("standalone node ID = %q, want %q", standaloneNode.ID, "research__data_loader")
	}
}

func TestCompile_ToolActionReferencesAndToolConfig(t *testing.T) {
	registry.Global().Register(registry.NodeTypeDef{
		Type:     "s3_fetch.list",
		Category: "tool",
		IsTool:   true,
		ToolMode: "function_call",
		ConfigSchema: map[string]any{
			"tool_config": map[string]any{
				"region": map[string]any{"type": "string"},
			},
		},
	})
	registry.Global().Register(registry.NodeTypeDef{
		Type:     "s3_fetch.download",
		Category: "tool",
		IsTool:   true,
		ToolMode: "standalone",
		ConfigSchema: map[string]any{
			"tool_config": map[string]any{
				"region": map[string]any{"type": "string"},
			},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"s3_fetch.list", "s3_fetch.download"},
		ToolConfig: map[string]map[string]any{
			"s3_fetch": {
				"region": "us-west-2",
			},
		},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	var llmNode graph.NodeDef
	var standalone graph.NodeDef
	for _, node := range gd.Nodes {
		switch node.Type {
		case "llm_prompt":
			llmNode = node
		case "s3_fetch.download":
			standalone = node
		}
	}

	tools, ok := llmNode.Config["tools"].([]string)
	if !ok {
		t.Fatalf("LLM tools config type = %T, want []string", llmNode.Config["tools"])
	}
	if len(tools) != 1 || tools[0] != "s3_fetch.list" {
		t.Fatalf("LLM tools = %#v, want [s3_fetch.list]", tools)
	}
	if _, ok := llmNode.Config["tool_config"].(map[string]map[string]any); !ok {
		t.Fatalf("LLM tool_config type = %T, want map[string]map[string]any", llmNode.Config["tool_config"])
	}

	if standalone.ID != "research__s3_fetch.download" {
		t.Fatalf("standalone ID = %q, want research__s3_fetch.download", standalone.ID)
	}
	if standalone.Config["tool_name"] != "s3_fetch" {
		t.Fatalf("standalone tool_name = %v, want s3_fetch", standalone.Config["tool_name"])
	}
	if standalone.Config["action_name"] != "download" {
		t.Fatalf("standalone action_name = %v, want download", standalone.Config["action_name"])
	}
	toolCfg, ok := standalone.Config["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("standalone tool_config type = %T, want map[string]any", standalone.Config["tool_config"])
	}
	if toolCfg["region"] != "us-west-2" {
		t.Fatalf("standalone tool_config.region = %v, want us-west-2", toolCfg["region"])
	}

	if gd.Entry != "research__s3_fetch.download" {
		t.Fatalf("Entry = %q, want research__s3_fetch.download", gd.Entry)
	}
}

func TestCompile_StandaloneToolDefaultArgsTemplate(t *testing.T) {
	registry.Global().Register(registry.NodeTypeDef{
		Type:     "http_fetch.fetch",
		Category: "tool",
		IsTool:   true,
		ToolMode: "standalone",
		Ports: registry.PortSchema{
			Inputs: []registry.PortDef{
				{Name: "url", Type: "string", Required: true},
				{Name: "method", Type: "string"},
			},
			Outputs: []registry.PortDef{
				{Name: "output", Type: "object"},
			},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"http_fetch.fetch"},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	var standalone graph.NodeDef
	for _, node := range gd.Nodes {
		if node.Type == "http_fetch.fetch" {
			standalone = node
			break
		}
	}
	if standalone.ID == "" {
		t.Fatal("expected standalone node for http_fetch.fetch")
	}

	argsTemplate, ok := standalone.Config["args_template"].(map[string]string)
	if !ok {
		typed, ok := standalone.Config["args_template"].(map[string]any)
		if !ok {
			t.Fatalf("args_template type = %T, want map[string]string or map[string]any", standalone.Config["args_template"])
		}
		argsTemplate = make(map[string]string, len(typed))
		for k, v := range typed {
			s, _ := v.(string)
			argsTemplate[k] = s
		}
	}
	if argsTemplate["url"] != "url" {
		t.Fatalf("args_template[url] = %q, want %q", argsTemplate["url"], "url")
	}
	if argsTemplate["method"] != "method" {
		t.Fatalf("args_template[method] = %q, want %q", argsTemplate["method"], "method")
	}

	if gd.Entry != standalone.ID {
		t.Fatalf("Entry = %q, want %q", gd.Entry, standalone.ID)
	}
}

func TestCompile_ToolModeInferredFromBytesPorts(t *testing.T) {
	registry.Global().Register(registry.NodeTypeDef{
		Type:     "pdf_extract.extract",
		Category: "tool",
		IsTool:   true,
		Ports: registry.PortSchema{
			Inputs: []registry.PortDef{
				{Name: "path", Type: "string", Required: true},
			},
			Outputs: []registry.PortDef{
				{Name: "blob", Type: "bytes"},
			},
		},
	})

	wf := minimalWorkflow()
	wf.Agents["researcher"] = Agent{
		Role:     "Researcher",
		Goal:     "Research",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		Tools:    []string{"pdf_extract.extract"},
	}

	gd, err := Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(gd.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(gd.Nodes))
	}

	foundStandalone := false
	for _, node := range gd.Nodes {
		if node.Type == "pdf_extract.extract" {
			foundStandalone = true
		}
	}
	if !foundStandalone {
		t.Fatal("expected standalone node for bytes-typed tool action")
	}
}

func TestCompileExtractTaskRefs(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want []string
	}{
		{"single ref", "{{tasks.research.output}}", []string{"research"}},
		{"multiple refs", "{{tasks.a.output}} and {{tasks.b.output}}", []string{"a", "b"}},
		{"no refs", "plain text", nil},
		{"duplicate refs", "{{tasks.x.output}} {{tasks.x.result}}", []string{"x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compileExtractTaskRefs(tt.tmpl)
			if len(got) != len(tt.want) {
				t.Fatalf("refs count = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ref[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRewriteTemplate(t *testing.T) {
	taskNodeIDs := map[string]string{
		"research":     "research__analyst",
		"write_report": "write_report__writer",
	}

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{
			"input ref",
			"Research {{input.topic}} thoroughly.",
			"Research {{.topic}} thoroughly.",
		},
		{
			"nested input ref",
			"Use {{input.config.model}} for this.",
			"Use {{.config.model}} for this.",
		},
		{
			"task output ref",
			"Based on {{tasks.research.output}}, write a report.",
			"Based on {{.research__analyst_output}}, write a report.",
		},
		{
			"both refs",
			"Topic: {{input.topic}}. Prior: {{tasks.research.output}}.",
			"Topic: {{.topic}}. Prior: {{.research__analyst_output}}.",
		},
		{
			"no refs",
			"Plain description with no placeholders.",
			"Plain description with no placeholders.",
		},
		{
			"unknown task ref preserved",
			"{{tasks.unknown.output}}",
			"{{tasks.unknown.output}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteTemplate(tt.tmpl, taskNodeIDs)
			if got != tt.want {
				t.Errorf("rewriteTemplate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompile_SortedKeys(t *testing.T) {
	m := map[string]int{"c": 3, "a": 1, "b": 2}
	got := sortedKeys(m)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("key[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

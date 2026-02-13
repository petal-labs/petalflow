package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/graph"
)

func TestOnboardingWorkflowSeeds_CompileWithValidNodeStructure(t *testing.T) {
	seeds := onboardingWorkflowSeeds()
	if len(seeds) == 0 {
		t.Fatal("onboarding seeds should not be empty")
	}

	allowedLLMTargetHandles := map[string]struct{}{
		"input":   {},
		"context": {},
	}

	for _, seed := range seeds {
		defBytes, err := json.Marshal(seed.Definition)
		if err != nil {
			t.Fatalf("marshal seed %q: %v", seed.ID, err)
		}

		compiled, err := compileAgentWorkflowDefinition(defBytes, seed.ID, seed.Name)
		if err != nil {
			t.Fatalf("compile seed %q: %v", seed.ID, err)
		}
		if compiled.Entry == "" {
			t.Fatalf("seed %q compiled graph should set entry", seed.ID)
		}
		if len(compiled.Nodes) == 0 {
			t.Fatalf("seed %q compiled graph should include nodes", seed.ID)
		}

		nodeTypesByID := make(map[string]string, len(compiled.Nodes))
		for _, node := range compiled.Nodes {
			if node.ID == "" {
				t.Fatalf("seed %q has compiled node with empty id", seed.ID)
			}
			if node.Type == "" {
				t.Fatalf("seed %q node %q has empty type", seed.ID, node.ID)
			}
			if _, exists := nodeTypesByID[node.ID]; exists {
				t.Fatalf("seed %q has duplicate node id %q", seed.ID, node.ID)
			}
			nodeTypesByID[node.ID] = node.Type
		}

		seenEdges := make(map[string]struct{}, len(compiled.Edges))
		for _, edge := range compiled.Edges {
			if _, ok := nodeTypesByID[edge.Source]; !ok {
				t.Fatalf("seed %q edge references unknown source node %q", seed.ID, edge.Source)
			}

			targetType, ok := nodeTypesByID[edge.Target]
			if !ok {
				t.Fatalf("seed %q edge references unknown target node %q", seed.ID, edge.Target)
			}

			if edge.SourceHandle == "" || edge.TargetHandle == "" {
				t.Fatalf(
					"seed %q edge %q -> %q should include both source/target handles",
					seed.ID,
					edge.Source,
					edge.Target,
				)
			}

			edgeKey := fmt.Sprintf("%s|%s|%s|%s", edge.Source, edge.SourceHandle, edge.Target, edge.TargetHandle)
			if _, exists := seenEdges[edgeKey]; exists {
				t.Fatalf("seed %q contains duplicate edge %s", seed.ID, edgeKey)
			}
			seenEdges[edgeKey] = struct{}{}

			if targetType == "llm_prompt" {
				if _, allowed := allowedLLMTargetHandles[edge.TargetHandle]; !allowed {
					t.Fatalf(
						"seed %q uses non-standard llm_prompt target handle %q on edge %s",
						seed.ID,
						edge.TargetHandle,
						edgeKey,
					)
				}
			}
		}
	}
}

func TestPatchLegacyOnboardingSampleWorkflows_UpdatesExistingRecords(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().Add(-5 * time.Minute).UTC()

	for _, seed := range onboardingWorkflowSeeds() {
		definition := cloneDefinitionMap(t, seed.Definition)
		for _, patch := range legacyOnboardingTaskInputPatches {
			if patch.WorkflowID != seed.ID {
				continue
			}
			if !setTaskInput(definition, patch.TaskID, patch.InputKey, patch.ExpectedTemplate) {
				t.Fatalf("seed %q is missing task %q for legacy patch setup", seed.ID, patch.TaskID)
			}
		}

		defBytes, err := json.Marshal(definition)
		if err != nil {
			t.Fatalf("marshal legacy definition %q: %v", seed.ID, err)
		}
		compiled, err := compileAgentWorkflowDefinition(defBytes, seed.ID, seed.Name)
		if err != nil {
			t.Fatalf("compile legacy definition %q: %v", seed.ID, err)
		}

		if err := store.Create(ctx, WorkflowRecord{
			ID:          seed.ID,
			SchemaKind:  "agent_workflow",
			Name:        seed.Name,
			Description: seed.Description,
			Tags:        append([]string(nil), seed.Tags...),
			Source:      json.RawMessage(defBytes),
			Compiled:    compiled,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("create seed %q: %v", seed.ID, err)
		}
	}

	_ = NewServer(ServerConfig{Store: store})

	for _, seed := range onboardingWorkflowSeeds() {
		rec, ok, err := store.Get(ctx, seed.ID)
		if err != nil {
			t.Fatalf("get patched seed %q: %v", seed.ID, err)
		}
		if !ok {
			t.Fatalf("patched seed %q not found", seed.ID)
		}
		if rec.Compiled == nil {
			t.Fatalf("patched seed %q has nil compiled graph", seed.ID)
		}
		if !rec.UpdatedAt.After(now) {
			t.Fatalf("patched seed %q should update updated_at", seed.ID)
		}

		var definition map[string]any
		if err := json.Unmarshal(rec.Source, &definition); err != nil {
			t.Fatalf("unmarshal patched source %q: %v", seed.ID, err)
		}
		for _, patch := range legacyOnboardingTaskInputPatches {
			if patch.WorkflowID != seed.ID {
				continue
			}
			if _, found := getTaskInput(definition, patch.TaskID, patch.InputKey); found {
				t.Fatalf(
					"patched seed %q still has legacy input %q on task %q",
					seed.ID,
					patch.InputKey,
					patch.TaskID,
				)
			}
		}

		assertCompiledNodeHandles(t, seed.ID, rec.Compiled)
	}
}

func TestPatchLegacyOnboardingSampleWorkflows_DoesNotOverwriteCustomInputs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seed := onboardingWorkflowSeeds()[0]
	patch := legacyOnboardingTaskInputPatches[0]

	definition := cloneDefinitionMap(t, seed.Definition)
	if !setTaskInput(definition, patch.TaskID, patch.InputKey, "{{tasks.custom.output}}") {
		t.Fatalf("seed %q missing task %q", seed.ID, patch.TaskID)
	}
	defBytes, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal custom definition: %v", err)
	}
	compiled, err := compileAgentWorkflowDefinition(defBytes, seed.ID, seed.Name)
	if err != nil {
		t.Fatalf("compile custom definition: %v", err)
	}

	now := time.Now().Add(-10 * time.Minute).UTC()
	if err := store.Create(ctx, WorkflowRecord{
		ID:         seed.ID,
		SchemaKind: "agent_workflow",
		Name:       seed.Name,
		Source:     json.RawMessage(defBytes),
		Compiled:   compiled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("create custom seed record: %v", err)
	}

	_ = NewServer(ServerConfig{Store: store})

	rec, ok, err := store.Get(ctx, seed.ID)
	if err != nil {
		t.Fatalf("get custom seed after patch: %v", err)
	}
	if !ok {
		t.Fatal("custom seed record missing")
	}

	var patched map[string]any
	if err := json.Unmarshal(rec.Source, &patched); err != nil {
		t.Fatalf("unmarshal patched custom source: %v", err)
	}
	value, found := getTaskInput(patched, patch.TaskID, patch.InputKey)
	if !found {
		t.Fatalf("expected custom input %q to remain on task %q", patch.InputKey, patch.TaskID)
	}
	if value != "{{tasks.custom.output}}" {
		t.Fatalf("custom input changed: got %q", value)
	}
	if !rec.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at changed for custom seed; got %s want %s", rec.UpdatedAt, now)
	}
}

func TestPatchLegacyOnboardingSampleWorkflows_PersistsInSQLite(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteWorkflowStore(t)
	seed := onboardingWorkflowSeeds()[0]
	patch := legacyOnboardingTaskInputPatches[0]

	definition := cloneDefinitionMap(t, seed.Definition)
	if !setTaskInput(definition, patch.TaskID, patch.InputKey, patch.ExpectedTemplate) {
		t.Fatalf("seed %q missing task %q", seed.ID, patch.TaskID)
	}
	defBytes, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal sqlite legacy definition: %v", err)
	}
	compiled, err := compileAgentWorkflowDefinition(defBytes, seed.ID, seed.Name)
	if err != nil {
		t.Fatalf("compile sqlite legacy definition: %v", err)
	}

	now := time.Now().Add(-2 * time.Minute).UTC()
	if err := store.Create(ctx, WorkflowRecord{
		ID:         seed.ID,
		SchemaKind: "agent_workflow",
		Name:       seed.Name,
		Source:     json.RawMessage(defBytes),
		Compiled:   compiled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("create sqlite sample record: %v", err)
	}

	_ = NewServer(ServerConfig{Store: store})

	rec, ok, err := store.Get(ctx, seed.ID)
	if err != nil {
		t.Fatalf("get sqlite sample after patch: %v", err)
	}
	if !ok {
		t.Fatal("sqlite sample record missing")
	}
	var patched map[string]any
	if err := json.Unmarshal(rec.Source, &patched); err != nil {
		t.Fatalf("unmarshal sqlite patched source: %v", err)
	}
	if _, found := getTaskInput(patched, patch.TaskID, patch.InputKey); found {
		t.Fatalf("sqlite sample still has legacy input %q", patch.InputKey)
	}
	if rec.Compiled == nil {
		t.Fatal("sqlite patched sample missing compiled graph")
	}
	assertCompiledNodeHandles(t, seed.ID, rec.Compiled)
}

func cloneDefinitionMap(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal clone source: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal clone source: %v", err)
	}
	return out
}

func setTaskInput(definition map[string]any, taskID, key, value string) bool {
	tasksRaw, ok := definition["tasks"]
	if !ok {
		return false
	}
	tasks, ok := tasksRaw.([]any)
	if !ok {
		return false
	}
	for i := range tasks {
		task, ok := tasks[i].(map[string]any)
		if !ok {
			continue
		}
		id, _ := task["id"].(string)
		if id != taskID {
			continue
		}
		inputs, ok := task["inputs"].(map[string]any)
		if !ok || inputs == nil {
			inputs = map[string]any{}
		}
		inputs[key] = value
		task["inputs"] = inputs
		tasks[i] = task
		definition["tasks"] = tasks
		return true
	}
	return false
}

func getTaskInput(definition map[string]any, taskID, key string) (string, bool) {
	tasksRaw, ok := definition["tasks"]
	if !ok {
		return "", false
	}
	tasks, ok := tasksRaw.([]any)
	if !ok {
		return "", false
	}
	for _, rawTask := range tasks {
		task, ok := rawTask.(map[string]any)
		if !ok {
			continue
		}
		id, _ := task["id"].(string)
		if id != taskID {
			continue
		}
		inputs, ok := task["inputs"].(map[string]any)
		if !ok || inputs == nil {
			return "", false
		}
		value, ok := inputs[key]
		if !ok {
			return "", false
		}
		text, ok := value.(string)
		return text, ok
	}
	return "", false
}

func assertCompiledNodeHandles(t *testing.T, workflowID string, compiled *graph.GraphDefinition) {
	t.Helper()
	if compiled == nil {
		t.Fatalf("workflow %q compiled graph is nil", workflowID)
	}
	nodeTypesByID := make(map[string]string, len(compiled.Nodes))
	for _, node := range compiled.Nodes {
		nodeTypesByID[node.ID] = node.Type
	}
	for _, edge := range compiled.Edges {
		if nodeTypesByID[edge.Target] != "llm_prompt" {
			continue
		}
		if edge.TargetHandle != "input" && edge.TargetHandle != "context" {
			t.Fatalf(
				"workflow %q contains unsupported llm target handle %q on %s -> %s",
				workflowID,
				edge.TargetHandle,
				edge.Source,
				edge.Target,
			)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

type onboardingWorkflowSeed struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Definition  map[string]any
}

func onboardingWorkflowSeeds() []onboardingWorkflowSeed {
	return []onboardingWorkflowSeed{
		{
			ID:          "sample_research_brief",
			Name:        "Sample: Research Brief",
			Description: "Research a topic and generate a concise brief with recommendations.",
			Tags:        []string{"sample", "starter", "onboarding"},
			Definition: map[string]any{
				"version": "1.0",
				"kind":    "agent_workflow",
				"id":      "sample_research_brief",
				"name":    "Sample: Research Brief",
				"input_schema": map[string]any{
					"type":     "object",
					"required": []string{"topic", "audience"},
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "Topic to research",
						},
						"audience": map[string]any{
							"type":        "string",
							"description": "Who this brief is for",
							"default":     "product team",
						},
					},
				},
				"default_inputs": map[string]any{
					"audience": "product team",
				},
				"agents": []map[string]any{
					{
						"id":        "researcher",
						"role":      "Research Analyst",
						"goal":      "Produce accurate and actionable summaries quickly.",
						"backstory": "You synthesize raw information into clear recommendations.",
						"provider":  "",
						"model":     "",
						"tools":     []string{},
					},
				},
				"tasks": []map[string]any{
					{
						"id":              "gather_facts",
						"description":     "Research '{{input.topic}}' and list 5 key facts with source links.",
						"agent":           "researcher",
						"expected_output": "A bullet list of factual findings with references.",
						"output_key":      "facts",
					},
					{
						"id":              "write_brief",
						"description":     "Write a brief for '{{input.audience}}' using {{tasks.gather_facts.output}}. Include Summary, Key Findings, Risks, and Next Steps.",
						"agent":           "researcher",
						"expected_output": "A concise, decision-ready brief.",
						"output_key":      "brief",
						"inputs": map[string]string{
							"facts": "{{tasks.gather_facts.output}}",
						},
					},
				},
				"execution": map[string]any{
					"strategy":   "sequential",
					"task_order": []string{"gather_facts", "write_brief"},
				},
			},
		},
		{
			ID:          "sample_meeting_actions",
			Name:        "Sample: Meeting to Action Items",
			Description: "Turn raw meeting notes into an executive summary and action list.",
			Tags:        []string{"sample", "starter", "onboarding"},
			Definition: map[string]any{
				"version": "1.0",
				"kind":    "agent_workflow",
				"id":      "sample_meeting_actions",
				"name":    "Sample: Meeting to Action Items",
				"input_schema": map[string]any{
					"type":     "object",
					"required": []string{"transcript"},
					"properties": map[string]any{
						"transcript": map[string]any{
							"type":        "string",
							"description": "Meeting transcript or notes",
							"multiline":   true,
						},
						"focus": map[string]any{
							"type":        "string",
							"description": "Optional focus area for extraction",
							"default":     "deliverables and owners",
						},
					},
				},
				"default_inputs": map[string]any{
					"focus": "deliverables and owners",
				},
				"agents": []map[string]any{
					{
						"id":        "coordinator",
						"role":      "Operations Coordinator",
						"goal":      "Extract clear next steps from messy notes.",
						"backstory": "You specialize in converting conversations into accountable plans.",
						"provider":  "",
						"model":     "",
						"tools":     []string{},
					},
				},
				"tasks": []map[string]any{
					{
						"id":              "summarize_meeting",
						"description":     "Summarize this meeting transcript:\n\n{{input.transcript}}",
						"agent":           "coordinator",
						"expected_output": "A concise summary of decisions, blockers, and open questions.",
						"output_key":      "summary",
					},
					{
						"id":              "extract_actions",
						"description":     "Using {{tasks.summarize_meeting.output}}, extract action items focused on '{{input.focus}}'. Return owner, task, and due date if mentioned.",
						"agent":           "coordinator",
						"expected_output": "A markdown table of actionable tasks.",
						"output_key":      "actions",
						"inputs": map[string]string{
							"summary": "{{tasks.summarize_meeting.output}}",
						},
					},
				},
				"execution": map[string]any{
					"strategy":   "sequential",
					"task_order": []string{"summarize_meeting", "extract_actions"},
				},
			},
		},
		{
			ID:          "sample_release_notes",
			Name:        "Sample: Release Notes Draft",
			Description: "Organize product changes and generate customer-facing release notes.",
			Tags:        []string{"sample", "starter", "onboarding"},
			Definition: map[string]any{
				"version": "1.0",
				"kind":    "agent_workflow",
				"id":      "sample_release_notes",
				"name":    "Sample: Release Notes Draft",
				"input_schema": map[string]any{
					"type":     "object",
					"required": []string{"version", "changes"},
					"properties": map[string]any{
						"version": map[string]any{
							"type":        "string",
							"description": "Release version number",
						},
						"changes": map[string]any{
							"type":        "string",
							"description": "Raw list of product changes from engineering",
							"multiline":   true,
						},
						"audience": map[string]any{
							"type":        "string",
							"description": "Target audience for notes",
							"default":     "end users",
						},
					},
				},
				"default_inputs": map[string]any{
					"audience": "end users",
				},
				"agents": []map[string]any{
					{
						"id":        "release_writer",
						"role":      "Product Communications Writer",
						"goal":      "Draft clear release notes from technical updates.",
						"backstory": "You transform engineering change logs into customer-ready messaging.",
						"provider":  "",
						"model":     "",
						"tools":     []string{},
					},
				},
				"tasks": []map[string]any{
					{
						"id":              "classify_changes",
						"description":     "Classify the following changes into Features, Improvements, and Fixes:\n\n{{input.changes}}",
						"agent":           "release_writer",
						"expected_output": "Categorized bullet list of product changes.",
						"output_key":      "categorized_changes",
					},
					{
						"id":              "draft_release_notes",
						"description":     "Draft release notes for version {{input.version}} aimed at {{input.audience}} using {{tasks.classify_changes.output}}. Include Highlights and Known Issues.",
						"agent":           "release_writer",
						"expected_output": "Polished markdown release notes ready for review.",
						"output_key":      "release_notes",
						"inputs": map[string]string{
							"changes": "{{tasks.classify_changes.output}}",
						},
					},
				},
				"execution": map[string]any{
					"strategy":   "sequential",
					"task_order": []string{"classify_changes", "draft_release_notes"},
				},
			},
		},
	}
}

func (s *Server) seedOnboardingSampleWorkflows(ctx context.Context) error {
	records, err := s.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list workflows before onboarding seed: %w", err)
	}
	if len(records) != 0 {
		return nil
	}

	now := time.Now()
	for _, seed := range onboardingWorkflowSeeds() {
		defBytes, err := json.Marshal(seed.Definition)
		if err != nil {
			return fmt.Errorf("marshal onboarding sample %q: %w", seed.Name, err)
		}
		compiled, err := compileAgentWorkflowDefinition(defBytes, seed.ID, seed.Name)
		if err != nil {
			return fmt.Errorf("compile onboarding sample %q: %w", seed.Name, err)
		}

		rec := WorkflowRecord{
			ID:          seed.ID,
			SchemaKind:  loader.SchemaKindAgent,
			Name:        seed.Name,
			Description: seed.Description,
			Tags:        append([]string(nil), seed.Tags...),
			Source:      json.RawMessage(defBytes),
			Compiled:    compiled,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := s.store.Create(ctx, rec); err != nil {
			if errors.Is(err, ErrWorkflowExists) {
				continue
			}
			return fmt.Errorf("create onboarding sample %q: %w", seed.Name, err)
		}
	}

	return nil
}

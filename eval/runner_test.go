package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// emittingClient emits llm.call / llm.response events like an instrumented
// client so the usage collector sees token usage.
type emittingClient struct {
	inner  core.LLMClient
	nodeID string
}

func (c *emittingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	emit := runtime.EmitterFromContext(ctx)

	emit(runtime.NewEvent(runtime.EventLLMCall, "eval-run").WithNode(c.nodeID, core.NodeKindLLM).
		WithPayload("model", req.Model))

	resp, err := c.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}

	emit(runtime.NewEvent(runtime.EventLLMResponse, "eval-run").WithNode(c.nodeID, core.NodeKindLLM).
		WithPayload("completion", resp.Text).
		WithPayload("input_tokens", resp.Usage.InputTokens).
		WithPayload("output_tokens", resp.Usage.OutputTokens).
		WithPayload("total_tokens", resp.Usage.TotalTokens).
		WithPayload("cost_usd", resp.Usage.CostUSD))
	return resp, nil
}

// echoClient answers with a fixed completion.
type echoClient struct {
	text string
}

func (c *echoClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{
		Text:  c.text,
		Model: req.Model,
		Usage: core.LLMTokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.001},
	}, nil
}

// evalWorkflow builds an LLM graph mirroring a recorded workflow.
func evalWorkflow(answerText string) graph.Graph {
	client := &emittingClient{inner: &echoClient{text: answerText}, nodeID: "answer"}
	llm := nodes.NewLLMNode("answer", client, nodes.LLMNodeConfig{
		Model:          "test-model",
		PromptTemplate: recordPromptTemplateEval,
		OutputKey:      "answer_output",
	})
	g, err := graph.NewGraphBuilder("eval-run").AddNode(llm).Build()
	if err != nil {
		panic(err)
	}
	return g
}

const recordPromptTemplateEval = "Summarize: {{.input}}"

func TestRunnerGradesEveryCase(t *testing.T) {
	ds := &Dataset{
		Name: "graders",
		Cases: []Case{
			{ID: "match", Input: "x", Expected: map[string]any{"answer_output": "fixed answer"}},
			{ID: "differs", Input: "y", Expected: map[string]any{"answer_output": "wrong expectation"}},
		},
	}

	runner := &Runner{
		Graders: []Grader{VarEquals{Var: "answer_output"}},
	}
	result, err := runner.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("fixed answer")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(result.Results))
	}
	if !result.Results[0].Passed {
		t.Errorf("matching case should pass: %+v", result.Results[0])
	}
	if result.Results[1].Passed {
		t.Errorf("differing case should fail: %+v", result.Results[1])
	}
	if result.Aggregate.Passed != 1 || result.Aggregate.Failed != 1 {
		t.Errorf("aggregate = %+v", result.Aggregate)
	}
	if result.Aggregate.PassRate != 0.5 {
		t.Errorf("pass rate = %v, want 0.5", result.Aggregate.PassRate)
	}
}

func TestRunnerUsageAndLatencyMetrics(t *testing.T) {
	ds := &Dataset{
		Name: "metrics",
		Cases: []Case{
			{ID: "a", Input: "a"},
			{ID: "b", Input: "b"},
		},
	}

	runner := &Runner{Graders: []Grader{Contains{Var: "answer_output", Substring: "fixed"}}}
	result, err := runner.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("fixed answer")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, r := range result.Results {
		if r.Usage.TotalTokens != 15 {
			t.Errorf("case %s usage = %+v, want 15 total tokens", r.CaseID, r.Usage)
		}
		if r.Usage.CostUSD != 0.001 {
			t.Errorf("case %s cost = %v", r.CaseID, r.Usage.CostUSD)
		}
	}
	agg := result.Aggregate
	if agg.TotalTokens != 30 {
		t.Errorf("aggregate tokens = %d, want 30", agg.TotalTokens)
	}
	if agg.CostUSD < 0.002-1e-9 || agg.CostUSD > 0.002+1e-9 {
		t.Errorf("aggregate cost = %v, want ~0.002", agg.CostUSD)
	}
	if agg.LatencyAvgMS < 0 || agg.LatencyP50MS < 0 || agg.LatencyP95MS < agg.LatencyP50MS {
		t.Errorf("latency metrics = %+v", agg)
	}
}

func TestRunnerFailureIsolation(t *testing.T) {
	// A dataset where one case's input makes the workflow fail: the batch
	// continues and the failure is recorded on that case.
	failFor := "poison"

	ds := &Dataset{
		Name: "isolation",
		Cases: []Case{
			{ID: "good-1", Input: "ok-1"},
			{ID: "bad", Input: failFor},
			{ID: "good-2", Input: "ok-2"},
		},
	}

	failingGraph := failingWorkflow(failFor)
	runner := &Runner{Graders: []Grader{Contains{Var: "answer_output", Substring: "processed"}}}

	result, err := runner.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: failingGraph})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byID := map[string]CaseResult{}
	for _, r := range result.Results {
		byID[r.CaseID] = r
	}
	if !byID["good-1"].Passed || !byID["good-2"].Passed {
		t.Errorf("good cases should pass: %+v %+v", byID["good-1"], byID["good-2"])
	}
	if byID["bad"].Passed {
		t.Error("failing case should not pass")
	}
	if byID["bad"].Error == "" {
		t.Error("failing case should record the run error")
	}
	if result.Aggregate.Failed != 1 {
		t.Errorf("aggregate failed = %d, want 1", result.Aggregate.Failed)
	}
}

// failingWorkflow fails deterministically for one specific input.
func failingWorkflow(failFor string) graph.Graph {
	failNode := core.NewFuncNode("gate", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		if input, _ := env.Input.(string); input == failFor {
			return nil, errors.New("boom")
		}
		env.SetVar("answer_output", "processed")
		return env, nil
	})
	g, err := graph.NewGraphBuilder("isolation").AddNode(failNode).Build()
	if err != nil {
		panic(err)
	}
	return g
}

func TestRunnerBoundedConcurrency(t *testing.T) {
	// 10 cases against a runner that records peak in-flight concurrency.
	ds := &Dataset{Name: "concurrent"}
	for i := 0; i < 10; i++ {
		ds.Cases = append(ds.Cases, Case{ID: fmt.Sprintf("case-%d", i), Input: i})
	}

	var mu sync.Mutex
	inFlight, peak := 0, 0
	concurrency := 3

	trackingRunner := trackingCaseRunner(func(c Case) (*core.Envelope, UsageTotals, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return core.NewEnvelope(), UsageTotals{TotalTokens: 1}, nil
	})

	runner := &Runner{
		Graders:     []Grader{alwaysPassGrader{}},
		Concurrency: concurrency,
	}
	result, err := runner.Run(context.Background(), ds, trackingRunner)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak > concurrency {
		t.Errorf("peak concurrency = %d, exceeds bound %d", peak, concurrency)
	}
	if peak < 2 {
		t.Errorf("peak concurrency = %d, expected parallel execution", peak)
	}
	if result.Aggregate.Passed != 10 {
		t.Errorf("passed = %d, want 10", result.Aggregate.Passed)
	}
}

func TestRunnerGraderErrorFailsCase(t *testing.T) {
	ds := &Dataset{Name: "grader-error", Cases: []Case{{ID: "c1", Input: "x"}}}
	runner := &Runner{Graders: []Grader{erroringGrader{}}}

	result, err := runner.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("anything")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Results[0].Passed {
		t.Error("grader error should fail the case")
	}
	if result.Results[0].Grades[0].Error == "" {
		t.Error("grader error should be recorded")
	}
}

func TestRunnerCustomGraderWithoutInternals(t *testing.T) {
	// An out-of-tree grader: implements Grader over the eval contract only.
	custom := keywordGrader{keyword: "fixed"}

	ds := &Dataset{Name: "custom", Cases: []Case{{ID: "c1", Input: "x"}}}
	runner := &Runner{Graders: []Grader{custom}}
	result, err := runner.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("fixed answer")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Results[0].Passed {
		t.Errorf("custom grader should pass: %+v", result.Results[0].Grades)
	}
}

type keywordGrader struct {
	keyword string
}

func (g keywordGrader) Name() string { return "keyword_judge" }

func (g keywordGrader) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	env := eval.Final
	if env == nil {
		return Grade{Score: 0}, nil
	}
	if text, ok := env.GetVar("answer_output"); ok && strings.Contains(text.(string), g.keyword) {
		return Grade{Score: 1, Passed: true, Reason: "keyword present"}, nil
	}
	return Grade{Score: 0, Reason: "keyword missing"}, nil
}

type alwaysPassGrader struct{}

func (alwaysPassGrader) Name() string { return "always_pass" }

func (alwaysPassGrader) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	return Grade{Score: 1, Passed: true}, nil
}

type erroringGrader struct{}

func (erroringGrader) Name() string { return "erroring" }

func (erroringGrader) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	return Grade{}, errors.New("grader exploded")
}

type trackingCaseRunner func(c Case) (*core.Envelope, UsageTotals, error)

func (f trackingCaseRunner) RunCase(ctx context.Context, c Case) (*core.Envelope, UsageTotals, error) {
	return f(c)
}

func TestRunnerValidation(t *testing.T) {
	ds := sampleDataset()

	if _, err := (&Runner{Graders: nil}).Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("x")}); err == nil {
		t.Error("expected error without graders")
	}
	if _, err := (&Runner{Graders: []Grader{alwaysPassGrader{}}}).Run(context.Background(), ds, nil); err == nil {
		t.Error("expected error without a case runner")
	}
	bad := &Dataset{Name: "bad"}
	if _, err := (&Runner{Graders: []Grader{alwaysPassGrader{}}}).Run(context.Background(), bad, &RuntimeCaseRunner{Graph: evalWorkflow("x")}); !errors.Is(err, ErrInvalidDataset) {
		t.Errorf("expected ErrInvalidDataset, got %v", err)
	}
	filtered := &Runner{Graders: []Grader{alwaysPassGrader{}}, Tags: []string{"missing"}}
	if _, err := filtered.Run(context.Background(), ds, &RuntimeCaseRunner{Graph: evalWorkflow("x")}); err == nil {
		t.Error("expected error when tag filter matches nothing")
	}
}

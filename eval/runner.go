package eval

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// UsageTotals aggregates token consumption and cost for a case or dataset.
type UsageTotals struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	TotalTokens  int     `json:"total_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// Add combines two totals.
func (u UsageTotals) Add(other UsageTotals) UsageTotals {
	return UsageTotals{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		TotalTokens:  u.TotalTokens + other.TotalTokens,
		CostUSD:      u.CostUSD + other.CostUSD,
	}
}

// CaseRunner executes a single dataset case and reports its final envelope
// plus token usage. Implement it to run cases against a live runtime, a
// replay runner (see the replay package), or any other execution mode.
type CaseRunner interface {
	// RunCase executes one case. Returning an error marks the case failed;
	// per-case failure isolation guarantees the batch continues.
	RunCase(ctx context.Context, c Case) (*core.Envelope, UsageTotals, error)
}

// RuntimeCaseRunner executes cases against a workflow with the live
// runtime. Each case runs on its own runtime instance with a fresh envelope
// seeded from the case input and variables. Token usage is captured from
// llm.response events; a base EventHandler, when set, still receives them.
type RuntimeCaseRunner struct {
	// Graph is the workflow under evaluation.
	Graph graph.Graph

	// Base provides the base run options. NodeRecorder and the run input
	// envelope are managed by the runner; EventHandler is composed with the
	// usage collector; everything else is passed through per case.
	Base runtime.RunOptions
}

// RunCase implements CaseRunner.
func (r *RuntimeCaseRunner) RunCase(ctx context.Context, c Case) (*core.Envelope, UsageTotals, error) {
	usage := UsageTotals{}
	collector := func(e runtime.Event) {
		switch e.Kind {
		case runtime.EventLLMResponse:
			if e.Payload["status"] == "error" {
				return
			}
			usage.TotalTokens += eventInt(e, "total_tokens")
			usage.InputTokens += eventInt(e, "input_tokens")
			usage.OutputTokens += eventInt(e, "output_tokens")
			if cost, ok := e.Payload["cost_usd"].(float64); ok {
				usage.CostUSD += cost
			}
		}
	}

	opts := r.Base
	if opts.EventHandler != nil {
		baseHandler := opts.EventHandler
		opts.EventHandler = func(e runtime.Event) {
			baseHandler(e)
			collector(e)
		}
	} else {
		opts.EventHandler = collector
	}

	env := core.NewEnvelope().WithInput(c.Input)
	for k, v := range c.Vars {
		env.SetVar(k, v)
	}

	final, err := runtime.NewRuntime().Run(ctx, r.Graph, env, opts)
	return final, usage, err
}

// eventInt extracts an integer event payload field.
func eventInt(e runtime.Event, key string) int {
	switch v := e.Payload[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// GradeResult records one grader's verdict on a case.
type GradeResult struct {
	Grader string  `json:"grader"`
	Score  float64 `json:"score"`
	Passed bool    `json:"passed"`
	Reason string  `json:"reason,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// CaseResult is the outcome of one dataset case.
type CaseResult struct {
	CaseID   string        `json:"case_id"`
	Passed   bool          `json:"passed"`
	Grades   []GradeResult `json:"grades,omitempty"`
	Error    string        `json:"error,omitempty"`
	Duration float64       `json:"duration_ms"`
	Usage    UsageTotals   `json:"usage"`
}

// Aggregate summarizes a dataset run.
type Aggregate struct {
	TotalCases   int     `json:"total_cases"`
	Passed       int     `json:"passed"`
	Failed       int     `json:"failed"`
	PassRate     float64 `json:"pass_rate"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyAvgMS float64 `json:"latency_avg_ms"`
	LatencyP50MS float64 `json:"latency_p50_ms"`
	LatencyP95MS float64 `json:"latency_p95_ms"`
	TotalTimeMS  float64 `json:"total_time_ms"`
}

// DatasetResult is the machine-readable outcome of a dataset run.
type DatasetResult struct {
	Dataset   string       `json:"dataset"`
	Tags      []string     `json:"tags,omitempty"`
	Graders   []string     `json:"graders"`
	StartedAt time.Time    `json:"started_at"`
	Finished  time.Time    `json:"finished_at"`
	Results   []CaseResult `json:"cases"`
	Aggregate Aggregate    `json:"aggregate"`
}

// Runner executes a dataset of cases through a CaseRunner and grades every
// case with the configured graders. Cases run with bounded concurrency and
// per-case failure isolation: a case that errors (or a grader that fails)
// is recorded and the batch continues.
type Runner struct {
	// Graders assert on each case's output. A case passes when every
	// grader passes. Required: at least one grader.
	Graders []Grader

	// Concurrency bounds the number of cases running simultaneously
	// (default: 1, sequential).
	Concurrency int

	// Tags optionally filters the dataset to cases carrying any of these
	// tags. Empty runs all cases.
	Tags []string
}

// Run executes the dataset and returns the aggregated result.
func (r *Runner) Run(ctx context.Context, ds *Dataset, caseRunner CaseRunner) (*DatasetResult, error) {
	if err := ds.Validate(); err != nil {
		return nil, err
	}
	if len(r.Graders) == 0 {
		return nil, fmt.Errorf("eval: runner requires at least one grader")
	}
	if caseRunner == nil {
		return nil, fmt.Errorf("eval: runner requires a CaseRunner")
	}

	concurrency := r.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	cases := ds.Filter(r.Tags)
	if len(cases) == 0 {
		return nil, fmt.Errorf("eval: tag filter matched no cases in dataset %q", ds.Name)
	}

	result := &DatasetResult{
		Dataset:   ds.Name,
		Tags:      r.Tags,
		Graders:   graderNames(r.Graders),
		StartedAt: time.Now(),
		Results:   make([]CaseResult, len(cases)),
	}

	// Bounded worker pool with indexed slots so results keep case order.
	indexes := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex // guards ctx error propagation only

	for worker := 0; worker < concurrency && worker < len(cases); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				// Per-case failure isolation: an error marks the case
				// failed and the batch continues.
				select {
				case <-ctx.Done():
					mu.Lock()
					result.Results[i] = CaseResult{CaseID: cases[i].ID, Error: ctx.Err().Error()}
					mu.Unlock()
					continue
				default:
				}
				result.Results[i] = r.runCase(ctx, cases[i], caseRunner)
			}
		}()
	}

	for i := range cases {
		select {
		case indexes <- i:
		case <-ctx.Done():
			close(indexes)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(indexes)
	wg.Wait()

	result.Finished = time.Now()
	result.Aggregate = aggregate(result.Results, result.StartedAt, result.Finished)
	return result, nil
}

// runCase executes and grades one case. It never returns an error: case
// failures are recorded in the result so one bad case cannot fail the batch.
func (r *Runner) runCase(ctx context.Context, c Case, caseRunner CaseRunner) CaseResult {
	out := CaseResult{CaseID: c.ID}

	start := time.Now()
	final, usage, runErr := caseRunner.RunCase(ctx, c)
	out.Duration = time.Since(start).Seconds() * 1000
	out.Usage = usage

	if runErr != nil {
		out.Error = runErr.Error()
		// Graders still run: graders that inspect RunError (or explicitly
		// tolerate failure) stay meaningful.
	}

	evaluation := CaseEvaluation{Case: c, Final: final, RunError: runErr}
	passed := runErr == nil
	for _, grader := range r.Graders {
		grade, gradeErr := grader.Grade(ctx, evaluation)
		result := GradeResult{Grader: grader.Name()}
		switch {
		case gradeErr != nil:
			result.Error = gradeErr.Error()
			passed = false
		default:
			result.Score = grade.Score
			result.Passed = grade.Passed
			result.Reason = grade.Reason
			if !grade.Passed {
				passed = false
			}
		}
		out.Grades = append(out.Grades, result)
	}

	if runErr != nil && passed {
		// A run error fails the case even if every grader tolerated it.
		passed = false
	}
	out.Passed = passed
	return out
}

// aggregate computes dataset-level metrics from per-case results.
func aggregate(results []CaseResult, started, finished time.Time) Aggregate {
	agg := Aggregate{TotalCases: len(results)}

	latencies := make([]float64, 0, len(results))
	for _, r := range results {
		if r.Passed {
			agg.Passed++
		} else {
			agg.Failed++
		}
		agg.TotalTokens += r.Usage.TotalTokens
		agg.CostUSD += r.Usage.CostUSD
		latencies = append(latencies, r.Duration)
	}
	agg.TotalTimeMS = finished.Sub(started).Seconds() * 1000

	if agg.TotalCases > 0 {
		agg.PassRate = float64(agg.Passed) / float64(agg.TotalCases)
		sum := 0.0
		for _, l := range latencies {
			sum += l
		}
		agg.LatencyAvgMS = sum / float64(len(latencies))
		sort.Float64s(latencies)
		agg.LatencyP50MS = percentile(latencies, 0.50)
		agg.LatencyP95MS = percentile(latencies, 0.95)
	}
	return agg
}

// percentile interpolates the p-th percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p * float64(len(sorted)-1)
	lower := int(rank)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	weight := rank - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// graderNames lists grader names for the report.
func graderNames(graders []Grader) []string {
	names := make([]string, 0, len(graders))
	for _, g := range graders {
		names = append(names, g.Name())
	}
	return names
}

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/eval"
	"github.com/petal-labs/petalflow/runtime"
)

// NewEvalCmd creates the "eval" subcommand: run a workflow against a
// dataset, grade the cases, and enforce quality/cost/latency gates.
// Gate errors exit non-zero (exitValidation) for CI.
func NewEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval <workflow-file>",
		Short: "Run a workflow against an eval dataset and enforce quality gates",
		Args:  cobra.ExactArgs(1),
		RunE:  runEval,
	}

	cmd.Flags().String("dataset", "", "Eval dataset JSON file (required)")
	cmd.Flags().String("policy", "", "Eval policy JSON file: graders + gates (required)")
	cmd.Flags().String("report", "", "Write the dataset result report to this JSON file")
	cmd.Flags().String("baseline", "", "Baseline report JSON to compare against")
	cmd.Flags().Bool("update-baseline", false, "Write the current report to the baseline path after a passing run (accepts intentional changes)")
	cmd.Flags().Int("concurrency", 1, "Number of cases to evaluate simultaneously")
	cmd.Flags().StringSlice("tags", nil, "Only evaluate cases carrying any of these tags")
	cmd.Flags().Duration("node-timeout", 0, "Per-node timeout; interrupts a node that ignores cancellation (0 = disabled)")
	cmd.Flags().StringArray("env", nil, "Set environment variable (repeatable)")
	cmd.Flags().StringArray("provider-key", nil, "Set provider API key (repeatable, e.g. --provider-key anthropic=sk-...)")
	cmd.Flags().String("store-path", "", "Path to SQLite store for tool registry (default: ~/.petalflow/petalflow.db)")
	cmd.Flags().String("database-dsn", "", "Database DSN (default: SQLite at ~/.petalflow/petalflow.db)")

	return cmd
}

func runEval(cmd *cobra.Command, args []string) error {
	datasetPath, _ := cmd.Flags().GetString("dataset")
	policyPath, _ := cmd.Flags().GetString("policy")
	reportPath, _ := cmd.Flags().GetString("report")
	baselinePath, _ := cmd.Flags().GetString("baseline")
	updateBaseline, _ := cmd.Flags().GetBool("update-baseline")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	tags, _ := cmd.Flags().GetStringSlice("tags")

	if datasetPath == "" {
		return exitError(exitValidation, "--dataset is required")
	}
	if policyPath == "" {
		return exitError(exitValidation, "--policy is required")
	}

	ds, err := eval.LoadDataset(datasetPath)
	if err != nil {
		return exitError(exitFileNotFound, "%v", err)
	}
	policy, graders, err := eval.LoadPolicy(policyPath)
	if err != nil {
		return exitError(exitValidation, "%v", err)
	}

	// Load the workflow and hydrate it once; every case re-runs it.
	gd, err := loadWorkflowForRun(cmd, args[0])
	if err != nil {
		return err
	}

	explicitStore := hasRunExplicitStore(cmd)
	store, err := resolveToolStore(cmd)
	if err != nil {
		if explicitStore {
			return exitError(exitRuntime, "loading tool store: %v", err)
		}
		store = runNoopToolStore{}
	}
	defer closeToolStore(store)
	if err := syncRunToolNodeTypes(cmd.Context(), store); err != nil {
		return exitError(exitRuntime, "syncing tool node types: %v", err)
	}

	providers, err := resolveRunProviders(cmd)
	if err != nil {
		return err
	}
	toolRegistry, err := buildRunToolRegistry(cmd, store)
	if err != nil {
		return err
	}
	execGraph, err := hydrateRunGraph(cmd, gd, providers, toolRegistry)
	if err != nil {
		return err
	}
	applyRunEnvVars(cmd)

	nodeTimeout, _ := cmd.Flags().GetDuration("node-timeout")
	caseRunner := &eval.RuntimeCaseRunner{
		Graph: execGraph,
		Base: runtime.RunOptions{
			NodeTimeout: nodeTimeout,
		},
	}

	runner := &eval.Runner{
		Graders:     graders,
		Concurrency: concurrency,
		Tags:        tags,
	}

	result, err := runner.Run(cmd.Context(), ds, caseRunner)
	if err != nil {
		return exitError(exitRuntime, "%v", err)
	}

	if reportPath != "" {
		if err := eval.WriteReport(result, reportPath); err != nil {
			return exitError(exitRuntime, "%v", err)
		}
	}

	var baseline *eval.DatasetResult
	if baselinePath != "" {
		if loaded, loadErr := eval.LoadReport(baselinePath); loadErr == nil {
			baseline = loaded
		}
		// A missing baseline is not an error: the first run establishes one.
	}

	violations, gateErr := policy.Gates.CheckGates(result, baseline)
	printEvalSummary(cmd, result, violations)

	// Accept intentional changes: write the new baseline so subsequent
	// runs compare against it.
	if updateBaseline && baselinePath != "" {
		if err := eval.WriteReport(result, baselinePath); err != nil {
			return exitError(exitRuntime, "updating baseline: %v", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Baseline updated: %s\n", baselinePath)
	}

	if gateErr != nil {
		return exitError(exitValidation, "%v", gateErr)
	}
	return nil
}

// printEvalSummary writes the run summary and any gate violations.
func printEvalSummary(cmd *cobra.Command, result *eval.DatasetResult, violations []eval.GateViolation) {
	fmt.Fprintln(cmd.OutOrStdout(), eval.Summarize(result))

	for _, v := range violations {
		fmt.Fprintf(cmd.ErrOrStderr(), "GATE %s [%s] %s: %s\n",
			strings.ToUpper(v.Severity), v.Gate, v.Metric, v.Message)
	}
	if len(violations) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All gates passed.")
	}
}

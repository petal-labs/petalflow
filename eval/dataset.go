// Package eval provides the evaluation harness for PetalFlow workflows:
// datasets of cases, graders that assert on run outputs, a batch runner
// with bounded concurrency and per-case failure isolation, and aggregate
// latency/cost metrics with machine-readable reports.
//
// The Grader interface is deliberately small and dependency-light so
// external graders — such as an LLM-as-judge — can be implemented outside
// the PetalFlow tree without importing PetalFlow internals beyond this
// package's contract.
package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Case is a single evaluation case: one workflow input plus expectations.
type Case struct {
	// ID identifies the case within the dataset (required, unique).
	ID string `json:"id"`
	// Input is the workflow input for this case.
	Input any `json:"input,omitempty"`
	// Vars are the initial envelope variables for this case.
	Vars map[string]any `json:"vars,omitempty"`
	// Expected holds expected output values, keyed by envelope variable
	// name. Graders such as VarEquals read from here; the shape is
	// free-form so custom graders can carry their own expectations.
	Expected map[string]any `json:"expected,omitempty"`
	// Metadata carries arbitrary annotations (author, ticket, source).
	Metadata map[string]any `json:"metadata,omitempty"`
	// Tags group cases for filtering and reporting.
	Tags []string `json:"tags,omitempty"`
}

// Dataset is a named collection of evaluation cases.
type Dataset struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Cases       []Case `json:"cases"`
}

// ErrInvalidDataset is returned when a dataset fails validation.
var ErrInvalidDataset = errors.New("invalid eval dataset")

// Validate checks the dataset's structural invariants: non-empty name, at
// least one case, and unique, non-empty case IDs.
func (d *Dataset) Validate() error {
	if d == nil {
		return fmt.Errorf("%w: dataset is nil", ErrInvalidDataset)
	}
	if d.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDataset)
	}
	if len(d.Cases) == 0 {
		return fmt.Errorf("%w: at least one case is required", ErrInvalidDataset)
	}
	seen := make(map[string]bool, len(d.Cases))
	for i, c := range d.Cases {
		if c.ID == "" {
			return fmt.Errorf("%w: cases[%d] missing id", ErrInvalidDataset, i)
		}
		if seen[c.ID] {
			return fmt.Errorf("%w: duplicate case id %q", ErrInvalidDataset, c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

// Filter returns the subset of cases carrying any of the given tags. An
// empty tag list returns all cases.
func (d *Dataset) Filter(tags []string) []Case {
	if len(tags) == 0 {
		return d.Cases
	}
	wanted := make(map[string]bool, len(tags))
	for _, t := range tags {
		wanted[t] = true
	}
	out := make([]Case, 0, len(d.Cases))
	for _, c := range d.Cases {
		for _, tag := range c.Tags {
			if wanted[tag] {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// LoadDataset reads and validates a dataset from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, fmt.Errorf("eval: read dataset: %w", err)
	}

	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("eval: decode dataset: %w", err)
	}
	if err := ds.Validate(); err != nil {
		return nil, err
	}
	return &ds, nil
}

// SaveDataset writes the dataset to path as formatted JSON.
func SaveDataset(ds *Dataset, path string) error {
	if err := ds.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: encode dataset: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("eval: write dataset: %w", err)
	}
	return nil
}

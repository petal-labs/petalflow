package agent

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestCompileSnapshots(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.input.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no snapshot input files found")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.json")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			wf, err := LoadFromBytes(data)
			if err != nil {
				t.Fatalf("LoadFromBytes: %v", err)
			}
			gd, err := Compile(wf)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			// Normalize timestamp so snapshots are deterministic.
			gd.Metadata["compiled_at"] = "NORMALIZED"

			got, err := json.MarshalIndent(gd, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			// Ensure trailing newline for clean diffs.
			got = append(got, '\n')

			goldenPath := strings.Replace(inputPath, ".input.json", ".golden.json", 1)
			if *update {
				if err := os.WriteFile(goldenPath, got, 0644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated golden file %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading golden file (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("snapshot mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

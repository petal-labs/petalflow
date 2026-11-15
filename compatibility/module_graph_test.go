package compatibility_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	goVersion           = "1.25.0"
	irisVersion         = "v1.0.0"
	petalFlowVersion    = "v0.1.0"
	petalFlowModule     = "github.com/petal-labs/petalflow"
	irisModule          = "github.com/petal-labs/iris"
	cortexModule        = "github.com/petal-labs/cortex"
	irisAdapterModule   = petalFlowModule + "/irisadapter"
	localAdapterVersion = "v0.0.0-00010101000000-000000000000"
)

type moduleFile struct {
	Go      string
	Require []moduleVersion
	Replace []moduleReplace
}

type moduleVersion struct {
	Path    string
	Version string
}

type moduleReplace struct {
	Old moduleVersion
	New moduleVersion
}

func TestSupportedModuleGraph(t *testing.T) {
	t.Parallel()

	root := readModule(t, "..")
	adapter := readModule(t, "../irisadapter")
	examples := readModule(t, "../examples")

	for name, module := range map[string]moduleFile{"root": root, "irisadapter": adapter, "examples": examples} {
		if module.Go != goVersion {
			t.Errorf("%s Go version = %q, want %q", name, module.Go, goVersion)
		}
		if _, ok := requirement(module, cortexModule); ok {
			t.Errorf("%s must integrate Cortex through its public adapter/MCP boundary, not a direct dependency", name)
		}
	}

	assertRequirement(t, "root Iris", root, irisModule, irisVersion)
	assertRequirement(t, "adapter Iris", adapter, irisModule, irisVersion)
	assertRequirement(t, "examples Iris", examples, irisModule, irisVersion)
	assertRequirement(t, "adapter PetalFlow", adapter, petalFlowModule, petalFlowVersion)
	assertRequirement(t, "examples PetalFlow", examples, petalFlowModule, petalFlowVersion)
	assertRequirement(t, "examples adapter", examples, irisAdapterModule, localAdapterVersion)
	assertLocalReplace(t, "adapter PetalFlow", adapter, petalFlowModule, "../")
	assertLocalReplace(t, "examples PetalFlow", examples, petalFlowModule, "../")
	assertLocalReplace(t, "examples adapter", examples, irisAdapterModule, "../irisadapter")
}

func readModule(t *testing.T, relativeDir string) moduleFile {
	t.Helper()
	dir, err := filepath.Abs(relativeDir)
	if err != nil {
		t.Fatalf("resolve %s: %v", relativeDir, err)
	}
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("read %s/go.mod: %v", relativeDir, err)
	}
	var module moduleFile
	if err := json.Unmarshal(output, &module); err != nil {
		t.Fatalf("decode %s/go.mod: %v", relativeDir, err)
	}
	return module
}

func assertRequirement(t *testing.T, name string, module moduleFile, path, want string) {
	t.Helper()
	got, ok := requirement(module, path)
	if !ok || got != want {
		t.Errorf("%s requirement = %q (present %v), want %q", name, got, ok, want)
	}
}

func requirement(module moduleFile, path string) (string, bool) {
	for _, item := range module.Require {
		if item.Path == path {
			return item.Version, true
		}
	}
	return "", false
}

func assertLocalReplace(t *testing.T, name string, module moduleFile, path, want string) {
	t.Helper()
	for _, item := range module.Replace {
		if item.Old.Path == path && item.New.Path == want && item.New.Version == "" {
			return
		}
	}
	t.Errorf("%s local replace = missing, want %s => %s", name, path, want)
}

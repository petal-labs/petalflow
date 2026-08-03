package graph

import (
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/registry"
)

func TestValidateWithRegistry_WarnsOnUnknownConfigKey(t *testing.T) {
	gd := &GraphDefinition{
		ID:      "g",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "webhook_call", Config: map[string]any{"url": "https://x", "timout": 5}},
		},
		Entry: "a",
	}

	diags := gd.ValidateWithRegistry(registry.Global())

	foundWarn := false
	for _, d := range diags {
		if d.Code == "GR-020" && d.Severity == SeverityWarning && strings.Contains(d.Message, "timout") {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Errorf("expected a GR-020 warning naming unknown config key 'timout', got diags: %+v", diags)
	}

	// Unknown config keys must be a warning, not a hard error.
	if HasErrors(filterByCode(diags, "GR-020")) {
		t.Error("unknown config key should be a warning, not an error")
	}
}

func TestValidateWithRegistry_NoWarnOnKnownConfigKeys(t *testing.T) {
	gd := &GraphDefinition{
		ID:      "g",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "webhook_call", Config: map[string]any{"url": "https://x", "method": "POST", "timeout": "5s"}},
		},
		Entry: "a",
	}

	diags := gd.ValidateWithRegistry(registry.Global())
	for _, d := range diags {
		if d.Code == "GR-020" {
			t.Errorf("unexpected config-key warning for a valid config: %s", d.Message)
		}
	}
}

func filterByCode(diags []Diagnostic, code string) []Diagnostic {
	var out []Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

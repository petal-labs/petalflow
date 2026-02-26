package schemafmt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// WorkflowKind identifies supported top-level workflow schema kinds.
type WorkflowKind string

const (
	KindAgent WorkflowKind = "agent_workflow"
	KindGraph WorkflowKind = "graph"

	LegacyKindAgent = "agent-workflow"

	SupportedAgentSchemaMajor = 1
	SupportedGraphSchemaMajor = 1

	CurrentAgentSchemaVersion = "1.0.0"
	CurrentGraphSchemaVersion = "1.0.0"

	LegacySchemaVersion = "legacy"
)

var semverPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-((?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)` +
		`(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?` +
		`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

// NormalizeKind validates and canonicalizes a schema kind string.
// Returns canonical kind, whether a legacy alias was used, and any validation error.
func NormalizeKind(raw string) (WorkflowKind, bool, error) {
	kind := strings.TrimSpace(raw)

	switch kind {
	case string(KindAgent):
		return KindAgent, false, nil
	case LegacyKindAgent:
		return KindAgent, true, nil
	case string(KindGraph):
		return KindGraph, false, nil
	default:
		return "", false, fmt.Errorf("invalid kind %q", raw)
	}
}

// ValidateSchemaVersion ensures schema_version is a valid SemVer 2.0.0 string
// and that its MAJOR version is supported.
func ValidateSchemaVersion(version string, supportedMajor int) error {
	v := strings.TrimSpace(version)
	if v == "" {
		return fmt.Errorf("schema_version is required")
	}

	match := semverPattern.FindStringSubmatch(v)
	if match == nil {
		return fmt.Errorf("schema_version %q must be a valid semantic version (MAJOR.MINOR.PATCH)", version)
	}

	major, err := strconv.Atoi(match[1])
	if err != nil {
		return fmt.Errorf("parsing schema_version major: %w", err)
	}
	if major != supportedMajor {
		return fmt.Errorf("schema_version %q has unsupported major %d (supported: %d.x.x)", version, major, supportedMajor)
	}

	return nil
}

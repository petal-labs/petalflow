package tool

import (
	"fmt"
	"slices"
)

// SideEffectLevel describes how executing a tool affects the world.
type SideEffectLevel string

const (
	// SideEffectNone means no classification was declared; origin
	// defaults fill in at registration.
	SideEffectNone SideEffectLevel = ""
	// SideEffectReadOnly means the tool computes or reads without
	// durable effects (template rendering, safe GETs, discovery).
	SideEffectReadOnly SideEffectLevel = "readonly"
	// SideEffectSideEffect means the tool performs durable writes or
	// reaches outward in ways that are not guaranteed read-only. The
	// serialized value is "side_effect".
	SideEffectSideEffect SideEffectLevel = "side_effect"
	// SideEffectDestructive means the tool deletes, overwrites beyond
	// recovery, or otherwise destroys targets.
	SideEffectDestructive SideEffectLevel = "destructive"
)

// PolicyClass names matched against classification bits in tool policies
// (require_approval_classes).
const (
	PolicyClassSideEffect      = "side_effect"
	PolicyClassDestructive     = "destructive"
	PolicyClassNetwork         = "network"
	PolicyClassDownstreamWrite = "downstream_write"
	PolicyClassReadonly        = "readonly"
)

// PolicyClasses is the closed set of class names accepted in policies.
var PolicyClasses = []string{
	PolicyClassSideEffect,
	PolicyClassDestructive,
	PolicyClassNetwork,
	PolicyClassDownstreamWrite,
	PolicyClassReadonly,
}

// SafetyClassification describes dangerous or side-effecting behavior for
// a tool or one of its actions. It is part of the manifest schema so the
// classification is owned by the same record that describes the tool.
//
// Semantics:
//   - Level targets computed effects: readonly, side_effect, destructive;
//     empty means unspecified (origin defaults apply at registration).
//   - Network marks outbound network/egress behavior.
//   - DownstreamWrite marks writes to downstream systems (email, webhooks,
//     records, payments).
//   - Destructive marks deletion or irreversible overwrite behavior.
//
// Action-level classification (inside ActionSpec) overrides the tool-level
// one when declared.
type SafetyClassification struct {
	Level SideEffectLevel `json:"side_effect_level,omitempty"`
	// Network marks outbound network/egress access.
	Network bool `json:"network,omitempty"`
	// Destructive marks deletion or irreversible operations.
	Destructive bool `json:"destructive,omitempty"`
	// DownstreamWrite marks writes that notify or record on external
	// systems; the serialized key is downstream_write.
	DownstreamWrite bool `json:"downstream_write,omitempty"`
	// Notes is human-readable context shown in registries and docs.
	Notes string `json:"notes,omitempty"`
}

// IsDangerous reports whether the classification marks any risky
// behavior at all.
func (s SafetyClassification) IsDangerous() bool {
	if s.Level == SideEffectSideEffect || s.Level == SideEffectDestructive {
		return true
	}
	return s.Network || s.Destructive || s.DownstreamWrite
}

// MatchesClass reports whether the classification falls into the named
// policy class.
func (s SafetyClassification) MatchesClass(class string) bool {
	switch class {
	case PolicyClassReadonly:
		return s.Level == SideEffectReadOnly
	case PolicyClassSideEffect:
		return s.Level == SideEffectSideEffect || s.Level == SideEffectDestructive
	case PolicyClassDestructive:
		return s.Level == SideEffectDestructive || s.Destructive
	case PolicyClassNetwork:
		return s.Network
	case PolicyClassDownstreamWrite:
		return s.DownstreamWrite
	default:
		return false
	}
}

func (s SafetyClassification) normalize() SafetyClassification {
	out := s
	switch out.Level {
	case SideEffectReadOnly, SideEffectSideEffect, SideEffectDestructive, SideEffectNone:
	default:
		out.Level = SideEffectNone
	}
	return out
}

// validate reports structural problems with a classification.
func (s SafetyClassification) validate(field string) []Diagnostic {
	var diags []Diagnostic
	normalized := s.normalize()
	if s.Level != SideEffectNone && normalized.Level != s.Level {
		diags = append(diags, Diagnostic{
			Field:    field,
			Code:     "INVALID_SIDE_EFFECT_LEVEL",
			Severity: SeverityError,
			Message: fmt.Sprintf("side_effect_level must be %q, %q, %q, or empty (got %q)",
				SideEffectReadOnly, SideEffectSideEffect, SideEffectDestructive, s.Level),
		})
	}
	if normalized.Level == SideEffectReadOnly && normalized.Destructive {
		diags = append(diags, Diagnostic{
			Field:    field,
			Code:     "CONFLICTING_CLASSIFICATION",
			Severity: SeverityError,
			Message:  "readonly tools cannot be marked destructive",
		})
	}
	return diags
}

// EffectiveSafety merges tool-level classification with the action-level
// override declared on the named action. A nil or missing action
// classification falls back to the tool-level classification.
func (m ToolManifest) EffectiveSafety(action string) SafetyClassification {
	combined := SafetyClassification{}
	if m.Safety != nil {
		combined = *m.Safety
	}
	if spec, ok := m.Actions[action]; ok && spec.Safety != nil {
		combined = mergeSafety(combined, *spec.Safety)
	}
	return combined.normalize()
}

// EffectiveRegistrationSafety returns the tool-level classification for a
// registration, applying conservative origin defaults when the manifest
// does not classify itself.
func (r ToolRegistration) EffectiveRegistrationSafety() SafetyClassification {
	if r.Manifest.Safety != nil {
		return r.Manifest.Safety.normalize()
	}
	return DefaultSafetyForOrigin(r).normalize()
}

// mergeSafety lets the action-level override fill or replace fields of
// the tool-level classification. A declared level replaces; boolean
// checks only upgrade (any true wins) so merges cannot under-classify.
func mergeSafety(tool, action SafetyClassification) SafetyClassification {
	merged := tool
	if action.Level != SideEffectNone {
		merged.Level = action.Level
	}
	if action.Network {
		merged.Network = true
	}
	if action.Destructive {
		merged.Destructive = true
	}
	if action.DownstreamWrite {
		merged.DownstreamWrite = true
	}
	if action.Notes != "" {
		merged.Notes = action.Notes
	}
	return merged
}

const safetyInferredNotes = "classification inferred from transport at registration"

// DefaultSafetyForOrigin returns the conservative default classification
// for a tool whose manifest does not classify itself. The defaults make
// classification stripping ineffective: re-registering the same behavior
// under a new name picks the origin default back up.
func DefaultSafetyForOrigin(reg ToolRegistration) SafetyClassification {
	switch reg.Origin {
	case OriginHTTP:
		return SafetyClassification{
			Level:   SideEffectSideEffect,
			Network: true,
			Notes:   safetyInferredNotes,
		}
	case OriginMCP:
		safety := SafetyClassification{
			Level: SideEffectSideEffect,
			Notes: safetyInferredNotes,
		}
		transport, ok := reg.Manifest.Transport.AsMCP()
		if ok {
			if transport.Mode == MCPModeSSE || transport.Mode == MCPModeStreamableHTTP {
				safety.Network = true
			}
		}
		return safety
	case OriginStdio:
		return SafetyClassification{
			Level: SideEffectSideEffect,
			Notes: "runs a local subprocess",
		}
	case OriginNative:
		return SafetyClassification{
			Level: SideEffectSideEffect,
			Notes: "in-process tool with undeclared effects",
		}
	default:
		return SafetyClassification{Level: SideEffectSideEffect}
	}
}

// ValidateSafetyClassifications checks tool-level and action-level
// classification declarations on a manifest.
func ValidateSafetyClassifications(manifest Manifest) []Diagnostic {
	var diags []Diagnostic
	if manifest.Safety != nil {
		diags = append(diags, manifest.Safety.validate("safety")...)
	}
	actions := make([]string, 0, len(manifest.Actions))
	for name := range manifest.Actions {
		actions = append(actions, name)
	}
	slices.Sort(actions)
	for _, name := range actions {
		spec := manifest.Actions[name]
		if spec.Safety == nil {
			continue
		}
		diags = append(diags, spec.Safety.validate(fmt.Sprintf("actions.%s.safety", name))...)
	}
	return diags
}

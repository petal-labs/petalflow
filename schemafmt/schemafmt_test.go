package schemafmt

import "testing"

func TestNormalizeKind(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       WorkflowKind
		wantLegacy bool
		wantErr    bool
	}{
		{name: "agent canonical", input: "agent_workflow", want: KindAgent},
		{name: "agent legacy", input: "agent-workflow", want: KindAgent, wantLegacy: true},
		{name: "graph", input: "graph", want: KindGraph},
		{name: "unknown", input: "workflow", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotLegacy, err := NormalizeKind(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NormalizeKind() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeKind() error = %v", err)
			}
			if gotKind != tt.want {
				t.Fatalf("NormalizeKind() kind = %q, want %q", gotKind, tt.want)
			}
			if gotLegacy != tt.wantLegacy {
				t.Fatalf("NormalizeKind() legacy = %v, want %v", gotLegacy, tt.wantLegacy)
			}
		})
	}
}

func TestValidateSchemaVersion(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		supportedMajor int
		wantErr        bool
	}{
		{name: "valid supported", version: "1.2.3", supportedMajor: 1},
		{name: "valid prerelease", version: "1.2.3-alpha.1", supportedMajor: 1},
		{name: "invalid format", version: "1.2", supportedMajor: 1, wantErr: true},
		{name: "unsupported major", version: "2.0.0", supportedMajor: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSchemaVersion(tt.version, tt.supportedMajor)
			if tt.wantErr && err == nil {
				t.Fatal("ValidateSchemaVersion() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateSchemaVersion() error = %v", err)
			}
		})
	}
}

package contract

import "testing"

func TestValidateVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "current", version: EventSchemaVersion},
		{name: "compatible minor", version: "1.9.0"},
		{name: "breaking major", version: "2.0.0", wantErr: true},
		{name: "invalid", version: "1", wantErr: true},
		{name: "missing", version: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateVersion(tt.version, EventSchemaVersion)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateVersion(%q, %q) error = %v, wantErr %v", tt.version, EventSchemaVersion, err, tt.wantErr)
			}
		})
	}
}

func TestValidateVersionRejectsInvalidSupportedVersion(t *testing.T) {
	t.Parallel()

	for _, supported := range []string{"missing-dots", "x.0.0"} {
		if err := ValidateVersion("1.0.0", supported); err == nil {
			t.Errorf("ValidateVersion() error = nil for invalid supported version %q", supported)
		}
	}
}

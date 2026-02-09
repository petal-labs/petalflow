package tool

import "testing"

func TestNewManifestDefaults(t *testing.T) {
	man := NewManifest("s3_fetch")

	if man.Schema != SchemaToolV1 {
		t.Errorf("Schema = %q, want %q", man.Schema, SchemaToolV1)
	}
	if man.ManifestVersion != ManifestVersionV1 {
		t.Errorf("ManifestVersion = %q, want %q", man.ManifestVersion, ManifestVersionV1)
	}
	if man.Tool.Name != "s3_fetch" {
		t.Errorf("Tool.Name = %q, want %q", man.Tool.Name, "s3_fetch")
	}
	if man.Actions == nil {
		t.Fatal("Actions should be initialized")
	}
}

func TestRegistrationActionNamesSorted(t *testing.T) {
	reg := Registration{
		Manifest: Manifest{
			Actions: map[string]ActionSpec{
				"download": {},
				"list":     {},
				"delete":   {},
			},
		},
	}

	got := reg.ActionNames()
	want := []string{"delete", "download", "list"}

	if len(got) != len(want) {
		t.Fatalf("len(ActionNames) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ActionNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

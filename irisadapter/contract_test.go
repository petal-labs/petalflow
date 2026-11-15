package irisadapter

import "testing"

func TestCompatibilityContractVersions(t *testing.T) {
	t.Parallel()

	if ContractVersion != "1.0.0" {
		t.Fatalf("ContractVersion = %q, want 1.0.0", ContractVersion)
	}
	if SupportedIrisVersion != "1.0.0" {
		t.Fatalf("SupportedIrisVersion = %q, want 1.0.0", SupportedIrisVersion)
	}
}

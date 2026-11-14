package petalflow_test

import (
	"testing"

	petalflow "github.com/petal-labs/petalflow"
)

func TestArtifactSPIReExports(t *testing.T) {
	store, err := petalflow.NewArtifactStore()
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}
	var _ petalflow.ArtifactStore = store
	if petalflow.ArtifactErrNotFound == nil || petalflow.ArtifactErrUnavailable == nil || petalflow.ArtifactErrPolicyRejected == nil {
		t.Fatal("artifact sentinel errors must be re-exported")
	}
}

package compatibility_test

import "testing"

func TestReleaseSupplyChainWorkflowContract(t *testing.T) {
	t.Parallel()

	workflow := readDocumentation(t, "../.github/workflows/release.yml")
	assertContainsAll(t, "release workflow", workflow,
		"id-token: write",
		"attestations: write",
		"anchore/sbom-action@v0.24.3",
		"sigstore/cosign-installer@v4.1.0",
		"actions/attest@v4",
		"-trimpath",
		"SOURCE_DATE_EPOCH",
		"GITHUB_SHA",
		"GITHUB_REPOSITORY",
		"go_version",
		"goos",
		"goarch",
		"build_time",
		"go_mod_sha256",
		"go_sum_sha256",
		"sbom.spdx.json",
		"provenance.json",
		"petalflow-linux-amd64",
		"petalflow-darwin-arm64",
		"petalflow-windows-amd64.exe",
		"test -s",
		"checksums-sha256.txt",
		"cosign sign-blob",
		"cosign verify-blob",
		"sha256sum -c",
		"retention-days: 7",
	)
}

func TestReleaseSupplyChainDocumentationContract(t *testing.T) {
	t.Parallel()

	release := readDocumentation(t, "../docs/release.md")
	assertContainsAll(t, "release guide", release,
		"SBOM",
		"SPDX",
		"provenance",
		"Sigstore",
		"cosign verify-blob",
		"sha256sum -c",
		"gh attestation verify",
		"retention",
		"petalflow-linux-amd64",
		"petalflow-darwin-arm64",
		"petalflow-windows-amd64.exe",
	)
}

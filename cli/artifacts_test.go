package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestArtifactsUploadCommandStreamsFile(t *testing.T) {
	var gotAuthorization, gotContentType, gotBody string
	reference := artifact.Reference{Digest: strings.Repeat("a", 64), Size: 11, ContentType: "text/plain"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artifact_id": artifact.ReferenceID(reference), "uri": artifact.ReferenceURI(reference),
			"download_url": "/api/runs/run-1/artifacts/id", "digest": reference.Digest,
			"size": reference.Size, "content_type": reference.ContentType,
		})
	}))
	defer server.Close()
	t.Setenv("PETALFLOW_API_TOKEN", "secret-token")
	input := writeTestFile(t, "artifact.txt", "upload body")
	cmd := NewArtifactsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"upload", "run-1", input, "--server", server.URL, "--content-type", "text/plain"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAuthorization != "Bearer secret-token" || gotContentType != "text/plain" || gotBody != "upload body" {
		t.Fatalf("authorization=%q content-type=%q body=%q", gotAuthorization, gotContentType, gotBody)
	}
}

func TestArtifactsDownloadCommandVerifiesDigest(t *testing.T) {
	const body = "download body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Artifact-SHA256", "e1bd0be8ffa10c22583bff572795b685ad333a7560b1d1478a49aef74b5cd38a")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	t.Setenv("PETALFLOW_API_TOKEN", "secret-token")
	output := filepath.Join(t.TempDir(), "download.txt")
	cmd := NewArtifactsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"download", "run-1", "ref-id", "--server", server.URL, "--output", output})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != body {
		t.Fatalf("download=%q err=%v", data, err)
	}
}

func TestArtifactsDownloadRemovesDigestMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Artifact-SHA256", strings.Repeat("0", 64))
		_, _ = io.WriteString(w, "corrupt")
	}))
	defer server.Close()
	t.Setenv("PETALFLOW_API_TOKEN", "secret-token")
	output := filepath.Join(t.TempDir(), "download.txt")
	cmd := NewArtifactsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"download", "run-1", "ref-id", "--server", server.URL, "--output", output})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute error = nil")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("partial output remains: %v", err)
	}
}

func TestArtifactsCommandRequiresToken(t *testing.T) {
	t.Setenv("PETALFLOW_API_TOKEN", "")
	cmd := NewArtifactsCmd()
	cmd.SetArgs([]string{"download", "run-1", "ref-id", "--output", filepath.Join(t.TempDir(), "out")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "PETALFLOW_API_TOKEN") {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestArtifactAPIURLValidation(t *testing.T) {
	for _, serverURL := range []string{"", "ftp://example.com", "https://user@example.com", "https://example.com?token=secret"} {
		if _, err := artifactAPIURL(serverURL, []string{"api", "runs", "run-1"}); err == nil {
			t.Errorf("artifactAPIURL(%q) error = nil", serverURL)
		}
	}
	if _, err := artifactAPIURL("https://example.com", []string{"api", "runs", ".."}); err == nil {
		t.Fatal("artifactAPIURL path traversal error = nil")
	}
	if got, err := artifactAPIURL("https://example.com/base", []string{"api", "runs", "run-1"}); err != nil || got != "https://example.com/base/api/runs/run-1" {
		t.Fatalf("artifactAPIURL=%q err=%v", got, err)
	}
}

func TestArtifactAPIStatusErrorUsesTypedCodeOnly(t *testing.T) {
	response := &http.Response{
		Status: "413 Request Entity Too Large",
		Body:   io.NopCloser(strings.NewReader(`{"error":{"code":"ARTIFACT_TOO_LARGE","message":"untrusted details"}}`)),
	}
	err := artifactAPIStatusError(response)
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_TOO_LARGE") || strings.Contains(err.Error(), "untrusted details") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteArtifactUploadResponseRejectsInvalidMetadata(t *testing.T) {
	if err := writeArtifactUploadResponse(io.Discard, strings.NewReader(`{"artifact_id":"invalid"}`)); err == nil {
		t.Fatal("writeArtifactUploadResponse error = nil")
	}
}

func TestWriteVerifiedArtifactRequiresDigestAndExclusiveOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "artifact")
	response := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader("body"))}
	if err := writeVerifiedArtifact(output, response); err == nil {
		t.Fatal("missing digest error = nil")
	}
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	response.Header.Set("X-Artifact-SHA256", "230d8358dc8e8890b4c598532f055ab2a7b8b5ef0b5b0c8b8c35d8d737fbeec9")
	response.Body = io.NopCloser(strings.NewReader("body"))
	if err := writeVerifiedArtifact(output, response); err == nil {
		t.Fatal("existing output error = nil")
	}
	data, _ := os.ReadFile(output)
	if string(data) != "existing" {
		t.Fatalf("existing output changed: %q", data)
	}
}

func TestOpenRootedArtifactOutputRejectsDirectoryPath(t *testing.T) {
	if _, _, _, err := openRootedArtifactOutput(t.TempDir()); err == nil {
		t.Fatal("directory output path error = nil")
	}
}

func TestWriteVerifiedArtifactDoesNotFollowOutputSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "download")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	response := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader("replacement"))}
	response.Header.Set("X-Artifact-SHA256", "a2a3d57dd0a0717fdb31543bd0cc1f79902454f2e8b3a9771e3d71f9a178e7b")
	if err := writeVerifiedArtifact(link, response); err == nil {
		t.Fatal("symlink output error = nil")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("target=%q err=%v", data, err)
	}
}

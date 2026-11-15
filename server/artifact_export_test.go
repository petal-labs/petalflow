package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
)

type corruptExportArtifactStore struct{ artifact.Store }

func (s corruptExportArtifactStore) Get(context.Context, artifact.Scope, artifact.Reference) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("x")), nil
}

func TestRunExportBundlesReferencedArtifactsWithDigests(t *testing.T) {
	ctx := context.Background()
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	runs := runtime.NewMemoryRunStore()
	events := newTestEventStore(t)
	put := func(content string) artifact.Metadata {
		metadata, putErr := store.Put(ctx, artifact.PutRequest{
			Scope:      artifact.Scope{TenantID: "tenant-a", RunID: "run-export"},
			WorkflowID: "workflow-a", ContentType: "text/plain",
		}, strings.NewReader(content))
		if putErr != nil {
			t.Fatal(putErr)
		}
		return metadata
	}
	checkpointArtifact := put("checkpoint artifact")
	eventArtifact := put("event artifact")
	unreferenced := put("unreferenced artifact")
	record := &runtime.RunRecord{
		ID: "run-export", TenantID: "tenant-a", WorkflowID: "workflow-a", Status: runtime.RunStatusCompleted,
		ResumeToken: "must-not-be-exported",
		Checkpoint: &runtime.Checkpoint{Envelope: &core.Envelope{Artifacts: []core.Artifact{{
			ID: "checkpoint", URI: artifact.ReferenceURI(checkpointArtifact.Reference),
		}}}},
	}
	if err := runs.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ctx, runtime.Event{
		RunID: record.ID, Seq: 1, Kind: runtime.EventNodeFinished,
		Payload: map[string]any{"artifact": artifact.ReferenceID(eventArtifact.Reference)},
	}); err != nil {
		t.Fatal(err)
	}
	auth := security.BearerTokenAuthenticator("test-token", security.Identity{Subject: "test", TenantID: "tenant-a"})
	handler := NewServer(ServerConfig{
		RunStore: runs, EventStore: events, ArtifactStore: store,
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-export/export", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Export-SHA256") == "" || response.Header().Get("Content-Disposition") == "" {
		t.Fatalf("export headers = %v", response.Header())
	}
	bundleDigest := sha256.Sum256(response.Body.Bytes())
	if response.Header().Get("X-Export-SHA256") != hex.EncodeToString(bundleDigest[:]) {
		t.Fatal("bundle digest header does not match response")
	}

	files := readRunExport(t, response.Body.Bytes())
	var manifest RunExportManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if manifest.RunID != record.ID || len(manifest.Artifacts) != 2 || manifest.RunSHA256 == "" || manifest.EventsSHA256 == "" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.RunSHA256 != sha256Hex(files["run.json"]) || manifest.EventsSHA256 != sha256Hex(files["events.json"]) {
		t.Fatal("manifest run or event digest does not match archive")
	}
	if bytes.Contains(files["run.json"], []byte(record.ResumeToken)) {
		t.Fatal("resume token leaked into run export")
	}
	for _, metadata := range []artifact.Metadata{checkpointArtifact, eventArtifact} {
		path := "artifacts/" + artifact.ReferenceID(metadata.Reference)
		body, ok := files[path]
		if !ok {
			t.Errorf("missing %s", path)
		} else if sha256Hex(body) != metadata.Reference.Digest {
			t.Errorf("digest mismatch for %s", path)
		}
	}
	if _, ok := files["artifacts/"+artifact.ReferenceID(unreferenced.Reference)]; ok {
		t.Fatal("unreferenced artifact included in export")
	}
}

func TestRunExportRejectsNonTerminalRun(t *testing.T) {
	handler, _, _ := newArtifactTestServer(t, "tenant-a", 1024)
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/export", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "RUN_NOT_SETTLED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunExportRequiresEventsAndRejectsCorruptArtifacts(t *testing.T) {
	ctx := context.Background()
	store, _ := artifact.NewInMemoryStore()
	runs := runtime.NewMemoryRunStore()
	metadata, err := store.Put(ctx, artifact.PutRequest{
		Scope: artifact.Scope{TenantID: "tenant-a", RunID: "run-terminal"}, ContentType: "text/plain",
	}, strings.NewReader("valid content"))
	if err != nil {
		t.Fatal(err)
	}
	record := &runtime.RunRecord{
		ID: metadata.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusCompleted,
		Checkpoint: &runtime.Checkpoint{Envelope: &core.Envelope{Artifacts: []core.Artifact{{
			URI: artifact.ReferenceURI(metadata.Reference),
		}}}},
	}
	if err := runs.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	auth := security.BearerTokenAuthenticator("test-token", security.Identity{Subject: "test", TenantID: "tenant-a"})
	request := func(handler http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/run-terminal/export", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	withoutEvents := NewServer(ServerConfig{
		RunStore: runs, ArtifactStore: store,
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	}).Handler()
	if response := request(withoutEvents); response.Code != http.StatusNotImplemented {
		t.Fatalf("without events status=%d body=%s", response.Code, response.Body.String())
	}
	events := newTestEventStore(t)
	corrupt := NewServer(ServerConfig{
		RunStore: runs, EventStore: events, ArtifactStore: corruptExportArtifactStore{Store: store},
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	}).Handler()
	if response := request(corrupt); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("corrupt status=%d body=%s", response.Code, response.Body.String())
	}
	failingEvents := NewServer(ServerConfig{
		RunStore: runs, EventStore: artifactGCErrorEventStore{EventStore: events}, ArtifactStore: store,
		Security: SecurityConfig{RequireAuth: true, Authenticator: auth},
	}).Handler()
	if response := request(failingEvents); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("event error status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMarshalExportJSONRejectsUnsupportedValues(t *testing.T) {
	if _, err := marshalExportJSON(make(chan struct{})); err == nil {
		t.Fatal("marshal unsupported value error = nil")
	}
}

func readRunExport(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	files := make(map[string][]byte)
	reader := tar.NewReader(gzipReader)
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[header.Name] = body
	}
	return files
}

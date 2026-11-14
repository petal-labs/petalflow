package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
)

func TestArtifactUploadAndStreamingDownload(t *testing.T) {
	handler, _, _ := newArtifactTestServer(t, "tenant-a", 1024)
	upload := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", strings.NewReader("artifact body"))
	upload.Header.Set("Authorization", "Bearer test-token")
	upload.Header.Set("Content-Type", "text/plain")
	uploadResult := httptest.NewRecorder()
	handler.ServeHTTP(uploadResult, upload)
	if uploadResult.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadResult.Code, uploadResult.Body.String())
	}
	var response ArtifactUploadResponse
	if err := json.Unmarshal(uploadResult.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ArtifactID == "" || response.DownloadURL == "" || response.Size != int64(len("artifact body")) {
		t.Fatalf("upload response = %+v", response)
	}

	download := httptest.NewRequest(http.MethodGet, response.DownloadURL, nil)
	download.Header.Set("Authorization", "Bearer test-token")
	downloadResult := httptest.NewRecorder()
	handler.ServeHTTP(downloadResult, download)
	if downloadResult.Code != http.StatusOK || downloadResult.Body.String() != "artifact body" {
		t.Fatalf("download status=%d body=%q", downloadResult.Code, downloadResult.Body.String())
	}
	if got := downloadResult.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := downloadResult.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("Content-Disposition=%q", got)
	}
	if downloadResult.Header().Get("Content-Length") != "13" || downloadResult.Header().Get("Digest") == "" || downloadResult.Header().Get("X-Artifact-SHA256") == "" {
		t.Fatalf("download headers = %v", downloadResult.Header())
	}
}

func TestArtifactRoutesHideCrossTenantRuns(t *testing.T) {
	handler, store, _ := newArtifactTestServer(t, "tenant-b", 1024)
	scope := artifact.Scope{TenantID: "tenant-b", RunID: "run-1"}
	metadata, err := store.Put(context.Background(), artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, strings.NewReader("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", strings.NewReader("denied")),
		httptest.NewRequest(http.MethodGet, "/api/runs/run-1/artifacts/"+artifact.ReferenceID(metadata.Reference), nil),
	} {
		request.Header.Set("Authorization", "Bearer test-token")
		request.Header.Set("Content-Type", "text/plain")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusNotFound || !strings.Contains(result.Body.String(), `"code":"NOT_FOUND"`) {
			t.Fatalf("%s status=%d body=%s", request.Method, result.Code, result.Body.String())
		}
	}
}

func TestArtifactUploadRejectsOverLimitWithoutPartialAcceptance(t *testing.T) {
	handler, store, _ := newArtifactTestServer(t, "tenant-a", 4)
	request := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", strings.NewReader("12345"))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "text/plain")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusRequestEntityTooLarge || !strings.Contains(result.Body.String(), "ARTIFACT_TOO_LARGE") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	items, err := store.ListByRun(context.Background(), artifact.Scope{TenantID: "tenant-a", RunID: "run-1"})
	if err != nil || len(items) != 0 {
		t.Fatalf("stored items=%v err=%v", items, err)
	}
}

func TestArtifactUploadRejectsDisallowedContentType(t *testing.T) {
	handler, _, _ := newArtifactTestServer(t, "tenant-a", 1024)
	request := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", bytes.NewReader([]byte{1, 2}))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/octet-stream")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusUnprocessableEntity || !strings.Contains(result.Body.String(), "ARTIFACT_POLICY_REJECTED") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestArtifactUploadMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipartWriter(t, &body, "file", "notes.txt", "text/plain", "multipart body")
	handler, _, _ := newArtifactTestServer(t, "tenant-a", 1024)
	request := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", &body)
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", writer)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestArtifactDownloadMissingAndInvalidAreHidden(t *testing.T) {
	handler, _, _ := newArtifactTestServer(t, "tenant-a", 1024)
	reference := artifact.Reference{Digest: strings.Repeat("a", 64), Size: 1, ContentType: "text/plain"}
	var bodies []string
	for _, id := range []string{"invalid", artifact.ReferenceID(reference)} {
		request := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/artifacts/"+id, nil)
		request.Header.Set("Authorization", "Bearer test-token")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusNotFound {
			t.Fatalf("id=%q status=%d body=%s", id, result.Code, result.Body.String())
		}
		bodies = append(bodies, result.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("invalid and missing responses differ: %q != %q", bodies[0], bodies[1])
	}
}

func TestArtifactRoutesRequireConfiguredStores(t *testing.T) {
	auth := security.BearerTokenAuthenticator("test-token", security.Identity{Subject: "test", TenantID: "tenant-a"})
	handler := NewServer(ServerConfig{Security: SecurityConfig{RequireAuth: true, Authenticator: auth}}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", strings.NewReader("body"))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "text/plain")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestArtifactUploadReaderValidation(t *testing.T) {
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/", nil),
		headerRequest("multipart/form-data", ""),
		headerRequest("multipart/form-data; boundary=empty", ""),
	}
	for _, request := range requests {
		if _, _, _, err := artifactUploadReader(request); err == nil {
			t.Errorf("Content-Type %q error = nil", request.Header.Get("Content-Type"))
		}
	}
	raw := headerRequest("text/plain; charset=utf-8", "body")
	if _, contentType, _, err := artifactUploadReader(raw); err != nil || contentType != "text/plain; charset=utf-8" {
		t.Fatalf("raw content type=%q err=%v", contentType, err)
	}
}

func TestArtifactErrorWriters(t *testing.T) {
	server := NewServer(ServerConfig{})
	storeCases := []struct {
		err    error
		status int
	}{
		{errArtifactUploadTooLarge, http.StatusRequestEntityTooLarge},
		{fmt.Errorf("content exceeds limit: %w", artifact.ErrPolicyRejected), http.StatusRequestEntityTooLarge},
		{fmt.Errorf("type denied: %w", artifact.ErrPolicyRejected), http.StatusUnprocessableEntity},
		{context.Canceled, http.StatusRequestTimeout},
		{errors.New("unavailable"), http.StatusServiceUnavailable},
	}
	for _, test := range storeCases {
		result := httptest.NewRecorder()
		server.writeArtifactStoreError(result, test.err)
		if result.Code != test.status {
			t.Errorf("error=%v status=%d want=%d", test.err, result.Code, test.status)
		}
	}
	missing := httptest.NewRecorder()
	server.writeArtifactLookupError(missing, artifact.ErrNotFound)
	unavailable := httptest.NewRecorder()
	server.writeArtifactLookupError(unavailable, errors.New("failed"))
	if missing.Code != http.StatusNotFound || unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup statuses missing=%d unavailable=%d", missing.Code, unavailable.Code)
	}
}

func TestArtifactUploadBodyLimitOnlyAppliesToExactRoute(t *testing.T) {
	server := NewServer(ServerConfig{})
	valid := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/artifacts", nil)
	nested := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/extra/artifacts", nil)
	wrongMethod := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/artifacts", nil)
	if !server.isArtifactUpload(valid) || server.isArtifactUpload(nested) || server.isArtifactUpload(wrongMethod) {
		t.Fatal("artifact upload route classification is not exact")
	}
}

func TestValidateUploadedArtifact(t *testing.T) {
	scope := artifact.Scope{TenantID: "tenant-a", RunID: "run-1"}
	reference := artifact.Reference{Digest: strings.Repeat("a", 64), Size: 1, ContentType: "text/plain"}
	metadata := artifact.Metadata{Scope: scope, Reference: reference}
	if err := validateUploadedArtifact(metadata, scope, 1, reference.Digest); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}
	if err := validateUploadedArtifact(metadata, scope, 2, reference.Digest); err == nil {
		t.Fatal("mismatched metadata error = nil")
	}
}

func headerRequest(contentType, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request
}

func newArtifactTestServer(t *testing.T, runTenant string, maxBytes int64) (http.Handler, *artifact.InMemoryStore, *runtime.MemoryRunStore) {
	t.Helper()
	store, err := artifact.NewInMemoryStore(artifact.InMemoryConfig{MaxSizeBytes: maxBytes, AllowedContentTypes: []string{"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	runs := runtime.NewMemoryRunStore()
	if err := runs.Create(context.Background(), &runtime.RunRecord{ID: "run-1", TenantID: runTenant}); err != nil {
		t.Fatal(err)
	}
	auth := security.BearerTokenAuthenticator("test-token", security.Identity{Subject: "test", TenantID: "tenant-a"})
	server := NewServer(ServerConfig{
		RunStore: runs, ArtifactStore: store,
		ArtifactPolicy: runtime.ArtifactPolicy{MaxArtifactBytes: maxBytes, AllowedContentTypes: []string{"text/plain"}},
		Security:       SecurityConfig{RequireAuth: true, Authenticator: auth},
	})
	return server.Handler(), store, runs
}

func multipartWriter(t *testing.T, body io.Writer, field, filename, contentType, content string) string {
	t.Helper()
	writer := multipart.NewWriter(body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, content)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType()
}

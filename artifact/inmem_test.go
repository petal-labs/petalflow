package artifact_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/artifact"
)

func TestInMemoryStoreRoundTripAndIdempotency(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	content := []byte("artifact payload")
	req := artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: "tenant-1", RunID: "run-1"},
		WorkflowID:  "workflow-1",
		NodeID:      "node-1",
		ContentType: "text/plain; charset=utf-8",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	}

	first, err := store.Put(ctx, req, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	second, err := store.Put(ctx, req, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("idempotent Put metadata differs:\nfirst:  %+v\nsecond: %+v", first, second)
	}

	wantDigest := sha256.Sum256(content)
	if first.Reference.Digest != hex.EncodeToString(wantDigest[:]) {
		t.Errorf("digest = %q, want SHA-256", first.Reference.Digest)
	}
	if first.Reference.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", first.Reference.Size, len(content))
	}

	reader, err := store.Get(ctx, req.Scope, first.Reference)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("Get content = %q, want %q", got, content)
	}

	stat, err := store.Stat(ctx, req.Scope, first.Reference)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !reflect.DeepEqual(stat, first) {
		t.Fatalf("Stat = %+v, want %+v", stat, first)
	}

	listed, err := store.ListByRun(ctx, req.Scope)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], first) {
		t.Fatalf("ListByRun = %+v, want one matching item", listed)
	}
}

func TestInMemoryStoreTenantAndRunIsolation(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	metadata, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "application/octet-stream"}, bytes.NewReader([]byte{1, 2, 3}))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	otherScopes := []artifact.Scope{
		{TenantID: "tenant-2", RunID: "run-1"},
		{TenantID: "tenant-1", RunID: "run-2"},
	}
	for _, other := range otherScopes {
		if _, err := store.Get(ctx, other, metadata.Reference); !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("Get(%+v) error = %v, want ErrNotFound", other, err)
		}
		listed, err := store.ListByRun(ctx, other)
		if err != nil {
			t.Fatalf("ListByRun(%+v): %v", other, err)
		}
		if len(listed) != 0 {
			t.Errorf("ListByRun(%+v) leaked %d items", other, len(listed))
		}
	}
}

func TestInMemoryStoreDeleteRemovesOnlyScopedReference(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	content := []byte("shared")
	firstScope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	secondScope := artifact.Scope{TenantID: "tenant-1", RunID: "run-2"}
	request := func(scope artifact.Scope) artifact.PutRequest {
		return artifact.PutRequest{Scope: scope, ContentType: "text/plain"}
	}
	first, err := store.Put(ctx, request(firstScope), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := store.Put(ctx, request(secondScope), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if first.Reference != second.Reference {
		t.Fatalf("same bytes produced different references: %+v != %+v", first.Reference, second.Reference)
	}

	if err := store.Delete(ctx, firstScope, first.Reference); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Stat(ctx, firstScope, first.Reference); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("deleted Stat error = %v, want ErrNotFound", err)
	}
	reader, err := store.Get(ctx, secondScope, second.Reference)
	if err != nil {
		t.Fatalf("Get retained reference: %v", err)
	}
	_ = reader.Close()
	if err := store.Delete(ctx, secondScope, second.Reference); err != nil {
		t.Fatalf("Delete final reference: %v", err)
	}
}

func TestInMemoryStoreRejectsPolicyViolationsAtomically(t *testing.T) {
	store, err := artifact.NewInMemoryStore(artifact.InMemoryConfig{
		MaxSizeBytes:        4,
		AllowedContentTypes: []string{"text/plain"},
	})
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}

	tests := []artifact.PutRequest{
		{Scope: scope, ContentType: "application/json"},
		{Scope: scope, ContentType: "text/plain"},
	}
	contents := [][]byte{[]byte(`{}`), []byte("oversized")}
	for i, req := range tests {
		if _, err := store.Put(ctx, req, bytes.NewReader(contents[i])); !errors.Is(err, artifact.ErrPolicyRejected) {
			t.Errorf("Put case %d error = %v, want ErrPolicyRejected", i, err)
		}
	}

	listed, err := store.ListByRun(ctx, scope)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("rejected puts left %d visible entries", len(listed))
	}
}

func TestInMemoryStoreHonorsCanceledContext(t *testing.T) {
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Put(ctx, artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: "tenant-1", RunID: "run-1"},
		ContentType: "text/plain",
	}, bytes.NewReader([]byte("content")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put error = %v, want context.Canceled", err)
	}
}

func TestInMemoryStoreResolvesRetentionAtWriteTime(t *testing.T) {
	store, err := artifact.NewInMemoryStore(artifact.InMemoryConfig{Retention: artifact.RetentionPolicy{
		Default: 3 * time.Hour, Tenants: map[string]time.Duration{"tenant-1": 2 * time.Hour},
		Workflows: map[string]time.Duration{"workflow-1": time.Hour},
	}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.Put(context.Background(), artifact.PutRequest{
		Scope:      artifact.Scope{TenantID: "tenant-1", RunID: "run-1"},
		WorkflowID: "workflow-1", ContentType: "text/plain",
	}, strings.NewReader("retained"))
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(metadata.ExpiresAt)
	if remaining <= 59*time.Minute || remaining > time.Hour {
		t.Fatalf("workflow retention remaining = %s, want about 1h", remaining)
	}
}

func TestInMemoryStoreLifecycleCandidatesAndConditionalDelete(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	put := func(tenant, run string, expires time.Time) artifact.Metadata {
		metadata, err := store.Put(ctx, artifact.PutRequest{
			Scope:       artifact.Scope{TenantID: tenant, RunID: run},
			ContentType: "text/plain", ExpiresAt: expires,
		}, strings.NewReader(tenant+run))
		if err != nil {
			t.Fatal(err)
		}
		return metadata
	}
	first := put("tenant-1", "run-a", now.Add(-2*time.Hour))
	second := put("tenant-1", "run-b", now.Add(-time.Hour))
	_ = put("tenant-2", "run-c", now.Add(-time.Hour))
	_ = put("tenant-1", "run-future", now.Add(time.Hour))

	items, err := store.ListExpired(ctx, now, "tenant-1", 1, 1)
	if err != nil || len(items) != 1 || items[0].Scope.RunID != second.Scope.RunID {
		t.Fatalf("ListExpired = %+v, %v", items, err)
	}
	deleted, err := store.DeleteExpired(ctx, first, now)
	if err != nil || !deleted {
		t.Fatalf("DeleteExpired = %v, %v", deleted, err)
	}
	deleted, err = store.DeleteExpired(ctx, first, now)
	if err != nil || deleted {
		t.Fatalf("second DeleteExpired = %v, %v", deleted, err)
	}
	if _, err := store.ListExpired(ctx, now, "bad tenant", 0, 1); err == nil {
		t.Fatal("invalid tenant error = nil")
	}
	if _, err := store.ListExpired(ctx, now, "", -1, 1); err == nil {
		t.Fatal("negative offset error = nil")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListExpired(canceled, now, "", 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ListExpired error = %v", err)
	}
}

func TestInMemoryStoreValidatesOperationInputs(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	validScope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	validRef := artifact.Reference{Digest: strings.Repeat("0", artifact.SHA256HexLength), ContentType: "text/plain"}

	if _, err := store.Put(ctx, artifact.PutRequest{Scope: validScope, ContentType: "text/plain"}, nil); err == nil {
		t.Fatal("Put with nil reader error = nil, want validation error")
	}
	if _, err := store.Put(ctx, artifact.PutRequest{Scope: artifact.Scope{RunID: "run-1"}, ContentType: "text/plain"}, bytes.NewReader(nil)); err == nil {
		t.Fatal("Put with invalid scope error = nil, want validation error")
	}
	if _, err := store.Get(ctx, artifact.Scope{RunID: "run-1"}, validRef); err == nil {
		t.Fatal("Get with invalid scope error = nil, want validation error")
	}
	if _, err := store.Stat(ctx, validScope, artifact.Reference{}); err == nil {
		t.Fatal("Stat with invalid reference error = nil, want validation error")
	}
	if err := store.Delete(ctx, validScope, validRef); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
	if _, err := store.ListByRun(ctx, artifact.Scope{RunID: "run-1"}); err == nil {
		t.Fatal("ListByRun with invalid scope error = nil, want validation error")
	}
}

func TestInMemoryStoreOperationsHonorCanceledContext(t *testing.T) {
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	ref := artifact.Reference{Digest: strings.Repeat("0", artifact.SHA256HexLength), ContentType: "text/plain"}

	if _, err := store.Get(ctx, scope, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Get error = %v, want context.Canceled", err)
	}
	if _, err := store.Stat(ctx, scope, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Stat error = %v, want context.Canceled", err)
	}
	if err := store.Delete(ctx, scope, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete error = %v, want context.Canceled", err)
	}
	if _, err := store.ListByRun(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Errorf("ListByRun error = %v, want context.Canceled", err)
	}
}

func TestInMemoryStoreReaderFailureIsAtomic(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	reader := io.MultiReader(bytes.NewReader([]byte("partial")), failingReader{})
	_, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, reader)
	if err == nil {
		t.Fatal("Put error = nil, want reader failure")
	}
	listed, listErr := store.ListByRun(ctx, scope)
	if listErr != nil {
		t.Fatalf("ListByRun: %v", listErr)
	}
	if len(listed) != 0 {
		t.Fatalf("failed Put left %d visible entries", len(listed))
	}
}

func TestNewInMemoryStoreRejectsInvalidConfig(t *testing.T) {
	tests := []artifact.InMemoryConfig{
		{MaxSizeBytes: -1},
		{AllowedContentTypes: []string{"invalid"}},
		{Retention: artifact.RetentionPolicy{Default: -time.Second}},
	}
	for _, config := range tests {
		if _, err := artifact.NewInMemoryStore(config); err == nil {
			t.Errorf("NewInMemoryStore(%+v) error = nil, want validation error", config)
		}
	}
	if _, err := artifact.NewInMemoryStore(artifact.InMemoryConfig{}, artifact.InMemoryConfig{}); err == nil {
		t.Error("NewInMemoryStore with two configs error = nil, want validation error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("reader failed")
}

func newStore(t *testing.T) *artifact.InMemoryStore {
	t.Helper()
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	return store
}

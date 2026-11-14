// Package artifacttest provides a reusable conformance suite for artifact.Store
// implementations. External backends should run it against isolated storage.
package artifacttest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/artifact"
)

// RunConformance verifies round trips, digest integrity, metadata fidelity,
// scope isolation, deletion, and the required terminal failure classes.
func RunConformance(t *testing.T, store artifact.Store) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	scope := artifact.Scope{TenantID: "petalflow-conformance", RunID: "run-" + suffix}

	t.Run("round_trip_and_metadata", func(t *testing.T) {
		testRoundTrip(t, ctx, store, scope)
	})
	t.Run("tenant_and_run_isolation", func(t *testing.T) {
		testIsolation(t, ctx, store, scope)
	})
	t.Run("not_found_classification", func(t *testing.T) {
		testNotFound(t, ctx, store, scope)
	})
	t.Run("delete", func(t *testing.T) {
		testDelete(t, ctx, store, artifact.Scope{TenantID: scope.TenantID, RunID: scope.RunID + "-delete"})
	})
	t.Run("policy_rejection_classification", func(t *testing.T) {
		_, err := store.Put(ctx, artifact.PutRequest{Scope: scope}, bytes.NewReader(nil))
		if !errors.Is(err, artifact.ErrPolicyRejected) {
			t.Fatalf("Put without content type error = %v, want ErrPolicyRejected", err)
		}
	})
}

// CheckUnavailable verifies the retryable failure contract for a backend that
// has been configured by its test to be unavailable.
func CheckUnavailable(t *testing.T, store artifact.Store) {
	t.Helper()
	scope := artifact.Scope{TenantID: "petalflow-conformance", RunID: "unavailable"}
	ref := artifact.Reference{Digest: strings.Repeat("0", artifact.SHA256HexLength), ContentType: "application/octet-stream"}
	_, err := store.Stat(context.Background(), scope, ref)
	if !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatalf("Stat error = %v, want ErrUnavailable", err)
	}
}

func testRoundTrip(t *testing.T, ctx context.Context, store artifact.Store, scope artifact.Scope) {
	t.Helper()
	content := []byte("artifact conformance payload")
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	request := artifact.PutRequest{
		Scope: scope, WorkflowID: "workflow-1", NodeID: "node-1",
		ContentType: "text/plain; charset=utf-8", ExpiresAt: expiresAt,
	}
	metadata, err := store.Put(ctx, request, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	wantDigest := sha256.Sum256(content)
	if metadata.Reference.Digest != hex.EncodeToString(wantDigest[:]) {
		t.Errorf("digest = %q, want SHA-256", metadata.Reference.Digest)
	}
	if metadata.Reference.Size != int64(len(content)) || metadata.Reference.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("reference = %+v, want size and normalized content type", metadata.Reference)
	}
	if metadata.Scope != scope || metadata.WorkflowID != request.WorkflowID || metadata.NodeID != request.NodeID || !metadata.ExpiresAt.Equal(expiresAt) {
		t.Errorf("metadata = %+v, want request metadata preserved", metadata)
	}
	assertContent(t, ctx, store, metadata, content)

	stat, err := store.Stat(ctx, scope, metadata.Reference)
	if err != nil || !reflect.DeepEqual(stat, metadata) {
		t.Fatalf("Stat = %+v, %v; want %+v", stat, err, metadata)
	}
	listed, err := store.ListByRun(ctx, scope)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if !containsMetadata(listed, metadata) {
		t.Fatalf("ListByRun did not contain stored metadata: %+v", listed)
	}
}

func assertContent(t *testing.T, ctx context.Context, store artifact.Store, metadata artifact.Metadata, want []byte) {
	t.Helper()
	reader, err := store.Get(ctx, metadata.Scope, metadata.Reference)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
	digest := sha256.Sum256(got)
	if hex.EncodeToString(digest[:]) != metadata.Reference.Digest {
		t.Fatal("returned content does not match reference digest")
	}
}

func testIsolation(t *testing.T, ctx context.Context, store artifact.Store, scope artifact.Scope) {
	t.Helper()
	metadata, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, bytes.NewReader([]byte("scoped")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, other := range []artifact.Scope{
		{TenantID: scope.TenantID + "-other", RunID: scope.RunID},
		{TenantID: scope.TenantID, RunID: scope.RunID + "-other"},
	} {
		if _, err := store.Get(ctx, other, metadata.Reference); !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("Get in scope %+v error = %v, want ErrNotFound", other, err)
		}
	}
}

func testNotFound(t *testing.T, ctx context.Context, store artifact.Store, scope artifact.Scope) {
	t.Helper()
	digest := sha256.Sum256([]byte("missing"))
	ref := artifact.Reference{Digest: hex.EncodeToString(digest[:]), Size: 7, ContentType: "text/plain"}
	if _, err := store.Stat(ctx, scope, ref); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Stat missing error = %v, want ErrNotFound", err)
	}
}

func testDelete(t *testing.T, ctx context.Context, store artifact.Store, scope artifact.Scope) {
	t.Helper()
	metadata, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, bytes.NewReader([]byte("delete me")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, scope, metadata.Reference); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, scope, metadata.Reference); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Get after Delete error = %v, want ErrNotFound", err)
	}
}

func containsMetadata(items []artifact.Metadata, want artifact.Metadata) bool {
	for _, item := range items {
		if reflect.DeepEqual(item, want) {
			return true
		}
	}
	return false
}

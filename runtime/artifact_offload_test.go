package runtime_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

func TestRuntimeOffloadsLargeArtifacts(t *testing.T) {
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	g := graph.NewGraph("artifact-offload")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = append(env.Artifacts, core.Artifact{ID: "report", Type: "document", MimeType: "text/plain", Text: "large payload"})
		return env, nil
	}))
	g.SetEntry("produce")

	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 4, MaxArtifactBytes: 1024, MaxRunBytes: 2048}
	result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := result.Artifacts[0]
	if got.Text != "" || len(got.Bytes) != 0 || got.URI == "" {
		t.Fatalf("artifact was not replaced by reference: %+v", got)
	}
	ref, err := artifact.ParseReferenceURI(got.URI)
	if err != nil {
		t.Fatalf("ParseReferenceURI: %v", err)
	}
	if got.Meta[artifact.MetaID] != artifact.ReferenceID(ref) {
		t.Fatalf("artifact ID metadata = %v", got.Meta[artifact.MetaID])
	}
	reader, err := store.Get(context.Background(), artifact.Scope{TenantID: "tenant-1", RunID: result.Trace.RunID}, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	if string(data) != "large payload" {
		t.Fatalf("stored data = %q", data)
	}
}

func TestRuntimeArtifactPolicyReturnsTypedError(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	g := graph.NewGraph("artifact-policy")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{{ID: "huge", Type: "file", MimeType: "application/octet-stream", Bytes: []byte(strings.Repeat("x", 9))}}
		return env, nil
	}))
	g.SetEntry("produce")
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1, MaxArtifactBytes: 8}
	_, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if !errors.Is(err, runtime.ErrArtifactPolicy) {
		t.Fatalf("Run error = %v, want ErrArtifactPolicy", err)
	}
	if runtime.ErrorCodeFor(err) != "artifact_policy_rejected" {
		t.Fatalf("error code = %q", runtime.ErrorCodeFor(err))
	}
}

func TestRuntimeLeavesSmallArtifactsInline(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	g := graph.NewGraph("small-artifact")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{{ID: "small", Type: "text", Text: "ok"}}
		return env, nil
	}))
	g.SetEntry("produce")
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 8, MaxArtifactBytes: 32}
	result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifacts[0].Text != "ok" || result.Artifacts[0].URI != "" {
		t.Fatalf("small artifact changed: %+v", result.Artifacts[0])
	}
}

func TestDurableCheckpointStoresArtifactReferenceOnly(t *testing.T) {
	artifactStore, _ := artifact.NewInMemoryStore()
	runStore := runtime.NewMemoryRunStore()
	g := graph.NewGraph("durable-artifact")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{{ID: "output", Type: "text", Text: "checkpoint payload"}}
		return env, nil
	}))
	g.SetEntry("produce")
	opts := runtime.DefaultRunOptions()
	opts.RunID = "run-durable-artifact"
	opts.TenantID = "tenant-1"
	opts.RunStore = runStore
	opts.ArtifactStore = artifactStore
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1}
	if _, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	record, err := runStore.Get(context.Background(), opts.RunID)
	if err != nil {
		t.Fatal(err)
	}
	got := record.Checkpoint.Envelope.Artifacts[0]
	if got.URI == "" || got.Text != "" || len(got.Bytes) != 0 {
		t.Fatalf("checkpoint artifact = %+v, want reference only", got)
	}
}

func TestRuntimeRetriesUnavailableArtifactStore(t *testing.T) {
	base, _ := artifact.NewInMemoryStore()
	store := &unavailableStore{Store: base, failures: 1}
	g := graph.NewGraph("artifact-retry")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{{ID: "retry", Type: "text", Text: "retry payload"}}
		return env, nil
	}))
	g.SetEntry("produce")
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1, MaxRetries: 1}
	result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.puts != 2 || result.Artifacts[0].URI == "" {
		t.Fatalf("puts=%d artifact=%+v", store.puts, result.Artifacts[0])
	}
}

func TestRuntimeReturnsTypedErrorAfterArtifactRetries(t *testing.T) {
	base, _ := artifact.NewInMemoryStore()
	store := &unavailableStore{Store: base, failures: 3}
	g := artifactGraph(core.Artifact{ID: "failed", Type: "text", Text: "payload"})
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1, MaxRetries: 1}
	_, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if !errors.Is(err, runtime.ErrArtifactOffload) {
		t.Fatalf("Run error = %v, want ErrArtifactOffload", err)
	}
	if runtime.ErrorCodeFor(err) != "artifact_offload_failed" || store.puts != 2 {
		t.Fatalf("error code=%q puts=%d", runtime.ErrorCodeFor(err), store.puts)
	}
}

func TestRuntimeRejectsMismatchedStoreMetadata(t *testing.T) {
	base, _ := artifact.NewInMemoryStore()
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = &mismatchedStore{Store: base}
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1}
	_, err := runtime.NewRuntime().Run(context.Background(), artifactGraph(core.Artifact{ID: "bad-ref", Type: "text", Text: "payload"}), core.NewEnvelope(), opts)
	if !errors.Is(err, runtime.ErrArtifactOffload) {
		t.Fatalf("Run error = %v, want ErrArtifactOffload", err)
	}
}

func TestRuntimeArtifactContentTypeAndRunLimits(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	tests := []struct {
		name   string
		art    core.Artifact
		policy runtime.ArtifactPolicy
	}{
		{name: "content type", art: core.Artifact{ID: "json", Type: "json", MimeType: "application/json", Text: `{}`}, policy: runtime.ArtifactPolicy{OffloadThresholdBytes: 1, AllowedContentTypes: []string{"text/plain"}}},
		{name: "run bytes", art: core.Artifact{ID: "large", Type: "text", Text: "12345"}, policy: runtime.ArtifactPolicy{OffloadThresholdBytes: 1, MaxRunBytes: 4}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := runtime.DefaultRunOptions()
			opts.TenantID = "tenant-1"
			opts.ArtifactStore = store
			opts.ArtifactPolicy = test.policy
			if _, err := runtime.NewRuntime().Run(context.Background(), artifactGraph(test.art), core.NewEnvelope(), opts); !errors.Is(err, runtime.ErrArtifactPolicy) {
				t.Fatalf("Run error = %v, want ErrArtifactPolicy", err)
			}
		})
	}
}

func TestRuntimeOffloadsBinaryAndMixedArtifacts(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	g := graph.NewGraph("artifact-encodings")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{
			{ID: "binary", Type: "file", Bytes: []byte{1, 2, 3}},
			{ID: "mixed", Type: "file", Text: "caption", Bytes: []byte{4, 5}},
		}
		return env, nil
	}))
	g.SetEntry("produce")
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "tenant-1"
	opts.ArtifactStore = store
	opts.ArtifactPolicy = runtime.ArtifactPolicy{OffloadThresholdBytes: 1}
	result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range result.Artifacts {
		if value.URI == "" || value.Text != "" || len(value.Bytes) != 0 {
			t.Fatalf("artifact = %+v, want reference only", value)
		}
	}
}

func TestRuntimeAcceptsAndTracksExistingArtifactReference(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "existing-ref-run"}
	metadata, err := store.Put(context.Background(), artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, strings.NewReader("stored"))
	if err != nil {
		t.Fatal(err)
	}
	env := core.NewEnvelope()
	env.Artifacts = []core.Artifact{{ID: "existing", Type: "text", URI: artifact.ReferenceURI(metadata.Reference)}}
	g := graph.NewGraph("existing-ref")
	_ = g.AddNode(core.NewNoopNode("noop"))
	g.SetEntry("noop")
	opts := runtime.DefaultRunOptions()
	opts.RunID = scope.RunID
	opts.TenantID = scope.TenantID
	opts.ArtifactStore = store
	result, err := runtime.NewRuntime().Run(context.Background(), g, env, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Artifacts[0].URI != env.Artifacts[0].URI {
		t.Fatalf("reference changed: %+v", result.Artifacts[0])
	}
}

func artifactGraph(value core.Artifact) *graph.BasicGraph {
	g := graph.NewGraph("artifact-test")
	_ = g.AddNode(core.NewFuncNode("produce", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Artifacts = []core.Artifact{value}
		return env, nil
	}))
	g.SetEntry("produce")
	return g
}

type unavailableStore struct {
	artifact.Store
	puts     int
	failures int
}

type mismatchedStore struct{ artifact.Store }

func (s *mismatchedStore) Put(ctx context.Context, request artifact.PutRequest, content io.Reader) (artifact.Metadata, error) {
	metadata, err := s.Store.Put(ctx, request, content)
	metadata.Reference.Size++
	return metadata, err
}

func (s *unavailableStore) Put(ctx context.Context, request artifact.PutRequest, content io.Reader) (artifact.Metadata, error) {
	s.puts++
	if s.puts <= s.failures {
		return artifact.Metadata{}, artifact.ErrUnavailable
	}
	return s.Store.Put(ctx, request, content)
}

package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/core"
)

const (
	DefaultArtifactOffloadThreshold int64 = 64 << 10
	DefaultArtifactMaxBytes         int64 = 64 << 20
	DefaultArtifactMaxRunBytes      int64 = 256 << 20
	DefaultArtifactMaxCount               = 1024
)

var (
	ErrArtifactPolicy  = errors.New("artifact policy rejected")
	ErrArtifactOffload = errors.New("artifact offload failed")
)

// ArtifactPolicy bounds inline and externally stored artifact content.
type ArtifactPolicy struct {
	OffloadThresholdBytes int64
	MaxArtifactBytes      int64
	MaxRunBytes           int64
	MaxArtifacts          int
	AllowedContentTypes   []string
	MaxRetries            int
	RetryBackoff          time.Duration
}

// ArtifactError is an actionable artifact policy or storage failure.
type ArtifactError struct {
	ArtifactID string
	Reason     string
	Retryable  bool
	Cause      error
	policy     bool
}

func (e *ArtifactError) Error() string {
	prefix := "artifact offload"
	if e.policy {
		prefix = "artifact policy"
	}
	if e.ArtifactID != "" {
		prefix += " for " + e.ArtifactID
	}
	if e.Reason != "" {
		prefix += ": " + e.Reason
	}
	return prefix
}

func (e *ArtifactError) Unwrap() error { return e.Cause }

func (e *ArtifactError) Is(target error) bool {
	return target == ErrArtifactPolicy && e.policy || target == ErrArtifactOffload && !e.policy
}

type artifactRunTracker struct {
	mu      sync.Mutex
	bytes   int64
	count   int
	digests map[string]struct{}
}

func normalizedArtifactPolicy(policy ArtifactPolicy) ArtifactPolicy {
	if policy.OffloadThresholdBytes <= 0 {
		policy.OffloadThresholdBytes = DefaultArtifactOffloadThreshold
	}
	if policy.MaxArtifactBytes <= 0 {
		policy.MaxArtifactBytes = DefaultArtifactMaxBytes
	}
	if policy.MaxRunBytes <= 0 {
		policy.MaxRunBytes = DefaultArtifactMaxRunBytes
	}
	if policy.MaxArtifacts <= 0 {
		policy.MaxArtifacts = DefaultArtifactMaxCount
	}
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	} else if policy.MaxRetries == 0 {
		policy.MaxRetries = 2
	}
	if policy.RetryBackoff <= 0 {
		policy.RetryBackoff = 50 * time.Millisecond
	}
	policy.AllowedContentTypes = append([]string(nil), policy.AllowedContentTypes...)
	return policy
}

func prepareArtifactOptions(opts RunOptions) RunOptions {
	if opts.ArtifactStore == nil {
		return opts
	}
	opts.ArtifactPolicy = normalizedArtifactPolicy(opts.ArtifactPolicy)
	if opts.artifactTracker == nil {
		opts.artifactTracker = &artifactRunTracker{digests: make(map[string]struct{})}
	}
	return opts
}

func offloadEnvelope(ctx context.Context, env *core.Envelope, nodeID string, opts RunOptions) (*core.Envelope, error) {
	if env == nil || opts.ArtifactStore == nil {
		return env, nil
	}
	result := env.Clone()
	for index := range result.Artifacts {
		offloaded, err := offloadArtifact(ctx, result.Artifacts[index], nodeID, env.Trace.RunID, opts)
		if err != nil {
			return nil, err
		}
		result.Artifacts[index] = offloaded
	}
	return result, nil
}

func offloadArtifact(ctx context.Context, value core.Artifact, nodeID, runID string, opts RunOptions) (core.Artifact, error) {
	if strings.HasPrefix(value.URI, artifact.ReferenceScheme+"://") {
		return trackArtifactReference(value, opts)
	}
	if value.URI != "" || len(value.Text)+len(value.Bytes) == 0 {
		return value, nil
	}
	payload, contentType, encoding, err := artifactPayload(value)
	if err != nil {
		return core.Artifact{}, policyArtifactError(value.ID, err.Error())
	}
	size := int64(len(payload))
	if err := validateArtifactPolicy(value.ID, contentType, size, opts.ArtifactPolicy); err != nil {
		return core.Artifact{}, err
	}
	if size <= opts.ArtifactPolicy.OffloadThresholdBytes {
		return value, nil
	}
	if opts.TenantID == "" || runID == "" {
		return core.Artifact{}, policyArtifactError(value.ID, "tenant and run IDs are required for scoped storage")
	}
	digest := sha256.Sum256(payload)
	digestString := hex.EncodeToString(digest[:])
	added, err := opts.artifactTracker.reserve(digestString, size, opts.ArtifactPolicy)
	if err != nil {
		return core.Artifact{}, policyArtifactError(value.ID, err.Error())
	}
	request := artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: opts.TenantID, RunID: runID},
		WorkflowID:  opts.WorkflowID,
		NodeID:      nodeID,
		ContentType: contentType,
	}
	metadata, err := putArtifactWithRetry(ctx, opts.ArtifactStore, request, payload, opts.ArtifactPolicy)
	if err != nil {
		if added {
			opts.artifactTracker.release(digestString, size)
		}
		if errors.Is(err, artifact.ErrPolicyRejected) {
			return core.Artifact{}, policyArtifactError(value.ID, "store rejected content policy")
		}
		return core.Artifact{}, storageArtifactError(value.ID, err)
	}
	if err := validateStoredArtifact(metadata, request.Scope, digestString, size); err != nil {
		if added {
			opts.artifactTracker.release(digestString, size)
		}
		return core.Artifact{}, storageArtifactError(value.ID, err)
	}
	value.Text = ""
	value.Bytes = nil
	value.URI = artifact.ReferenceURI(metadata.Reference)
	value.Meta = artifactReferenceMeta(value.Meta, metadata.Reference, encoding)
	return value, nil
}

func validateStoredArtifact(metadata artifact.Metadata, scope artifact.Scope, digest string, size int64) error {
	if err := metadata.Reference.Validate(); err != nil {
		return fmt.Errorf("store returned invalid reference: %w", err)
	}
	if metadata.Scope != scope || metadata.Reference.Digest != digest || metadata.Reference.Size != size {
		return errors.New("store returned mismatched artifact metadata")
	}
	return nil
}

func trackArtifactReference(value core.Artifact, opts RunOptions) (core.Artifact, error) {
	reference, err := artifact.ParseReferenceURI(value.URI)
	if err != nil {
		return core.Artifact{}, policyArtifactError(value.ID, "invalid artifact reference")
	}
	if _, err := opts.artifactTracker.reserve(reference.Digest, reference.Size, opts.ArtifactPolicy); err != nil {
		return core.Artifact{}, policyArtifactError(value.ID, err.Error())
	}
	return value, nil
}

func artifactPayload(value core.Artifact) ([]byte, string, string, error) {
	contentType := strings.TrimSpace(value.MimeType)
	if contentType == "" {
		if len(value.Bytes) > 0 {
			contentType = "application/octet-stream"
		} else {
			contentType = "text/plain; charset=utf-8"
		}
	}
	if len(value.Bytes) == 0 {
		return []byte(value.Text), contentType, "text", nil
	}
	if value.Text == "" {
		return append([]byte(nil), value.Bytes...), contentType, "bytes", nil
	}
	payload, err := json.Marshal(struct {
		Text  string `json:"text"`
		Bytes []byte `json:"bytes"`
	}{Text: value.Text, Bytes: value.Bytes})
	return payload, "application/vnd.petalflow.artifact+json", "text+bytes", err
}

func validateArtifactPolicy(id, contentType string, size int64, policy ArtifactPolicy) error {
	if size > policy.MaxArtifactBytes {
		return policyArtifactError(id, fmt.Sprintf("size %d exceeds per-artifact limit %d", size, policy.MaxArtifactBytes))
	}
	if len(policy.AllowedContentTypes) == 0 {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return policyArtifactError(id, "invalid content type")
	}
	for _, allowed := range policy.AllowedContentTypes {
		allowedType, _, parseErr := mime.ParseMediaType(allowed)
		if parseErr == nil && strings.EqualFold(mediaType, allowedType) {
			return nil
		}
	}
	return policyArtifactError(id, fmt.Sprintf("content type %q is not allowed", contentType))
}

func putArtifactWithRetry(ctx context.Context, store artifact.Store, request artifact.PutRequest, payload []byte, policy ArtifactPolicy) (artifact.Metadata, error) {
	var err error
	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		var metadata artifact.Metadata
		metadata, err = store.Put(ctx, request, bytes.NewReader(payload))
		if err == nil {
			return metadata, nil
		}
		if !errors.Is(err, artifact.ErrUnavailable) || attempt == policy.MaxRetries {
			return artifact.Metadata{}, err
		}
		timer := time.NewTimer(policy.RetryBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return artifact.Metadata{}, ctx.Err()
		case <-timer.C:
		}
	}
	return artifact.Metadata{}, err
}

func (t *artifactRunTracker) reserve(digest string, size int64, policy ArtifactPolicy) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.digests[digest]; exists {
		return false, nil
	}
	if t.count+1 > policy.MaxArtifacts {
		return false, fmt.Errorf("artifact count exceeds per-run limit %d", policy.MaxArtifacts)
	}
	if t.bytes+size > policy.MaxRunBytes {
		return false, fmt.Errorf("artifact bytes exceed per-run limit %d", policy.MaxRunBytes)
	}
	t.digests[digest] = struct{}{}
	t.count++
	t.bytes += size
	return true, nil
}

func (t *artifactRunTracker) release(digest string, size int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.digests[digest]; !exists {
		return
	}
	delete(t.digests, digest)
	t.count--
	t.bytes -= size
}

func artifactReferenceMeta(meta map[string]any, reference artifact.Reference, encoding string) map[string]any {
	result := make(map[string]any, len(meta)+5)
	for key, value := range meta {
		result[key] = value
	}
	result[artifact.MetaDigest] = reference.Digest
	result[artifact.MetaSize] = reference.Size
	result[artifact.MetaContentType] = reference.ContentType
	result[artifact.MetaEncoding] = encoding
	result[artifact.MetaID] = artifact.ReferenceID(reference)
	return result
}

func policyArtifactError(id, reason string) *ArtifactError {
	return &ArtifactError{ArtifactID: id, Reason: reason, policy: true, Cause: ErrArtifactPolicy}
}

func storageArtifactError(id string, cause error) *ArtifactError {
	return &ArtifactError{ArtifactID: id, Reason: "store write failed", Retryable: errors.Is(cause, artifact.ErrUnavailable), Cause: cause}
}

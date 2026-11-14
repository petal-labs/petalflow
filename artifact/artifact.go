// Package artifact defines PetalFlow's pluggable external artifact storage
// contract. It depends only on the Go standard library so storage backends can
// implement Store without importing PetalFlow internals.
//
// Store implementations must provide these guarantees:
//
//   - Put is atomic: a failed Put must not make content or metadata visible.
//   - Content is immutable: bytes returned for a Reference never change.
//   - References are content-addressed with SHA-256 and include enough metadata
//     for callers to verify every read independently.
//   - Every operation is scoped to a tenant and run. A reference that exists in
//     another scope must be reported as not found.
//   - Successfully returned metadata is visible to Stat and ListByRun before
//     Put returns.
//
// Implementations should wrap the sentinel errors in this package so callers
// can classify terminal policy failures separately from retryable outages.
package artifact

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"
	"time"
)

const (
	// SHA256HexLength is the encoded length of a SHA-256 digest.
	SHA256HexLength     = 64
	maxIdentifierLength = 256
)

var (
	// ErrNotFound means the artifact is not visible in the requested scope.
	ErrNotFound = errors.New("artifact not found")
	// ErrUnavailable means the backing store is temporarily unavailable.
	ErrUnavailable = errors.New("artifact store unavailable")
	// ErrPolicyRejected means content was rejected by a size or content-type policy.
	ErrPolicyRejected = errors.New("artifact rejected by policy")

	identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// Scope authorizes access to artifacts belonging to one tenant and run.
type Scope struct {
	TenantID string `json:"tenant_id"`
	RunID    string `json:"run_id"`
}

// Validate checks that both identifiers are present and safe for storage keys.
func (s Scope) Validate() error {
	for field, value := range map[string]string{"tenant_id": s.TenantID, "run_id": s.RunID} {
		if value == "" {
			return fmt.Errorf("artifact scope: %s is required", field)
		}
		if err := validateIdentifier(field, value); err != nil {
			return err
		}
	}
	return nil
}

// Reference is a stable, content-addressed artifact identifier. Digest is the
// lowercase hexadecimal SHA-256 of the content.
type Reference struct {
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// Validate checks that the reference can be independently verified.
func (r Reference) Validate() error {
	if len(r.Digest) != SHA256HexLength || strings.ToLower(r.Digest) != r.Digest {
		return fmt.Errorf("artifact reference: digest must be %d lowercase hexadecimal characters", SHA256HexLength)
	}
	if _, err := hex.DecodeString(r.Digest); err != nil {
		return fmt.Errorf("artifact reference: invalid digest: %w", err)
	}
	if r.Size < 0 {
		return fmt.Errorf("artifact reference: size must not be negative")
	}
	if _, err := normalizeContentType(r.ContentType); err != nil {
		return fmt.Errorf("artifact reference: %w", err)
	}
	return nil
}

// PutRequest supplies ownership, origin, and retention metadata for content.
type PutRequest struct {
	Scope       Scope     `json:"scope"`
	WorkflowID  string    `json:"workflow_id,omitempty"`
	NodeID      string    `json:"node_id,omitempty"`
	ContentType string    `json:"content_type"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

// Validate checks all request metadata. Content-type violations wrap
// ErrPolicyRejected because callers should treat them as terminal.
func (r PutRequest) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	for field, value := range map[string]string{"workflow_id": r.WorkflowID, "node_id": r.NodeID} {
		if value == "" {
			continue
		}
		if err := validateIdentifier(field, value); err != nil {
			return err
		}
	}
	if _, err := normalizeContentType(r.ContentType); err != nil {
		return fmt.Errorf("%w: %v", ErrPolicyRejected, err)
	}
	return nil
}

// Metadata describes stored content without requiring a content read.
type Metadata struct {
	Reference  Reference `json:"reference"`
	Scope      Scope     `json:"scope"`
	WorkflowID string    `json:"workflow_id,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

// Store is the pluggable artifact storage interface. Implementations must be
// safe for concurrent use. Get returns a new reader owned by the caller.
type Store interface {
	Put(ctx context.Context, request PutRequest, content io.Reader) (Metadata, error)
	Get(ctx context.Context, scope Scope, reference Reference) (io.ReadCloser, error)
	Stat(ctx context.Context, scope Scope, reference Reference) (Metadata, error)
	Delete(ctx context.Context, scope Scope, reference Reference) error
	ListByRun(ctx context.Context, scope Scope) ([]Metadata, error)
}

func validateIdentifier(field, value string) error {
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("artifact: %s exceeds %d characters", field, maxIdentifierLength)
	}
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("artifact: %s contains invalid characters", field)
	}
	return nil
}

func normalizeContentType(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("content_type is required")
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") {
		return "", fmt.Errorf("invalid content_type %q", value)
	}
	return mime.FormatMediaType(strings.ToLower(mediaType), params), nil
}

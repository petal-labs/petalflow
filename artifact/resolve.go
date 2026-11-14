package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

const DefaultResolveMaxBytes int64 = 64 << 20

// ResolveURI reads and independently verifies a run-scoped artifact URI.
func ResolveURI(ctx context.Context, resolver Resolver, uri string) ([]byte, Reference, error) {
	reference, err := ParseReferenceURI(uri)
	if err != nil {
		return nil, Reference{}, err
	}
	if resolver.Store == nil {
		return nil, Reference{}, fmt.Errorf("artifact resolve: store is required")
	}
	maxBytes := resolver.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultResolveMaxBytes
	}
	if reference.Size > maxBytes {
		return nil, Reference{}, fmt.Errorf("artifact resolve: size %d exceeds limit %d", reference.Size, maxBytes)
	}
	reader, err := resolver.Store.Get(ctx, resolver.Scope, reference)
	if err != nil {
		return nil, Reference{}, fmt.Errorf("artifact resolve: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, reference.Size+1))
	if err != nil {
		return nil, Reference{}, fmt.Errorf("artifact resolve: read: %w", err)
	}
	if int64(len(data)) != reference.Size {
		return nil, Reference{}, fmt.Errorf("artifact resolve: size mismatch")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != reference.Digest {
		return nil, Reference{}, fmt.Errorf("artifact resolve: digest mismatch")
	}
	return data, reference, nil
}

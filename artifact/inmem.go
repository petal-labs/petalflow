package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"
)

// InMemoryConfig configures content policies for InMemoryStore. A zero maximum
// size and an empty allowed-content-types list mean unbounded and unrestricted.
type InMemoryConfig struct {
	MaxSizeBytes        int64
	AllowedContentTypes []string
}

// InMemoryStore is a concurrent, process-local Store implementation intended
// for tests, embedded use, and backend development.
type InMemoryStore struct {
	mu           sync.RWMutex
	config       InMemoryConfig
	allowedTypes map[string]struct{}
	blobs        map[string][]byte
	entries      map[string]map[string]Metadata
	references   map[string]int
}

// NewInMemoryStore creates an empty store. At most one configuration may be
// supplied. Configuration slices are copied before use.
func NewInMemoryStore(configs ...InMemoryConfig) (*InMemoryStore, error) {
	if len(configs) > 1 {
		return nil, fmt.Errorf("artifact in-memory store: at most one config is allowed")
	}
	var config InMemoryConfig
	if len(configs) == 1 {
		config = configs[0]
	}
	allowed, err := validateInMemoryConfig(config)
	if err != nil {
		return nil, err
	}
	config.AllowedContentTypes = append([]string(nil), config.AllowedContentTypes...)
	return &InMemoryStore{
		config:       config,
		allowedTypes: allowed,
		blobs:        make(map[string][]byte),
		entries:      make(map[string]map[string]Metadata),
		references:   make(map[string]int),
	}, nil
}

// Put reads and validates all content before atomically making it visible.
func (s *InMemoryStore) Put(ctx context.Context, request PutRequest, content io.Reader) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	if err := request.Validate(); err != nil {
		return Metadata{}, fmt.Errorf("artifact put: %w", err)
	}
	if content == nil {
		return Metadata{}, fmt.Errorf("artifact put: content reader is required")
	}
	contentType, _ := normalizeContentType(request.ContentType)
	if !s.contentTypeAllowed(contentType) {
		return Metadata{}, fmt.Errorf("artifact put: %w: content type %q", ErrPolicyRejected, contentType)
	}

	data, err := s.readContent(content)
	if err != nil {
		return Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	return s.commit(request, contentType, data), nil
}

// Get opens immutable content only when the reference belongs to scope.
func (s *InMemoryStore) Get(ctx context.Context, scope Scope, reference Reference) (io.ReadCloser, error) {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return nil, fmt.Errorf("artifact get: %w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.lookupMetadata(scope, reference); !ok {
		return nil, fmt.Errorf("artifact get: %w", ErrNotFound)
	}
	data, ok := s.blobs[referenceKey(reference)]
	if !ok {
		return nil, fmt.Errorf("artifact get: %w: content missing", ErrUnavailable)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}

// Stat returns metadata without reading artifact content.
func (s *InMemoryStore) Stat(ctx context.Context, scope Scope, reference Reference) (Metadata, error) {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return Metadata{}, fmt.Errorf("artifact stat: %w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	metadata, ok := s.lookupMetadata(scope, reference)
	if !ok {
		return Metadata{}, fmt.Errorf("artifact stat: %w", ErrNotFound)
	}
	return metadata, nil
}

// Delete removes the reference from one scope. Shared immutable content remains
// available to any other scope that references it.
func (s *InMemoryStore) Delete(ctx context.Context, scope Scope, reference Reference) error {
	if err := validateLookup(ctx, scope, reference); err != nil {
		return fmt.Errorf("artifact delete: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scopeEntries, ok := s.entries[scopeKey(scope)]
	key := referenceKey(reference)
	if !ok {
		return fmt.Errorf("artifact delete: %w", ErrNotFound)
	}
	if _, ok := scopeEntries[key]; !ok {
		return fmt.Errorf("artifact delete: %w", ErrNotFound)
	}
	delete(scopeEntries, key)
	if len(scopeEntries) == 0 {
		delete(s.entries, scopeKey(scope))
	}
	s.references[key]--
	if s.references[key] == 0 {
		delete(s.references, key)
		delete(s.blobs, key)
	}
	return nil
}

// ListByRun returns a deterministic snapshot of metadata visible to scope.
func (s *InMemoryStore) ListByRun(ctx context.Context, scope Scope) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("artifact list by run: %w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Metadata, 0, len(s.entries[scopeKey(scope)]))
	for _, metadata := range s.entries[scopeKey(scope)] {
		items = append(items, metadata)
	}
	sort.Slice(items, func(i, j int) bool {
		return referenceKey(items[i].Reference) < referenceKey(items[j].Reference)
	})
	return items, nil
}

func validateInMemoryConfig(config InMemoryConfig) (map[string]struct{}, error) {
	if config.MaxSizeBytes < 0 {
		return nil, fmt.Errorf("artifact in-memory store: max size must not be negative")
	}
	allowed := make(map[string]struct{}, len(config.AllowedContentTypes))
	for _, value := range config.AllowedContentTypes {
		normalized, err := normalizeContentType(value)
		if err != nil {
			return nil, fmt.Errorf("artifact in-memory store: %w", err)
		}
		allowed[normalized] = struct{}{}
	}
	return allowed, nil
}

func (s *InMemoryStore) contentTypeAllowed(contentType string) bool {
	if len(s.allowedTypes) == 0 {
		return true
	}
	_, ok := s.allowedTypes[contentType]
	return ok
}

func (s *InMemoryStore) readContent(content io.Reader) ([]byte, error) {
	reader := content
	if s.config.MaxSizeBytes > 0 && s.config.MaxSizeBytes < math.MaxInt64 {
		reader = io.LimitReader(content, s.config.MaxSizeBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("artifact put: read content: %w", err)
	}
	if s.config.MaxSizeBytes > 0 && int64(len(data)) > s.config.MaxSizeBytes {
		return nil, fmt.Errorf("artifact put: %w: content exceeds %d bytes", ErrPolicyRejected, s.config.MaxSizeBytes)
	}
	return data, nil
}

func (s *InMemoryStore) commit(request PutRequest, contentType string, data []byte) Metadata {
	digest := sha256.Sum256(data)
	reference := Reference{Digest: hex.EncodeToString(digest[:]), Size: int64(len(data)), ContentType: contentType}
	key := referenceKey(reference)
	scope := scopeKey(request.Scope)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[scope][key]; ok {
		return existing
	}
	metadata := Metadata{
		Reference: reference, Scope: request.Scope, WorkflowID: request.WorkflowID,
		NodeID: request.NodeID, CreatedAt: time.Now().UTC(), ExpiresAt: request.ExpiresAt,
	}
	if s.entries[scope] == nil {
		s.entries[scope] = make(map[string]Metadata)
	}
	s.blobs[key] = append([]byte(nil), data...)
	s.entries[scope][key] = metadata
	s.references[key]++
	return metadata
}

func (s *InMemoryStore) lookupMetadata(scope Scope, reference Reference) (Metadata, bool) {
	metadata, ok := s.entries[scopeKey(scope)][referenceKey(reference)]
	return metadata, ok
}

func validateLookup(ctx context.Context, scope Scope, reference Reference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	return reference.Validate()
}

func scopeKey(scope Scope) string {
	return scope.TenantID + "\x00" + scope.RunID
}

func referenceKey(reference Reference) string {
	return fmt.Sprintf("%s\x00%d\x00%s", reference.Digest, reference.Size, reference.ContentType)
}

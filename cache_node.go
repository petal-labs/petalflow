package petalflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
)

// CacheStore is the interface for cache storage backends.
type CacheStore interface {
	// Get retrieves a cached envelope by key.
	// Returns the envelope, whether it was found, and any error.
	Get(ctx context.Context, key string) (*Envelope, bool, error)

	// Set stores an envelope with the given key.
	// TTL of 0 means no expiration.
	Set(ctx context.Context, key string, env *Envelope, ttl time.Duration) error

	// Delete removes a cached entry.
	Delete(ctx context.Context, key string) error
}

// CacheResult contains metadata about a cache operation.
type CacheResult struct {
	Hit       bool      `json:"hit"`
	Key       string    `json:"key"`
	StoredAt  time.Time `json:"stored_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// CacheNodeConfig configures a CacheNode.
type CacheNodeConfig struct {
	// CacheKey specifies how to generate the cache key.
	// Supports template syntax: "{{.input}}" or "{{.vars.query}}".
	// If empty, the key is computed from InputVars.
	CacheKey string

	// InputVars lists variables to include in the cache key hash.
	// If empty and CacheKey is empty, uses all Vars.
	InputVars []string

	// WrappedNode is the node to execute on cache miss.
	WrappedNode Node

	// Store is the cache storage backend.
	// If nil, a new MemoryCacheStore is created.
	Store CacheStore

	// TTL is the time-to-live for cache entries.
	// 0 means no expiration.
	TTL time.Duration

	// OutputVar stores cache metadata (hit/miss, key).
	OutputVar string

	// IncludeArtifacts includes artifacts in cache key computation.
	IncludeArtifacts bool

	// IncludeInput includes the Input field in cache key computation.
	IncludeInput bool
}

// CacheNode wraps another node and caches its results.
// On cache hit, it returns the cached envelope without executing the wrapped node.
// On cache miss, it executes the wrapped node and caches the result.
type CacheNode struct {
	BaseNode
	config CacheNodeConfig
}

// NewCacheNode creates a new CacheNode with the given configuration.
func NewCacheNode(id string, config CacheNodeConfig) *CacheNode {
	// Set defaults
	if config.Store == nil {
		config.Store = NewMemoryCacheStore()
	}

	return &CacheNode{
		BaseNode: NewBaseNode(id, NodeKindCache),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *CacheNode) Config() CacheNodeConfig {
	return n.config
}

// Run executes the cache node logic.
func (n *CacheNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Check context
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Generate cache key
	key, err := n.generateCacheKey(env)
	if err != nil {
		return nil, fmt.Errorf("cache node %s: failed to generate cache key: %w", n.id, err)
	}

	// Check cache
	cachedEnv, found, err := n.config.Store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("cache node %s: failed to get from cache: %w", n.id, err)
	}

	if found {
		// Cache hit - clone and return cached envelope
		result := cachedEnv.Clone()

		// Store cache result if configured
		if n.config.OutputVar != "" {
			result.SetVar(n.config.OutputVar, CacheResult{
				Hit: true,
				Key: key,
			})
		}

		return result, nil
	}

	// Cache miss - execute wrapped node
	if n.config.WrappedNode == nil {
		return nil, fmt.Errorf("cache node %s: no wrapped node configured", n.id)
	}

	// Execute wrapped node
	resultEnv, err := n.config.WrappedNode.Run(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("cache node %s: wrapped node failed: %w", n.id, err)
	}

	// Store in cache
	storedAt := time.Now()
	var expiresAt time.Time
	if n.config.TTL > 0 {
		expiresAt = storedAt.Add(n.config.TTL)
	}

	// Clone before storing to prevent mutation issues
	if err := n.config.Store.Set(ctx, key, resultEnv.Clone(), n.config.TTL); err != nil {
		return nil, fmt.Errorf("cache node %s: failed to store in cache: %w", n.id, err)
	}

	// Clone result for return
	result := resultEnv.Clone()

	// Store cache result if configured
	if n.config.OutputVar != "" {
		result.SetVar(n.config.OutputVar, CacheResult{
			Hit:       false,
			Key:       key,
			StoredAt:  storedAt,
			ExpiresAt: expiresAt,
		})
	}

	return result, nil
}

// generateCacheKey creates a deterministic cache key from the envelope.
func (n *CacheNode) generateCacheKey(env *Envelope) (string, error) {
	// If CacheKey template is provided, use it
	if n.config.CacheKey != "" {
		return n.renderCacheKeyTemplate(env)
	}

	// Otherwise, compute hash from selected inputs
	return n.computeCacheKeyHash(env)
}

// renderCacheKeyTemplate renders the CacheKey template with envelope data.
func (n *CacheNode) renderCacheKeyTemplate(env *Envelope) (string, error) {
	tmpl, err := template.New("cachekey").Parse(n.config.CacheKey)
	if err != nil {
		return "", fmt.Errorf("invalid cache key template: %w", err)
	}

	// Build template data
	data := make(map[string]any)
	data["input"] = env.Input
	data["vars"] = env.Vars

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute cache key template: %w", err)
	}

	// Prefix with node ID for uniqueness
	return fmt.Sprintf("%s:%s", n.id, buf.String()), nil
}

// computeCacheKeyHash computes a deterministic hash from envelope data.
func (n *CacheNode) computeCacheKeyHash(env *Envelope) (string, error) {
	// Build data to hash
	hashData := make(map[string]any)

	// Include Input if configured
	if n.config.IncludeInput {
		hashData["input"] = env.Input
	}

	// Include specific vars or all vars
	if len(n.config.InputVars) > 0 {
		vars := make(map[string]any)
		for _, varName := range n.config.InputVars {
			if val, ok := env.GetVar(varName); ok {
				vars[varName] = val
			}
		}
		hashData["vars"] = vars
	} else {
		// Include all vars
		hashData["vars"] = env.Vars
	}

	// Include artifacts if configured
	if n.config.IncludeArtifacts {
		hashData["artifacts"] = env.Artifacts
	}

	// Compute deterministic hash
	hash, err := computeStableHash(hashData)
	if err != nil {
		return "", err
	}

	// Prefix with node ID for uniqueness
	return fmt.Sprintf("%s:%s", n.id, hash), nil
}

// computeStableHash creates a deterministic hash of the given data.
// It handles map key ordering to ensure consistent hashes.
func computeStableHash(data any) (string, error) {
	// Convert to stable JSON
	jsonBytes, err := stableJSONMarshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal data for hashing: %w", err)
	}

	// Compute SHA256 hash
	hash := sha256.Sum256(jsonBytes)
	return hex.EncodeToString(hash[:]), nil
}

// stableJSONMarshal marshals data to JSON with sorted map keys.
func stableJSONMarshal(v any) ([]byte, error) {
	// Convert to stable representation
	stable := toStableValue(v)

	// Marshal with sorted keys
	return json.Marshal(stable)
}

// toStableValue converts a value to a stable representation for hashing.
// Maps are converted to sorted key-value pairs.
func toStableValue(v any) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]any:
		// Sort keys and convert values recursively
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Use ordered slice of key-value pairs
		pairs := make([]any, 0, len(val)*2)
		for _, k := range keys {
			pairs = append(pairs, k, toStableValue(val[k]))
		}
		return pairs

	case []any:
		// Convert each element
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = toStableValue(item)
		}
		return result

	case []Artifact:
		// Convert artifacts to stable representation
		result := make([]any, len(val))
		for i, art := range val {
			result[i] = toStableValue(map[string]any{
				"id":   art.ID,
				"type": art.Type,
				"text": art.Text,
				"meta": art.Meta,
			})
		}
		return result

	default:
		// Primitives are already stable
		return val
	}
}

// MemoryCacheStore is an in-memory cache implementation with TTL support.
type MemoryCacheStore struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

// cacheEntry holds a cached envelope with metadata.
type cacheEntry struct {
	envelope  *Envelope
	storedAt  time.Time
	expiresAt time.Time
}

// NewMemoryCacheStore creates a new in-memory cache store.
func NewMemoryCacheStore() *MemoryCacheStore {
	return &MemoryCacheStore{
		entries: make(map[string]*cacheEntry),
	}
}

// Get retrieves a cached envelope by key.
func (s *MemoryCacheStore) Get(ctx context.Context, key string) (*Envelope, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	entry, exists := s.entries[key]
	s.mu.RUnlock()

	if !exists {
		return nil, false, nil
	}

	// Check TTL expiration
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		// Entry expired - delete it
		s.mu.Lock()
		delete(s.entries, key)
		s.mu.Unlock()
		return nil, false, nil
	}

	return entry.envelope, true, nil
}

// Set stores an envelope with the given key.
func (s *MemoryCacheStore) Set(ctx context.Context, key string, env *Envelope, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	entry := &cacheEntry{
		envelope: env,
		storedAt: time.Now(),
	}

	if ttl > 0 {
		entry.expiresAt = entry.storedAt.Add(ttl)
	}

	s.mu.Lock()
	s.entries[key] = entry
	s.mu.Unlock()

	return nil
}

// Delete removes a cached entry.
func (s *MemoryCacheStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()

	return nil
}

// Size returns the number of entries in the cache.
func (s *MemoryCacheStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Clear removes all entries from the cache.
func (s *MemoryCacheStore) Clear() {
	s.mu.Lock()
	s.entries = make(map[string]*cacheEntry)
	s.mu.Unlock()
}

// Keys returns all cache keys (for debugging/testing).
func (s *MemoryCacheStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Prune removes all expired entries.
func (s *MemoryCacheStore) Prune() int {
	now := time.Now()
	pruned := 0

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, entry := range s.entries {
		if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
			delete(s.entries, key)
			pruned++
		}
	}

	return pruned
}

// MockNode is a simple node for testing that stores a value.
type MockNode struct {
	BaseNode
	ReturnValue any
	ReturnError error
	CallCount   int
	mu          sync.Mutex
}

// NewMockNode creates a new mock node for testing.
func NewMockNode(id string, returnValue any) *MockNode {
	return &MockNode{
		BaseNode:    NewBaseNode(id, NodeKindNoop),
		ReturnValue: returnValue,
	}
}

// Run executes the mock node.
func (n *MockNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	n.mu.Lock()
	n.CallCount++
	n.mu.Unlock()

	if n.ReturnError != nil {
		return nil, n.ReturnError
	}

	result := env.Clone()
	if n.ReturnValue != nil {
		result.SetVar("mock_result", n.ReturnValue)
	}
	return result, nil
}

// GetCallCount returns the number of times Run was called.
func (n *MockNode) GetCallCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.CallCount
}

// CacheKeyBuilder helps build complex cache keys.
type CacheKeyBuilder struct {
	parts []string
}

// NewCacheKeyBuilder creates a new cache key builder.
func NewCacheKeyBuilder() *CacheKeyBuilder {
	return &CacheKeyBuilder{
		parts: make([]string, 0),
	}
}

// Add adds a part to the cache key.
func (b *CacheKeyBuilder) Add(key string, value any) *CacheKeyBuilder {
	// Convert value to string representation
	var strVal string
	switch v := value.(type) {
	case string:
		strVal = v
	case fmt.Stringer:
		strVal = v.String()
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			strVal = fmt.Sprintf("%v", v)
		} else {
			strVal = string(jsonBytes)
		}
	}
	b.parts = append(b.parts, fmt.Sprintf("%s=%s", key, strVal))
	return b
}

// Build creates the final cache key.
func (b *CacheKeyBuilder) Build() string {
	sort.Strings(b.parts)
	return strings.Join(b.parts, "&")
}

// Hash creates a hashed version of the cache key.
func (b *CacheKeyBuilder) Hash() string {
	key := b.Build()
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// Ensure interface compliance at compile time.
var _ Node = (*CacheNode)(nil)
var _ CacheStore = (*MemoryCacheStore)(nil)

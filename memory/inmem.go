package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/petal-labs/petalflow/core"
)

// InMemoryProvider is a process-local Provider for tests, examples, and local
// development. Data is partitioned by tenant, namespace, and conversation key
// exactly as a durable backend must partition it, so workflows behave the
// same when a real provider is swapped in. Knowledge search uses simple term
// overlap; it is deterministic but not semantic.
type InMemoryProvider struct {
	mu            sync.RWMutex
	conversations map[string][]storedMessage
	documents     map[string][]storedDocument
	nextID        int
}

type storedMessage struct {
	id   string
	msg  core.Message
	meta map[string]string
}

type storedDocument struct {
	collection string
	artifact   core.Artifact
	terms      map[string]int
}

// NewInMemoryProvider creates an empty provider.
func NewInMemoryProvider() *InMemoryProvider {
	return &InMemoryProvider{
		conversations: make(map[string][]storedMessage),
		documents:     make(map[string][]storedDocument),
	}
}

// Name implements Named.
func (p *InMemoryProvider) Name() string { return "inmemory" }

// Recall implements MemoryProvider.
func (p *InMemoryProvider) Recall(ctx context.Context, req RecallRequest) (RecallResult, error) {
	if err := ctx.Err(); err != nil {
		return RecallResult{}, Unavailable(err)
	}
	if err := req.Scope.ValidateConversation(); err != nil {
		return RecallResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultRecallLimit
	}
	roles := make(map[string]bool, len(req.Roles))
	for _, r := range req.Roles {
		roles[r] = true
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	stored := p.conversations[conversationKey(req.Scope)]
	matching := make([]core.Message, 0, len(stored))
	for _, s := range stored {
		if len(roles) > 0 && !roles[s.msg.Role] {
			continue
		}
		matching = append(matching, cloneMessage(s.msg))
	}
	truncated := false
	if len(matching) > limit {
		matching = matching[len(matching)-limit:]
		truncated = true
	}
	return RecallResult{Messages: matching, Truncated: truncated}, nil
}

// Remember implements MemoryProvider.
func (p *InMemoryProvider) Remember(ctx context.Context, req RememberRequest) (RememberResult, error) {
	if err := ctx.Err(); err != nil {
		return RememberResult{}, Unavailable(err)
	}
	if err := req.Scope.ValidateConversation(); err != nil {
		return RememberResult{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := conversationKey(req.Scope)
	ids := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		p.nextID++
		id := fmt.Sprintf("msg-%d", p.nextID)
		p.conversations[key] = append(p.conversations[key], storedMessage{
			id:   id,
			msg:  cloneMessage(m),
			meta: cloneStringMap(req.Metadata),
		})
		ids = append(ids, id)
	}
	return RememberResult{Stored: len(ids), IDs: ids}, nil
}

// AddDocument seeds the knowledge store for a namespace. Collection may be
// empty. The artifact's Text is indexed for Search.
func (p *InMemoryProvider) AddDocument(scope Scope, collection string, artifact core.Artifact) error {
	scope = scope.Normalized()
	if err := scope.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if artifact.ID == "" {
		p.nextID++
		artifact.ID = fmt.Sprintf("doc-%d", p.nextID)
	}
	if artifact.Type == "" {
		artifact.Type = "document"
	}
	key := knowledgeKey(scope)
	p.documents[key] = append(p.documents[key], storedDocument{
		collection: collection,
		artifact:   cloneArtifact(artifact),
		terms:      termFrequencies(artifact.Text),
	})
	return nil
}

// Search implements KnowledgeProvider.
func (p *InMemoryProvider) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, Unavailable(err)
	}
	scope := req.Scope.Normalized()
	if err := scope.Validate(); err != nil {
		return SearchResult{}, err
	}
	topK := req.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	collections := make(map[string]bool, len(req.Collections))
	for _, c := range req.Collections {
		collections[c] = true
	}
	queryTerms := termFrequencies(req.Query)
	if len(queryTerms) == 0 {
		return SearchResult{}, nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	hits := make([]SearchHit, 0)
	for _, doc := range p.documents[knowledgeKey(scope)] {
		if len(collections) > 0 && !collections[doc.collection] {
			continue
		}
		if !matchesFilters(doc.artifact, req.Filters) {
			continue
		}
		score := overlapScore(queryTerms, doc.terms)
		if score <= 0 || score < req.MinScore {
			continue
		}
		hits = append(hits, SearchHit{Artifact: cloneArtifact(doc.artifact), Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	total := len(hits)
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return SearchResult{Hits: hits, TotalFound: total}, nil
}

// Reset removes all stored conversations and documents.
func (p *InMemoryProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conversations = make(map[string][]storedMessage)
	p.documents = make(map[string][]storedDocument)
}

func conversationKey(scope Scope) string {
	scope = scope.Normalized()
	return scope.TenantID + "\x00" + scope.Namespace + "\x00" + scope.ConversationKey()
}

func knowledgeKey(scope Scope) string {
	scope = scope.Normalized()
	return scope.TenantID + "\x00" + scope.Namespace
}

func termFrequencies(text string) map[string]int {
	terms := make(map[string]int)
	isTermRune := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 0x7f
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isTermRune(r) }) {
		if len(word) < 2 {
			continue
		}
		terms[word]++
	}
	return terms
}

// overlapScore is the fraction of distinct query terms present in the
// document, in [0, 1].
func overlapScore(query, doc map[string]int) float64 {
	if len(query) == 0 {
		return 0
	}
	matched := 0
	for term := range query {
		if doc[term] > 0 {
			matched++
		}
	}
	return float64(matched) / float64(len(query))
}

func matchesFilters(a core.Artifact, filters map[string]string) bool {
	for k, want := range filters {
		got, ok := a.Meta[k].(string)
		if !ok || got != want {
			return false
		}
	}
	return true
}

func cloneMessage(m core.Message) core.Message {
	if m.Meta != nil {
		meta := make(map[string]any, len(m.Meta))
		for k, v := range m.Meta {
			meta[k] = v
		}
		m.Meta = meta
	}
	return m
}

func cloneArtifact(a core.Artifact) core.Artifact {
	if a.Meta != nil {
		meta := make(map[string]any, len(a.Meta))
		for k, v := range a.Meta {
			meta[k] = v
		}
		a.Meta = meta
	}
	if a.Bytes != nil {
		a.Bytes = append([]byte(nil), a.Bytes...)
	}
	return a
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Ensure interface compliance at compile time.
var (
	_ Provider = (*InMemoryProvider)(nil)
	_ Named    = (*InMemoryProvider)(nil)
)

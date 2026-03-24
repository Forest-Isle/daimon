package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// EmbeddingCache provides LRU caching for embeddings with TTL.
type EmbeddingCache struct {
	mu      sync.RWMutex
	cache   map[string]*embeddingEntry
	maxSize int
	ttl     time.Duration
}

type embeddingEntry struct {
	embedding []float32
	expiresAt time.Time
}

// NewEmbeddingCache creates a new embedding cache.
func NewEmbeddingCache(maxSize int, ttl time.Duration) *EmbeddingCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &EmbeddingCache{
		cache:   make(map[string]*embeddingEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a cached embedding.
func (c *EmbeddingCache) Get(text, model string) ([]float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.cacheKey(text, model)
	entry, exists := c.cache[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.embedding, true
}

// Set stores an embedding in the cache.
func (c *EmbeddingCache) Set(text, model string, embedding []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict expired entries and enforce size limit
	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	key := c.cacheKey(text, model)
	c.cache[key] = &embeddingEntry{
		embedding: embedding,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// cacheKey generates a cache key from text and model.
func (c *EmbeddingCache) cacheKey(text, model string) string {
	h := sha256.New()
	h.Write([]byte(text))
	h.Write([]byte(model))
	return hex.EncodeToString(h.Sum(nil))
}

// evictOldest removes expired entries or oldest entries if cache is full.
func (c *EmbeddingCache) evictOldest() {
	now := time.Now()
	for key, entry := range c.cache {
		if now.After(entry.expiresAt) {
			delete(c.cache, key)
		}
	}

	// If still over limit, remove arbitrary entries
	if len(c.cache) >= c.maxSize {
		count := len(c.cache) - c.maxSize + 1
		for key := range c.cache {
			delete(c.cache, key)
			count--
			if count <= 0 {
				break
			}
		}
	}
}

// CachedEmbedder wraps an EmbeddingProvider with caching.
type CachedEmbedder struct {
	provider EmbeddingProvider
	cache    *EmbeddingCache
	model    string
}

// NewCachedEmbedder creates a cached embedding provider.
func NewCachedEmbedder(provider EmbeddingProvider, model string, cacheSize int, ttl time.Duration) *CachedEmbedder {
	return &CachedEmbedder{
		provider: provider,
		cache:    NewEmbeddingCache(cacheSize, ttl),
		model:    model,
	}
}

// Embed generates or retrieves a cached embedding.
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Check cache first
	if emb, ok := c.cache.Get(text, c.model); ok {
		return emb, nil
	}

	// Generate embedding
	emb, err := c.provider.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cache.Set(text, c.model, emb)
	return emb, nil
}

// EmbedBatch generates or retrieves cached embeddings for multiple texts.
func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var uncachedIndices []int
	var uncachedTexts []string

	// Check cache for each text
	for i, text := range texts {
		if emb, ok := c.cache.Get(text, c.model); ok {
			results[i] = emb
		} else {
			uncachedIndices = append(uncachedIndices, i)
			uncachedTexts = append(uncachedTexts, text)
		}
	}

	// Generate embeddings for uncached texts
	if len(uncachedTexts) > 0 {
		embeddings, err := c.provider.EmbedBatch(ctx, uncachedTexts)
		if err != nil {
			return nil, err
		}

		// Cache and assign results
		for i, emb := range embeddings {
			idx := uncachedIndices[i]
			results[idx] = emb
			c.cache.Set(uncachedTexts[i], c.model, emb)
		}
	}

	return results, nil
}

// Dimensions returns the embedding dimension from the underlying provider.
func (c *CachedEmbedder) Dimensions() int {
	return c.provider.Dimensions()
}

// SearchResultCache provides caching for search results.
type SearchResultCache struct {
	mu      sync.RWMutex
	cache   map[string]*searchCacheEntry
	maxSize int
	ttl     time.Duration
}

type searchCacheEntry struct {
	results   []SearchResult
	expiresAt time.Time
}

// NewSearchResultCache creates a new search result cache.
func NewSearchResultCache(maxSize int, ttl time.Duration) *SearchResultCache {
	if maxSize <= 0 {
		maxSize = 500
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &SearchResultCache{
		cache:   make(map[string]*searchCacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves cached search results.
func (c *SearchResultCache) Get(query SearchQuery) ([]SearchResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.queryKey(query)
	entry, exists := c.cache[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.results, true
}

// Set stores search results in the cache.
func (c *SearchResultCache) Set(query SearchQuery, results []SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	key := c.queryKey(query)
	c.cache[key] = &searchCacheEntry{
		results:   results,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate clears all cached results (called when new facts are added).
func (c *SearchResultCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*searchCacheEntry)
}

// queryKey generates a cache key from a search query.
func (c *SearchResultCache) queryKey(query SearchQuery) string {
	h := sha256.New()
	h.Write([]byte(query.Text))
	h.Write([]byte(query.SessionID))
	h.Write([]byte(query.UserID))
	for _, scope := range query.Scopes {
		h.Write([]byte(scope))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// evictOldest removes expired or oldest entries.
func (c *SearchResultCache) evictOldest() {
	now := time.Now()
	for key, entry := range c.cache {
		if now.After(entry.expiresAt) {
			delete(c.cache, key)
		}
	}

	if len(c.cache) >= c.maxSize {
		count := len(c.cache) - c.maxSize + 1
		for key := range c.cache {
			delete(c.cache, key)
			count--
			if count <= 0 {
				break
			}
		}
	}
}

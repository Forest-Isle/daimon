package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// KnowledgeSearchCache provides caching for knowledge search results.
type KnowledgeSearchCache struct {
	mu      sync.RWMutex
	cache   map[string]*knowledgeCacheEntry
	maxSize int
	ttl     time.Duration
}

type knowledgeCacheEntry struct {
	results   []KnowledgeResult
	expiresAt time.Time
}

// NewKnowledgeSearchCache creates a new knowledge search cache.
func NewKnowledgeSearchCache(maxSize int, ttl time.Duration) *KnowledgeSearchCache {
	if maxSize <= 0 {
		maxSize = 500
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &KnowledgeSearchCache{
		cache:   make(map[string]*knowledgeCacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves cached search results.
func (c *KnowledgeSearchCache) Get(query KnowledgeQuery) ([]KnowledgeResult, bool) {
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
func (c *KnowledgeSearchCache) Set(query KnowledgeQuery, results []KnowledgeResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	key := c.queryKey(query)
	c.cache[key] = &knowledgeCacheEntry{
		results:   results,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate clears all cached results (called when new chunks are added).
func (c *KnowledgeSearchCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*knowledgeCacheEntry)
}

// queryKey generates a cache key from a knowledge query.
func (c *KnowledgeSearchCache) queryKey(query KnowledgeQuery) string {
	h := sha256.New()
	h.Write([]byte(query.Text))
	h.Write([]byte(query.SourceType))
	return hex.EncodeToString(h.Sum(nil))
}

// evictOldest removes expired or oldest entries.
func (c *KnowledgeSearchCache) evictOldest() {
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

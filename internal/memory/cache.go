package memory

import (
	"context"
	"sync"
	"time"
)

// cacheEntry holds a cached search result with expiration.
type cacheEntry struct {
	results   []SearchResult
	expiresAt time.Time
}

// CachedStore wraps a Store with an in-memory LRU cache for Search results.
type CachedStore struct {
	inner   Store
	mu      sync.RWMutex
	cache   map[string]*cacheEntry // key = query.Text + query.Limit
	maxSize int
	ttl     time.Duration
}

// NewCachedStore creates a caching wrapper around the given Store.
// maxSize is the maximum number of cached queries. ttl is the cache entry lifetime.
func NewCachedStore(inner Store, maxSize int, ttl time.Duration) *CachedStore {
	if maxSize <= 0 {
		maxSize = 500
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &CachedStore{
		inner:   inner,
		cache:   make(map[string]*cacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func (c *CachedStore) cacheKey(q SearchQuery) string {
	return q.Text + "|" + string(rune(q.Limit))
}

func (c *CachedStore) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	key := c.cacheKey(q)

	c.mu.RLock()
	if entry, ok := c.cache[key]; ok && time.Now().Before(entry.expiresAt) {
		c.mu.RUnlock()
		return entry.results, nil
	}
	c.mu.RUnlock()

	results, err := c.inner.Search(ctx, q)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	// Evict oldest if at capacity
	if len(c.cache) >= c.maxSize {
		for k := range c.cache {
			delete(c.cache, k)
			break // evict one random entry
		}
	}
	c.cache[key] = &cacheEntry{
		results:   results,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return results, nil
}

// passThrough methods to inner Store

func (c *CachedStore) Save(ctx context.Context, e Entry) error {
	c.invalidate()
	return c.inner.Save(ctx, e)
}

func (c *CachedStore) ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error) {
	return c.inner.ListByScope(ctx, scope, userID)
}

func (c *CachedStore) Update(ctx context.Context, id, content string, version int) error {
	c.invalidate()
	return c.inner.Update(ctx, id, content, version)
}

func (c *CachedStore) Delete(ctx context.Context, id string) error {
	c.invalidate()
	return c.inner.Delete(ctx, id)
}

func (c *CachedStore) invalidate() {
	c.mu.Lock()
	c.cache = make(map[string]*cacheEntry)
	c.mu.Unlock()
}

var _ Store = (*CachedStore)(nil)

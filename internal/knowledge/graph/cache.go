package graph

import (
	"sync"
	"time"
)

// ConnectivityCache caches frequently accessed graph query results.
type ConnectivityCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
	ttl     time.Duration
}

type cacheEntry struct {
	triples   []Triple
	createdAt time.Time
}

// NewConnectivityCache creates a cache with the given max entries and TTL.
func NewConnectivityCache(maxSize int, ttl time.Duration) *ConnectivityCache {
	return &ConnectivityCache{
		entries: make(map[string]*cacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves cached triples for the given key. Returns nil, false on miss or expiry.
func (c *ConnectivityCache) Get(key string) ([]Triple, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.createdAt) > c.ttl {
		return nil, false
	}
	return e.triples, true
}

// Put stores triples under the given key. Evicts the oldest entry if at capacity.
func (c *ConnectivityCache) Put(key string, triples []Triple) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		// Don't evict if we're updating an existing key
		if _, exists := c.entries[key]; !exists {
			c.evictOldest()
		}
	}
	c.entries[key] = &cacheEntry{triples: triples, createdAt: time.Now()}
}

// Invalidate clears all cached entries.
func (c *ConnectivityCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// Size returns the number of entries currently in the cache.
func (c *ConnectivityCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *ConnectivityCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, e := range c.entries {
		if oldestKey == "" || e.createdAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.createdAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

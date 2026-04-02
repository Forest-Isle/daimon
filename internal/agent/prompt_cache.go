package agent

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// PromptCache deduplicates system prompt construction across agents.
// When multiple agents share the same configuration, the system prompt
// is built once and reused. This reduces LLM API costs by ensuring
// byte-identical prompts hit the provider's prompt cache.
type PromptCache struct {
	mu    sync.RWMutex
	cache map[string]*CachedPrompt
}

// CachedPrompt holds a cached system prompt string.
type CachedPrompt struct {
	SystemPrompt string
	Hash         string
	CreatedAt    time.Time
	hitCount     atomic.Int64
}

// HitCount returns the number of cache hits.
func (cp *CachedPrompt) HitCount() int64 {
	return cp.hitCount.Load()
}

// NewPromptCache creates a new PromptCache.
func NewPromptCache() *PromptCache {
	return &PromptCache{
		cache: make(map[string]*CachedPrompt),
	}
}

// GetOrBuild returns a cached system prompt for the given key, or builds
// and caches a new one using the builder function. The key should uniquely
// identify the prompt configuration (e.g. agent name + model + custom prompt hash).
func (pc *PromptCache) GetOrBuild(key string, builder func() string) string {
	pc.mu.RLock()
	if cached, ok := pc.cache[key]; ok {
		cached.hitCount.Add(1)
		pc.mu.RUnlock()
		return cached.SystemPrompt
	}
	pc.mu.RUnlock()

	prompt := builder()
	hash := sha256Hex(prompt)

	pc.mu.Lock()
	// Double-check after acquiring write lock
	if cached, ok := pc.cache[key]; ok {
		cached.hitCount.Add(1)
		pc.mu.Unlock()
		return cached.SystemPrompt
	}
	pc.cache[key] = &CachedPrompt{
		SystemPrompt: prompt,
		Hash:         hash,
		CreatedAt:    time.Now(),
	}
	pc.mu.Unlock()

	return prompt
}

// Get returns a cached prompt by key, or nil if not found.
func (pc *PromptCache) Get(key string) *CachedPrompt {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.cache[key]
}

// Invalidate removes a cached prompt by key.
func (pc *PromptCache) Invalidate(key string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	delete(pc.cache, key)
}

// InvalidateAll clears the entire cache.
func (pc *PromptCache) InvalidateAll() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.cache = make(map[string]*CachedPrompt)
}

// Size returns the number of cached prompts.
func (pc *PromptCache) Size() int {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return len(pc.cache)
}

// Stats returns cache statistics.
func (pc *PromptCache) Stats() PromptCacheStats {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	var totalHits int64
	for _, cp := range pc.cache {
		totalHits += cp.HitCount()
	}
	return PromptCacheStats{
		Size:      len(pc.cache),
		TotalHits: totalHits,
	}
}

// PromptCacheStats holds cache statistics.
type PromptCacheStats struct {
	Size      int
	TotalHits int64
}

// BuildCacheKey constructs a cache key from agent spec fields.
// Two agents with the same name, model, and system prompt will share a cache entry.
func BuildCacheKey(spec *AgentSpec) string {
	return fmt.Sprintf("%s:%s:%s", spec.Name, spec.Model, sha256Hex(spec.SystemPrompt))
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8]) // first 8 bytes = 16 hex chars, enough for dedup
}

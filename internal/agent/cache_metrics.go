package agent

import (
	"sync"
	"time"
)

// CacheMetrics tracks prompt caching performance across requests.
// All methods are safe for concurrent use.
type CacheMetrics struct {
	mu sync.Mutex

	TotalRequests            int64
	CacheHits                int64 // requests with CacheReadTokens > 0
	CacheMisses              int64 // requests with CacheReadTokens == 0
	TotalInputTokens         int64
	TotalCacheReadTokens     int64
	TotalCacheCreationTokens int64
	TotalOutputTokens        int64

	// Sliding window for recent hit rate
	recentResults []cacheResult
	windowSize    int
}

type cacheResult struct {
	hit        bool
	readTokens int64
	timestamp  time.Time
}

// NewCacheMetrics creates a CacheMetrics tracker with the given sliding window size.
func NewCacheMetrics(windowSize int) *CacheMetrics {
	if windowSize <= 0 {
		windowSize = 100
	}
	return &CacheMetrics{windowSize: windowSize}
}

// Record adds a new cache observation from an LLM response.
func (m *CacheMetrics) Record(inputTokens, outputTokens, cacheRead, cacheCreation int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalRequests++
	m.TotalInputTokens += inputTokens
	m.TotalOutputTokens += outputTokens
	m.TotalCacheReadTokens += cacheRead
	m.TotalCacheCreationTokens += cacheCreation

	hit := cacheRead > 0
	if hit {
		m.CacheHits++
	} else {
		m.CacheMisses++
	}

	m.recentResults = append(m.recentResults, cacheResult{
		hit:        hit,
		readTokens: cacheRead,
		timestamp:  time.Now(),
	})
	if len(m.recentResults) > m.windowSize {
		m.recentResults = m.recentResults[1:]
	}
}

// HitRate returns the overall cache hit rate (0.0-1.0).
func (m *CacheMetrics) HitRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TotalRequests == 0 {
		return 0
	}
	return float64(m.CacheHits) / float64(m.TotalRequests)
}

// RecentHitRate returns the hit rate over the recent sliding window.
func (m *CacheMetrics) RecentHitRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.recentResults) == 0 {
		return 0
	}
	hits := 0
	for _, r := range m.recentResults {
		if r.hit {
			hits++
		}
	}
	return float64(hits) / float64(len(m.recentResults))
}

// TokenSavingsRate returns the fraction of input tokens served from cache.
func (m *CacheMetrics) TokenSavingsRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.TotalInputTokens + m.TotalCacheReadTokens
	if total == 0 {
		return 0
	}
	return float64(m.TotalCacheReadTokens) / float64(total)
}

// Snapshot returns a point-in-time copy of the current metrics.
func (m *CacheMetrics) Snapshot() CacheMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	var hitRate float64
	if m.TotalRequests > 0 {
		hitRate = float64(m.CacheHits) / float64(m.TotalRequests)
	}

	var savingsRate float64
	if total := m.TotalInputTokens + m.TotalCacheReadTokens; total > 0 {
		savingsRate = float64(m.TotalCacheReadTokens) / float64(total)
	}

	return CacheMetricsSnapshot{
		TotalRequests:            m.TotalRequests,
		CacheHits:                m.CacheHits,
		CacheMisses:              m.CacheMisses,
		HitRate:                  hitRate,
		TotalInputTokens:         m.TotalInputTokens,
		TotalCacheReadTokens:     m.TotalCacheReadTokens,
		TotalCacheCreationTokens: m.TotalCacheCreationTokens,
		TotalOutputTokens:        m.TotalOutputTokens,
		TokenSavingsRate:         savingsRate,
	}
}

// CacheMetricsSnapshot is a point-in-time copy of cache performance metrics.
type CacheMetricsSnapshot struct {
	TotalRequests            int64   `json:"total_requests"`
	CacheHits                int64   `json:"cache_hits"`
	CacheMisses              int64   `json:"cache_misses"`
	HitRate                  float64 `json:"hit_rate"`
	TotalInputTokens         int64   `json:"total_input_tokens"`
	TotalCacheReadTokens     int64   `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens"`
	TotalOutputTokens        int64   `json:"total_output_tokens"`
	TokenSavingsRate         float64 `json:"token_savings_rate"`
}

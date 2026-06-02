// Package ratelimit provides rate limiting and backpressure for agent message
// handling and HTTP endpoints.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter is the rate limiting interface used by the gateway.
// Allow returns whether a request identified by key is allowed,
// the retry-after duration if not, and any error.
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

// TokenBucket implements the token bucket algorithm.
// It is thread-safe.
type TokenBucket struct {
	rate       float64   // tokens per second
	burst      int       // max tokens
	tokens     float64   // current tokens
	lastRefill time.Time // last refill timestamp
	mu         sync.Mutex
}

// NewTokenBucket creates a new TokenBucket with the given rate (tokens/sec) and burst size.
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	return &TokenBucket{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // start with a full bucket
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed. Returns (allowed, waitTime).
// Thread-safe.
func (tb *TokenBucket) Allow() (bool, time.Duration) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens = min(float64(tb.burst), tb.tokens+elapsed*tb.rate)
	tb.lastRefill = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true, 0
	}

	// Calculate wait time until next token
	waitTime := time.Duration((1 - tb.tokens) / tb.rate * float64(time.Second))
	return false, waitTime
}

// Tokens returns the current number of tokens (for testing).
func (tb *TokenBucket) Tokens() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.tokens
}

// SetTimeForTesting sets the internal timestamp for testing purposes.
// Use with caution: this is intended for tests only.
func (tb *TokenBucket) SetTimeForTesting(t time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.lastRefill = t
}

// reset fills the bucket to burst capacity.
func (tb *TokenBucket) reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = float64(tb.burst)
	tb.lastRefill = time.Now()
}

// PerKeyLimiter maps string keys to individual token buckets.
// Idle buckets are automatically cleaned up after a configurable TTL.
type PerKeyLimiter struct {
	rate       float64
	burst      int
	cleanupTTL time.Duration
	mu         sync.Mutex
	buckets    map[string]*bucketEntry
	stopCh     chan struct{}
	stopOnce   sync.Once
}

type bucketEntry struct {
	bucket     *TokenBucket
	lastAccess time.Time
}

// NewPerKeyLimiter creates a PerKeyLimiter that maps keys to token buckets.
// cleanupTTL controls how long an idle bucket is retained before cleanup.
// A cleanup goroutine runs periodically; call Stop() to shut it down.
func NewPerKeyLimiter(rate float64, burst int, cleanupTTL time.Duration) *PerKeyLimiter {
	pkl := &PerKeyLimiter{
		rate:       rate,
		burst:      burst,
		cleanupTTL: cleanupTTL,
		buckets:    make(map[string]*bucketEntry),
		stopCh:     make(chan struct{}),
	}
	go pkl.cleanupLoop()
	return pkl
}

// Allow implements Limiter.
func (pkl *PerKeyLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	pkl.mu.Lock()
	entry, ok := pkl.buckets[key]
	if !ok {
		entry = &bucketEntry{
			bucket: NewTokenBucket(pkl.rate, pkl.burst),
		}
		pkl.buckets[key] = entry
	}
	entry.lastAccess = time.Now()
	pkl.mu.Unlock()

	allowed, waitTime := entry.bucket.Allow()
	return allowed, waitTime, nil
}

// cleanupLoop periodically removes idle buckets.
func (pkl *PerKeyLimiter) cleanupLoop() {
	ticker := time.NewTicker(pkl.cleanupTTL)
	defer ticker.Stop()

	for {
		select {
		case <-pkl.stopCh:
			return
		case <-ticker.C:
			pkl.cleanup()
		}
	}
}

func (pkl *PerKeyLimiter) cleanup() {
	pkl.mu.Lock()
	defer pkl.mu.Unlock()

	now := time.Now()
	for key, entry := range pkl.buckets {
		if now.Sub(entry.lastAccess) > pkl.cleanupTTL {
			delete(pkl.buckets, key)
		}
	}
}

// Stop shuts down the cleanup goroutine.
func (pkl *PerKeyLimiter) Stop() {
	pkl.stopOnce.Do(func() {
		close(pkl.stopCh)
	})
}

// BucketCount returns the number of active buckets (for testing).
func (pkl *PerKeyLimiter) BucketCount() int {
	pkl.mu.Lock()
	defer pkl.mu.Unlock()
	return len(pkl.buckets)
}

// NoopLimiter always allows requests. Used when rate limiting is disabled.
type NoopLimiter struct{}

// Allow always returns true.
func (NoopLimiter) Allow(_ context.Context, _ string) (bool, time.Duration, error) {
	return true, 0, nil
}

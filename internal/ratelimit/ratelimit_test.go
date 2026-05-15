package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTokenBucket_AllowsWithinRate(t *testing.T) {
	tb := NewTokenBucket(10, 20) // 10 tokens/sec, burst 20

	// Should be able to consume burst tokens immediately
	for i := 0; i < 20; i++ {
		allowed, _ := tb.Allow()
		if !allowed {
			t.Fatalf("expected request %d to be allowed, burst=%d", i+1, 20)
		}
	}

	// Next request should be denied (bucket empty)
	allowed, waitTime := tb.Allow()
	if allowed {
		t.Fatal("expected request to be denied after exhausting burst")
	}
	if waitTime <= 0 {
		t.Fatal("expected positive wait time after burst exhausted")
	}
}

func TestTokenBucket_DeniesExceedingRate(t *testing.T) {
	tb := NewTokenBucket(2, 2) // very low rate and burst

	// Consume all burst tokens
	for i := 0; i < 2; i++ {
		allowed, _ := tb.Allow()
		if !allowed {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	// Should be denied immediately
	allowed, waitTime := tb.Allow()
	if allowed {
		t.Fatal("expected request to be denied with low burst and no wait")
	}
	// With rate=2, wait should be ~0.5 seconds
	if waitTime > time.Second || waitTime < 0 {
		t.Fatalf("expected waitTime around 500ms, got %v", waitTime)
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	tb := NewTokenBucket(100, 5) // high rate, low burst

	// Consume all burst tokens
	for i := 0; i < 5; i++ {
		allowed, _ := tb.Allow()
		if !allowed {
			t.Fatalf("expected burst request %d to be allowed", i+1)
		}
	}

	// Should be denied immediately
	allowed, _ := tb.Allow()
	if allowed {
		t.Fatal("expected denial after burst exhausted")
	}

	// Wait for token refill (100 tokens/sec = 0.01 sec per token)
	time.Sleep(20 * time.Millisecond)

	// Should be allowed now (at least 2 tokens refilled)
	allowed, _ = tb.Allow()
	if !allowed {
		t.Fatal("expected request to be allowed after refill period")
	}
}

func TestTokenBucket_RefillCapAtBurst(t *testing.T) {
	tb := NewTokenBucket(100, 3)

	// Consume all
	tb.Allow()
	tb.Allow()
	tb.Allow()

	// "Rewind" time far into the past so next call refills a lot
	tb.SetTimeForTesting(time.Now().Add(-1 * time.Hour))

	// Should only refill up to burst=3, not accumulate unbounded tokens
	allowed, _ := tb.Allow()
	if !allowed {
		t.Fatal("expected first request to be allowed (burst tokens available)")
	}
	allowed, _ = tb.Allow()
	if !allowed {
		t.Fatal("expected second request to be allowed")
	}
	allowed, _ = tb.Allow()
	if !allowed {
		t.Fatal("expected third request to be allowed")
	}
	// The 4th should be denied because max is burst=3
	allowed, _ = tb.Allow()
	if allowed {
		t.Fatal("expected 4th request to be denied after consuming burst cap")
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	tb := NewTokenBucket(1000, 100) // very high rate and burst

	var wg sync.WaitGroup
	allowedCount := 0
	var mu sync.Mutex

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := tb.Allow(); ok {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowedCount != 100 {
		t.Fatalf("expected all 100 concurrent requests to be allowed, got %d", allowedCount)
	}
}

func TestPerKeyLimiter_IsolatesKeys(t *testing.T) {
	ctx := context.Background()
	pkl := NewPerKeyLimiter(10, 5, 10*time.Minute)
	defer pkl.Stop()

	// Key "A" consumes all its burst tokens
	for i := 0; i < 5; i++ {
		allowed, _, err := pkl.Allow(ctx, "A")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Fatalf("key A expected allowed %d", i+1)
		}
	}

	// Key "A" should now be denied
	allowed, _, _ := pkl.Allow(ctx, "A")
	if allowed {
		t.Fatal("key A should be denied after consuming burst")
	}

	// Key "B" should still have its full burst available (isolated)
	for i := 0; i < 5; i++ {
		allowed, _, err := pkl.Allow(ctx, "B")
		if err != nil {
			t.Fatalf("unexpected error for key B: %v", err)
		}
		if !allowed {
			t.Fatalf("key B expected allowed %d (should be isolated from A)", i+1)
		}
	}
}

func TestPerKeyLimiter_CleanupRemovesIdleKeys(t *testing.T) {
	ctx := context.Background()
	// Very short cleanup TTL for testing
	pkl := NewPerKeyLimiter(10, 5, 50*time.Millisecond)
	defer pkl.Stop()

	// Use key "A"
	allowed, _, err := pkl.Allow(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected key A allowed")
	}

	if pkl.BucketCount() != 1 {
		t.Fatalf("expected 1 bucket, got %d", pkl.BucketCount())
	}

	// Wait for cleanup
	time.Sleep(150 * time.Millisecond)

	if pkl.BucketCount() != 0 {
		t.Fatalf("expected 0 buckets after cleanup, got %d", pkl.BucketCount())
	}
}

func TestPerKeyLimiter_ActiveKeysNotCleanedUp(t *testing.T) {
	ctx := context.Background()
	pkl := NewPerKeyLimiter(10, 5, 50*time.Millisecond)
	defer pkl.Stop()

	// Keep key "A" active by using it repeatedly
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_, _, _ = pkl.Allow(ctx, "A")
			}
		}
	}()

	// Wait long enough for cleanup to fire
	time.Sleep(150 * time.Millisecond)
	close(done)

	// Key A should still exist (it was actively used)
	if pkl.BucketCount() != 1 {
		t.Fatalf("expected 1 bucket for active key A, got %d", pkl.BucketCount())
	}
}

func TestNoopLimiter_AlwaysAllows(t *testing.T) {
	ctx := context.Background()
	nl := NoopLimiter{}

	for i := 0; i < 1000; i++ {
		allowed, waitTime, err := nl.Allow(ctx, "any-key")
		if err != nil {
			t.Fatalf("noop limiter should never error, got %v", err)
		}
		if !allowed {
			t.Fatal("noop limiter should always allow")
		}
		if waitTime != 0 {
			t.Fatalf("noop limiter waitTime should be 0, got %v", waitTime)
		}
	}
}

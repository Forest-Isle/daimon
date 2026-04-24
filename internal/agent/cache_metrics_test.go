package agent

import (
	"testing"
)

func TestCacheMetrics_EmptyState(t *testing.T) {
	m := NewCacheMetrics(10)

	if m.HitRate() != 0 {
		t.Errorf("HitRate on empty: got %f, want 0", m.HitRate())
	}
	if m.RecentHitRate() != 0 {
		t.Errorf("RecentHitRate on empty: got %f, want 0", m.RecentHitRate())
	}
	if m.TokenSavingsRate() != 0 {
		t.Errorf("TokenSavingsRate on empty: got %f, want 0", m.TokenSavingsRate())
	}

	snap := m.Snapshot()
	if snap.TotalRequests != 0 || snap.CacheHits != 0 || snap.CacheMisses != 0 {
		t.Errorf("Snapshot on empty: unexpected non-zero values: %+v", snap)
	}
	if snap.HitRate != 0 || snap.TokenSavingsRate != 0 {
		t.Errorf("Snapshot rates on empty: hit=%f savings=%f", snap.HitRate, snap.TokenSavingsRate)
	}
}

func TestCacheMetrics_Record(t *testing.T) {
	m := NewCacheMetrics(10)

	// Record a cache miss
	m.Record(100, 50, 0, 0)
	// Record a cache hit
	m.Record(100, 50, 80, 0)
	// Record another hit with creation
	m.Record(100, 50, 60, 20)

	if m.TotalRequests != 3 {
		t.Errorf("TotalRequests: got %d, want 3", m.TotalRequests)
	}
	if m.CacheHits != 2 {
		t.Errorf("CacheHits: got %d, want 2", m.CacheHits)
	}
	if m.CacheMisses != 1 {
		t.Errorf("CacheMisses: got %d, want 1", m.CacheMisses)
	}
	if m.TotalInputTokens != 300 {
		t.Errorf("TotalInputTokens: got %d, want 300", m.TotalInputTokens)
	}
	if m.TotalOutputTokens != 150 {
		t.Errorf("TotalOutputTokens: got %d, want 150", m.TotalOutputTokens)
	}
	if m.TotalCacheReadTokens != 140 {
		t.Errorf("TotalCacheReadTokens: got %d, want 140", m.TotalCacheReadTokens)
	}
	if m.TotalCacheCreationTokens != 20 {
		t.Errorf("TotalCacheCreationTokens: got %d, want 20", m.TotalCacheCreationTokens)
	}
}

func TestCacheMetrics_HitRate(t *testing.T) {
	m := NewCacheMetrics(10)

	m.Record(100, 50, 0, 0)   // miss
	m.Record(100, 50, 80, 0)  // hit
	m.Record(100, 50, 60, 0)  // hit
	m.Record(100, 50, 0, 0)   // miss

	got := m.HitRate()
	want := 0.5
	if got != want {
		t.Errorf("HitRate: got %f, want %f", got, want)
	}
}

func TestCacheMetrics_RecentHitRate(t *testing.T) {
	m := NewCacheMetrics(3) // small window

	// Fill window with misses
	m.Record(100, 50, 0, 0) // miss
	m.Record(100, 50, 0, 0) // miss
	m.Record(100, 50, 0, 0) // miss

	if got := m.RecentHitRate(); got != 0 {
		t.Errorf("RecentHitRate all misses: got %f, want 0", got)
	}

	// Add hits that push misses out of window
	m.Record(100, 50, 80, 0)  // hit — window: [miss, miss, hit]
	m.Record(100, 50, 60, 0)  // hit — window: [miss, hit, hit]
	m.Record(100, 50, 40, 0)  // hit — window: [hit, hit, hit]

	if got := m.RecentHitRate(); got != 1.0 {
		t.Errorf("RecentHitRate all hits in window: got %f, want 1.0", got)
	}

	// Overall hit rate should be different (3/6 = 0.5)
	if got := m.HitRate(); got != 0.5 {
		t.Errorf("HitRate overall: got %f, want 0.5", got)
	}
}

func TestCacheMetrics_TokenSavingsRate(t *testing.T) {
	m := NewCacheMetrics(10)

	// 200 input + 100 cached = 300 total, 100/300 = 0.333...
	m.Record(100, 50, 50, 0)
	m.Record(100, 50, 50, 0)

	got := m.TokenSavingsRate()
	want := 100.0 / 300.0
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("TokenSavingsRate: got %f, want %f", got, want)
	}
}

func TestCacheMetrics_Snapshot(t *testing.T) {
	m := NewCacheMetrics(10)

	m.Record(100, 50, 0, 0)   // miss
	m.Record(100, 50, 80, 10) // hit

	snap := m.Snapshot()

	if snap.TotalRequests != 2 {
		t.Errorf("snap.TotalRequests: got %d, want 2", snap.TotalRequests)
	}
	if snap.CacheHits != 1 {
		t.Errorf("snap.CacheHits: got %d, want 1", snap.CacheHits)
	}
	if snap.CacheMisses != 1 {
		t.Errorf("snap.CacheMisses: got %d, want 1", snap.CacheMisses)
	}
	if snap.HitRate != 0.5 {
		t.Errorf("snap.HitRate: got %f, want 0.5", snap.HitRate)
	}
	if snap.TotalInputTokens != 200 {
		t.Errorf("snap.TotalInputTokens: got %d, want 200", snap.TotalInputTokens)
	}
	if snap.TotalCacheReadTokens != 80 {
		t.Errorf("snap.TotalCacheReadTokens: got %d, want 80", snap.TotalCacheReadTokens)
	}
	if snap.TotalCacheCreationTokens != 10 {
		t.Errorf("snap.TotalCacheCreationTokens: got %d, want 10", snap.TotalCacheCreationTokens)
	}

	// TokenSavingsRate = 80 / (200 + 80) = 80/280
	wantSavings := 80.0 / 280.0
	if diff := snap.TokenSavingsRate - wantSavings; diff > 0.001 || diff < -0.001 {
		t.Errorf("snap.TokenSavingsRate: got %f, want %f", snap.TokenSavingsRate, wantSavings)
	}
}

func TestCacheMetrics_DefaultWindowSize(t *testing.T) {
	m := NewCacheMetrics(0) // should default to 100
	if m.windowSize != 100 {
		t.Errorf("default windowSize: got %d, want 100", m.windowSize)
	}

	m2 := NewCacheMetrics(-5) // should also default to 100
	if m2.windowSize != 100 {
		t.Errorf("negative windowSize: got %d, want 100", m2.windowSize)
	}
}

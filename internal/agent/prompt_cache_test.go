package agent

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestPromptCache_GetOrBuild(t *testing.T) {
	pc := NewPromptCache()
	var buildCount atomic.Int32

	builder := func() string {
		buildCount.Add(1)
		return "system prompt content"
	}

	// First call should build
	result := pc.GetOrBuild("key1", builder)
	if result != "system prompt content" {
		t.Errorf("expected 'system prompt content', got %q", result)
	}
	if buildCount.Load() != 1 {
		t.Errorf("expected 1 build call, got %d", buildCount.Load())
	}

	// Second call should hit cache
	result = pc.GetOrBuild("key1", builder)
	if result != "system prompt content" {
		t.Errorf("expected 'system prompt content', got %q", result)
	}
	if buildCount.Load() != 1 {
		t.Errorf("expected still 1 build call, got %d", buildCount.Load())
	}

	// Different key should build again
	pc.GetOrBuild("key2", builder)
	if buildCount.Load() != 2 {
		t.Errorf("expected 2 build calls, got %d", buildCount.Load())
	}
}

func TestPromptCache_HitCount(t *testing.T) {
	pc := NewPromptCache()
	builder := func() string { return "prompt" }

	pc.GetOrBuild("k", builder) // build
	pc.GetOrBuild("k", builder) // hit
	pc.GetOrBuild("k", builder) // hit

	cached := pc.Get("k")
	if cached == nil {
		t.Fatal("expected cached prompt")
	}
	if cached.HitCount() != 2 {
		t.Errorf("expected 2 hits, got %d", cached.HitCount())
	}
}

func TestPromptCache_Invalidate(t *testing.T) {
	pc := NewPromptCache()
	pc.GetOrBuild("k1", func() string { return "v1" })
	pc.GetOrBuild("k2", func() string { return "v2" })

	if pc.Size() != 2 {
		t.Errorf("expected size 2, got %d", pc.Size())
	}

	pc.Invalidate("k1")
	if pc.Size() != 1 {
		t.Errorf("expected size 1, got %d", pc.Size())
	}

	if pc.Get("k1") != nil {
		t.Error("expected k1 to be invalidated")
	}
}

func TestPromptCache_InvalidateAll(t *testing.T) {
	pc := NewPromptCache()
	pc.GetOrBuild("k1", func() string { return "v1" })
	pc.GetOrBuild("k2", func() string { return "v2" })

	pc.InvalidateAll()
	if pc.Size() != 0 {
		t.Errorf("expected size 0, got %d", pc.Size())
	}
}

func TestPromptCache_Stats(t *testing.T) {
	pc := NewPromptCache()
	builder := func() string { return "prompt" }

	pc.GetOrBuild("a", builder)
	pc.GetOrBuild("b", builder)
	pc.GetOrBuild("a", builder) // hit on a

	stats := pc.Stats()
	if stats.Size != 2 {
		t.Errorf("expected size 2, got %d", stats.Size)
	}
	if stats.TotalHits != 1 {
		t.Errorf("expected 1 total hit, got %d", stats.TotalHits)
	}
}

func TestPromptCache_ConcurrentAccess(t *testing.T) {
	pc := NewPromptCache()
	var buildCount atomic.Int32

	builder := func() string {
		buildCount.Add(1)
		return "concurrent prompt"
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := pc.GetOrBuild("shared-key", builder)
			if result != "concurrent prompt" {
				t.Errorf("unexpected result: %q", result)
			}
		}()
	}
	wg.Wait()

	// Builder should have been called very few times (ideally 1-2 due to races at the read lock)
	if buildCount.Load() > 5 {
		t.Errorf("expected few build calls, got %d", buildCount.Load())
	}
}

func TestBuildCacheKey(t *testing.T) {
	spec1 := &AgentSpec{Name: "agent1", Model: "claude", SystemPrompt: "be helpful"}
	spec2 := &AgentSpec{Name: "agent1", Model: "claude", SystemPrompt: "be helpful"}
	spec3 := &AgentSpec{Name: "agent2", Model: "claude", SystemPrompt: "be helpful"}

	key1 := BuildCacheKey(spec1)
	key2 := BuildCacheKey(spec2)
	key3 := BuildCacheKey(spec3)

	if key1 != key2 {
		t.Errorf("same specs should produce same key: %q != %q", key1, key2)
	}
	if key1 == key3 {
		t.Error("different agent names should produce different keys")
	}
}

func TestSha256Hex(t *testing.T) {
	h1 := sha256Hex("hello")
	h2 := sha256Hex("hello")
	h3 := sha256Hex("world")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 16 {
		t.Errorf("expected 16 hex chars, got %d", len(h1))
	}
}

package knowledge

import (
	"testing"
	"time"
)

func TestNewKnowledgeSearchCache_Defaults(t *testing.T) {
	c := NewKnowledgeSearchCache(0, 0)
	if c.maxSize != 500 {
		t.Errorf("expected default maxSize 500, got %d", c.maxSize)
	}
	if c.ttl != 5*time.Minute {
		t.Errorf("expected default ttl 5m, got %v", c.ttl)
	}
}

func TestKnowledgeSearchCache_SetAndGet(t *testing.T) {
	c := NewKnowledgeSearchCache(100, time.Minute)
	q := KnowledgeQuery{Text: "hello world"}
	results := []KnowledgeResult{
		{Chunk: Chunk{ID: "chunk1", Content: "hello"}, Score: 1.0},
	}

	c.Set(q, results)

	got, ok := c.Get(q)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Chunk.ID != "chunk1" {
		t.Errorf("expected chunk1, got %s", got[0].Chunk.ID)
	}
}

func TestKnowledgeSearchCache_Miss(t *testing.T) {
	c := NewKnowledgeSearchCache(100, time.Minute)
	q := KnowledgeQuery{Text: "not cached"}
	_, ok := c.Get(q)
	if ok {
		t.Fatal("expected cache miss for uncached query")
	}
}

func TestKnowledgeSearchCache_ExpiredEntry(t *testing.T) {
	c := NewKnowledgeSearchCache(100, 1*time.Microsecond)
	q := KnowledgeQuery{Text: "expire me"}
	c.Set(q, []KnowledgeResult{{Chunk: Chunk{ID: "chunk1"}}})

	time.Sleep(10 * time.Millisecond)

	_, ok := c.Get(q)
	if ok {
		t.Fatal("expected cache miss for expired entry")
	}
}

func TestKnowledgeSearchCache_Invalidate(t *testing.T) {
	c := NewKnowledgeSearchCache(100, time.Minute)
	q := KnowledgeQuery{Text: "test"}
	c.Set(q, []KnowledgeResult{{Chunk: Chunk{ID: "chunk1"}}})

	c.Invalidate()

	_, ok := c.Get(q)
	if ok {
		t.Fatal("expected cache miss after Invalidate")
	}
}

func TestKnowledgeSearchCache_EvictOldest(t *testing.T) {
	c := NewKnowledgeSearchCache(3, time.Minute)
	c.Set(KnowledgeQuery{Text: "q1"}, []KnowledgeResult{{Chunk: Chunk{ID: "c1"}}})
	c.Set(KnowledgeQuery{Text: "q2"}, []KnowledgeResult{{Chunk: Chunk{ID: "c2"}}})
	c.Set(KnowledgeQuery{Text: "q3"}, []KnowledgeResult{{Chunk: Chunk{ID: "c3"}}})

	// This should trigger eviction
	c.Set(KnowledgeQuery{Text: "q4"}, []KnowledgeResult{{Chunk: Chunk{ID: "c4"}}})

	// Cache should have at most 3 entries now
	c.mu.RLock()
	size := len(c.cache)
	c.mu.RUnlock()
	if size > 3 {
		t.Errorf("expected at most 3 entries after eviction, got %d", size)
	}
}

func TestKnowledgeSearchCache_DifferentQueries(t *testing.T) {
	c := NewKnowledgeSearchCache(100, time.Minute)
	q1 := KnowledgeQuery{Text: "query one"}
	q2 := KnowledgeQuery{Text: "query two"}

	c.Set(q1, []KnowledgeResult{{Chunk: Chunk{ID: "result1"}}})
	c.Set(q2, []KnowledgeResult{{Chunk: Chunk{ID: "result2"}}})

	_, ok1 := c.Get(q1)
	if !ok1 {
		t.Error("expected hit for q1")
	}
	_, ok2 := c.Get(q2)
	if !ok2 {
		t.Error("expected hit for q2")
	}
}

func TestKnowledgeSearchCache_QueryKeyIncludesSourceType(t *testing.T) {
	c := NewKnowledgeSearchCache(100, time.Minute)
	q1 := KnowledgeQuery{Text: "search", SourceType: "markdown"}
	q2 := KnowledgeQuery{Text: "search", SourceType: "web"}

	c.Set(q1, []KnowledgeResult{{Chunk: Chunk{ID: "md"}}})
	c.Set(q2, []KnowledgeResult{{Chunk: Chunk{ID: "web"}}})

	r1, ok1 := c.Get(q1)
	if !ok1 || r1[0].Chunk.ID != "md" {
		t.Error("source type filtering failed")
	}
	r2, ok2 := c.Get(q2)
	if !ok2 || r2[0].Chunk.ID != "web" {
		t.Error("source type filtering failed")
	}
}

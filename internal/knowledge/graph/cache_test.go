package graph

import (
	"testing"
	"time"
)

func TestConnectivityCache_PutGet(t *testing.T) {
	c := NewConnectivityCache(10, 5*time.Minute)

	triples := []Triple{
		{Predicate: "knows", Weight: 1.0},
		{Predicate: "works_at", Weight: 0.8},
	}

	// Miss on empty cache
	if _, ok := c.Get("key1"); ok {
		t.Error("expected cache miss")
	}

	// Put and get
	c.Put("key1", triples)
	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 triples, got %d", len(got))
	}
	if got[0].Predicate != "knows" {
		t.Errorf("expected 'knows', got %q", got[0].Predicate)
	}
}

func TestConnectivityCache_TTLExpiry(t *testing.T) {
	c := NewConnectivityCache(10, 10*time.Millisecond)

	c.Put("key1", []Triple{{Predicate: "test"}})

	// Should be present immediately
	if _, ok := c.Get("key1"); !ok {
		t.Error("expected cache hit before expiry")
	}

	// Wait for TTL
	time.Sleep(20 * time.Millisecond)

	if _, ok := c.Get("key1"); ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestConnectivityCache_Eviction(t *testing.T) {
	c := NewConnectivityCache(2, 5*time.Minute)

	c.Put("key1", []Triple{{Predicate: "first"}})
	time.Sleep(time.Millisecond) // ensure different timestamps
	c.Put("key2", []Triple{{Predicate: "second"}})
	time.Sleep(time.Millisecond)

	// Cache is full (2 entries), adding a third should evict the oldest (key1)
	c.Put("key3", []Triple{{Predicate: "third"}})

	if _, ok := c.Get("key1"); ok {
		t.Error("key1 should have been evicted (oldest)")
	}
	if _, ok := c.Get("key2"); !ok {
		t.Error("key2 should still be present")
	}
	if _, ok := c.Get("key3"); !ok {
		t.Error("key3 should be present")
	}
}

func TestConnectivityCache_EvictionDoesNotEvictUpdate(t *testing.T) {
	c := NewConnectivityCache(2, 5*time.Minute)

	c.Put("key1", []Triple{{Predicate: "first"}})
	c.Put("key2", []Triple{{Predicate: "second"}})

	// Updating key1 should not trigger eviction
	c.Put("key1", []Triple{{Predicate: "updated"}})

	if c.Size() != 2 {
		t.Errorf("expected 2 entries, got %d", c.Size())
	}
	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("key1 should exist after update")
	}
	if got[0].Predicate != "updated" {
		t.Errorf("expected 'updated', got %q", got[0].Predicate)
	}
}

func TestConnectivityCache_Invalidate(t *testing.T) {
	c := NewConnectivityCache(10, 5*time.Minute)

	c.Put("key1", []Triple{{Predicate: "a"}})
	c.Put("key2", []Triple{{Predicate: "b"}})

	if c.Size() != 2 {
		t.Errorf("expected 2 entries, got %d", c.Size())
	}

	c.Invalidate()

	if c.Size() != 0 {
		t.Errorf("expected 0 entries after invalidate, got %d", c.Size())
	}
	if _, ok := c.Get("key1"); ok {
		t.Error("key1 should be gone after invalidate")
	}
}

func TestConnectivityCache_Size(t *testing.T) {
	c := NewConnectivityCache(10, 5*time.Minute)

	if c.Size() != 0 {
		t.Errorf("expected 0, got %d", c.Size())
	}

	c.Put("a", nil)
	c.Put("b", nil)
	if c.Size() != 2 {
		t.Errorf("expected 2, got %d", c.Size())
	}
}

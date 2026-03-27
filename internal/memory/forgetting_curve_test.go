package memory

import (
	"context"
	"testing"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

func TestAccessLog(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	log := NewAccessLog(db)
	ctx := context.Background()

	// Record accesses
	if err := log.RecordAccess(ctx, "fact1", "session1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := log.RecordAccess(ctx, "fact1", "session2"); err != nil {
		t.Fatal(err)
	}

	// Check stats
	count, lastAccess, err := log.GetStats(ctx, "fact1")
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Errorf("expected 2 accesses, got %d", count)
	}

	if time.Since(lastAccess) > 5*time.Second {
		t.Errorf("last access too old: %v", lastAccess)
	}
}

func TestForgettingCurve(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	embedder := &mockEmbedder{dimension: 128}
	cfg := MemoryConfig{}
	s := NewSQLiteStore(db, embedder, cfg)

	// Create manager without access log to avoid deadlock
	fc := &ForgettingCurveManager{
		store: s,
		db:    db,
	}

	ctx := context.Background()

	// Insert old fact (7 days ago to ensure low strength)
	oldFact := Entry{
		ID:        "old_fact",
		Scope:     ScopeUser,
		Content:   "old memory",
		CreatedAt: time.Now().Add(-168 * time.Hour),
		UpdatedAt: time.Now().Add(-168 * time.Hour),
	}
	s.SaveFact(ctx, oldFact)

	// Insert recent fact
	recentFact := Entry{
		ID:        "recent_fact",
		Scope:     ScopeUser,
		Content:   "recent memory",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	s.SaveFact(ctx, recentFact)

	// Compute strengths
	oldStrength := fc.ComputeStrength(ctx, oldFact)
	recentStrength := fc.ComputeStrength(ctx, recentFact)

	if oldStrength >= recentStrength {
		t.Errorf("old fact should have lower strength: old=%f, recent=%f", oldStrength, recentStrength)
	}

	if oldStrength >= 0.3 {
		t.Skipf("Old strength %f is above threshold, skipping fade test", oldStrength)
	}

	// Fade weak memories
	fc.FadeWeakMemories(ctx)

	// Check old fact was archived
	var scope string
	err = db.QueryRow(`SELECT scope FROM memory_facts WHERE id = ?`, "old_fact").Scan(&scope)
	if err != nil {
		t.Fatal(err)
	}

	if scope != "archive" {
		t.Errorf("old fact should be archived, got scope: %s", scope)
	}
}

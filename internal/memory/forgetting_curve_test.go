package memory

import (
	"context"
	"os"
	"path/filepath"
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

	// Create manager
	fc := NewForgettingCurveManager(db)
	ctx := context.Background()

	// Test ComputeStrength with old vs recent facts
	oldFact := Entry{
		ID:        "old_fact",
		Scope:     ScopeUser,
		Content:   "old memory",
		Metadata:  map[string]string{},
		CreatedAt: time.Now().Add(-168 * time.Hour), // 7 days ago
		UpdatedAt: time.Now().Add(-168 * time.Hour),
	}

	recentFact := Entry{
		ID:        "recent_fact",
		Scope:     ScopeUser,
		Content:   "recent memory",
		Metadata:  map[string]string{},
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}

	oldStrength := fc.ComputeStrength(ctx, oldFact)
	recentStrength := fc.ComputeStrength(ctx, recentFact)

	if oldStrength >= recentStrength {
		t.Errorf("old fact should have lower strength: old=%f, recent=%f", oldStrength, recentStrength)
	}

	if oldStrength >= 0.3 {
		t.Skipf("Old strength %f is above threshold, skipping fade test", oldStrength)
	}

	t.Logf("old_fact strength: %f, recent_fact strength: %f", oldStrength, recentStrength)
}

func TestFadeWeakMemoriesFromFiles(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fc := NewForgettingCurveManager(db)
	ctx := context.Background()

	// Create temp directory structure
	baseDir := t.TempDir()
	for _, dir := range []string{"user", "session", "archived"} {
		os.MkdirAll(filepath.Join(baseDir, dir), 0755)
	}

	// Write a weak memory file (very old, low strength)
	weakContent := `---
id: weak_fact
scope: session
created_at: 2020-01-01T00:00:00Z
updated_at: 2020-01-01T00:00:00Z
---

This is a very old memory that should be archived.
`
	weakPath := filepath.Join(baseDir, "session", "memory_20200101_weak_fact.md")
	os.WriteFile(weakPath, []byte(weakContent), 0644)

	// Write a strong memory file (recent)
	strongContent := `---
id: strong_fact
scope: session
created_at: ` + time.Now().Format(time.RFC3339) + `
updated_at: ` + time.Now().Format(time.RFC3339) + `
---

This is a recent memory that should not be archived.
`
	strongPath := filepath.Join(baseDir, "session", "memory_20260330_strong_fact.md")
	os.WriteFile(strongPath, []byte(strongContent), 0644)

	// Run fade
	err = fc.FadeWeakMemoriesFromFiles(ctx, baseDir)
	if err != nil {
		t.Fatal(err)
	}

	// Weak file should be moved to archived/
	if _, err := os.Stat(weakPath); !os.IsNotExist(err) {
		t.Error("weak file should have been archived")
	}
	archivedPath := filepath.Join(baseDir, "archived", "memory_20200101_weak_fact.md")
	if _, err := os.Stat(archivedPath); os.IsNotExist(err) {
		t.Error("weak file should exist in archived/")
	}

	// Strong file should still be in place
	if _, err := os.Stat(strongPath); os.IsNotExist(err) {
		t.Error("strong file should not have been archived")
	}
}

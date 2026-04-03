package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func TestConsolidator_FileMove(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	memDir := filepath.Join(tmpDir, "memory")
	fileStore, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Create old session file
	entry := Entry{
		ID:        "sess_001",
		Scope:     ScopeSession,
		UserID:    "user123",
		SessionID: "sess123",
		Content:   "Session memory",
		CreatedAt: time.Now().Add(-25 * time.Hour), // Old enough
		UpdatedAt: time.Now().Add(-25 * time.Hour),
	}

	if err := fileStore.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}

	// Backdate the file's modtime so the consolidator treats it as old
	sessionFiles, _ := filepath.Glob(filepath.Join(memDir, "session", "*.md"))
	if len(sessionFiles) == 0 {
		t.Fatal("Expected session file to exist")
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(sessionFiles[0], oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Update the file's frontmatter with high strength
	mf, err := fileStore.parseFile(sessionFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	mf.Strength = 0.8
	if err := fileStore.writeFileAtomic(sessionFiles[0], *mf); err != nil {
		t.Fatal(err)
	}
	// Re-backdate after rewrite
	if err := os.Chtimes(sessionFiles[0], oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Run consolidation
	consolidator := NewConsolidator(fileStore, db.DB, memDir, 24*time.Hour)
	if err := consolidator.consolidate(ctx); err != nil {
		t.Fatalf("Consolidation failed: %v", err)
	}

	// Verify file moved to user/
	userFiles, _ := filepath.Glob(filepath.Join(memDir, "user", "*.md"))
	if len(userFiles) == 0 {
		t.Error("Expected file in user/ directory")
	}
}

func TestIndexRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	memDir := filepath.Join(tmpDir, "memory")
	fileStore, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Create files
	for i := 0; i < 5; i++ {
		entry := Entry{
			ID:        "idx_" + string(rune('0'+i)),
			Scope:     ScopeUser,
			Content:   "Index test " + string(rune('A'+i)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := fileStore.Save(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	// Clear index
	if _, err := db.Exec(`DELETE FROM memory_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM memory_fts`); err != nil {
		t.Fatal(err)
	}

	// Rebuild
	if err := fileStore.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Verify index populated
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memory_index`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("Expected 5 index entries, got %d", count)
	}
}

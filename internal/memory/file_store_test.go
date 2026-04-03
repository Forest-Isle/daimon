package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func TestFileMemoryStore_SaveAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	memDir := filepath.Join(tmpDir, "memory")
	fileStore, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Save entry
	entry := Entry{
		ID:        "test_001",
		Scope:     ScopeUser,
		UserID:    "user123",
		Content:   "Test memory content",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := fileStore.Save(ctx, entry); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	filePath := filepath.Join(memDir, "user", "memory_"+entry.CreatedAt.Format("20060102")+"_test_001.md")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File not created: %s", filePath)
	}

	// Search
	results, err := fileStore.Search(ctx, SearchQuery{
		Text:   "memory",
		Limit:  10,
		UserID: "user123",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected search results, got none")
	}
}

func TestFileMemoryStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	memDir := filepath.Join(tmpDir, "memory")
	fileStore, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	entry := Entry{
		ID:        "test_002",
		Scope:     ScopeSession,
		SessionID: "sess123",
		Content:   "Temporary memory",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := fileStore.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}

	if err := fileStore.Delete(ctx, "test_002"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify file moved to archived
	archivedPath := filepath.Join(memDir, "archived", "memory_"+entry.CreatedAt.Format("20060102")+"_test_002.md")
	if _, err := os.Stat(archivedPath); os.IsNotExist(err) {
		t.Error("File not archived")
	}
}

func TestFileMemoryStore_RebuildIndex(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	memDir := filepath.Join(tmpDir, "memory")
	fileStore, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Save entries
	for i := 0; i < 3; i++ {
		entry := Entry{
			ID:        "test_" + string(rune('0'+i)),
			Scope:     ScopeUser,
			Content:   "Memory " + string(rune('A'+i)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := fileStore.Save(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	// Rebuild index
	if err := fileStore.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Verify MEMORY.md exists
	indexPath := filepath.Join(memDir, "MEMORY.md")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("MEMORY.md not created")
	}
}

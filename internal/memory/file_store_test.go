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
	defer func() { _ = db.Close() }()

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
	defer func() { _ = db.Close() }()

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
	defer func() { _ = db.Close() }()

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

	// Rebuild index — rebuilds SQLite index tables only.
	if err := fileStore.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Verify entries are searchable after rebuild.
	results, err := fileStore.Search(ctx, SearchQuery{Text: "Memory A", Limit: 5})
	if err != nil {
		t.Fatalf("Search after rebuild failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results after rebuild")
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello world", "hello world"},
		// Apostrophe and question mark stripped; short words kept if >= 2 chars
		{"What is Kira Voss's preferred framework?", "What is Kira Voss preferred framework"},
		// FTS5 boolean operators removed (case-insensitive)
		{"foo AND bar OR NOT baz", "foo bar baz"},
		// NEAR is an FTS5 operator; slash stripped leaving NEAR removed, "3" is 1 char → dropped
		{"NEAR/3 something", "something"},
		// Two-char token kept
		{"it", "it"},
		// Punctuation replaced with spaces, tokens < 2 chars dropped
		{"hello.world!", "hello world"},
	}
	for _, tc := range cases {
		got := sanitizeFTS5Query(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

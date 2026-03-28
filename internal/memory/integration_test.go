package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

func TestConsolidator_FileMove(t *testing.T) {
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

	if err := fileStore.SaveFact(ctx, entry); err != nil {
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

func TestMigration_SQLiteToFiles(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert legacy data
	_, err = db.Exec(`
		INSERT INTO memory_facts (id, scope, user_id, content, created_at, updated_at)
		VALUES ('legacy_001', 'user', 'user123', 'Legacy memory', datetime('now'), datetime('now'))
	`)
	if err != nil {
		t.Fatal(err)
	}

	// Run migration (simplified)
	memDir := filepath.Join(tmpDir, "memory")
	os.MkdirAll(filepath.Join(memDir, "user"), 0755)

	rows, err := db.Query(`SELECT id, scope, user_id, content, created_at, updated_at FROM memory_facts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, scope, userID, content string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &scope, &userID, &content, &createdAt, &updatedAt); err != nil {
			t.Fatal(err)
		}

		mf := MemoryFile{
			ID:        id,
			Scope:     scope,
			UserID:    userID,
			Content:   content,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}

		filePath := filepath.Join(memDir, scope, "memory_"+createdAt.Format("20060102")+"_"+id+".md")
		fs := &FileMemoryStore{baseDir: memDir}
		if err := fs.writeFileAtomic(filePath, mf); err != nil {
			t.Fatal(err)
		}
		count++
	}

	if count == 0 {
		t.Error("Expected migrated files")
	}

	// Verify file exists
	files, _ := filepath.Glob(filepath.Join(memDir, "user", "*.md"))
	if len(files) == 0 {
		t.Error("No files migrated")
	}
}

func TestIndexRebuild(t *testing.T) {
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

	// Create files
	for i := 0; i < 5; i++ {
		entry := Entry{
			ID:        "idx_" + string(rune('0'+i)),
			Scope:     ScopeUser,
			Content:   "Index test " + string(rune('A'+i)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := fileStore.SaveFact(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	// Clear index
	db.Exec(`DELETE FROM memory_index`)
	db.Exec(`DELETE FROM memory_fts`)

	// Rebuild
	if err := fileStore.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Verify index populated
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM memory_index`).Scan(&count)
	if count != 5 {
		t.Errorf("Expected 5 index entries, got %d", count)
	}
}

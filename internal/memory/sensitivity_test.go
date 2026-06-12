package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/store"
)

func setupTestFileStore(t *testing.T) (*FileMemoryStore, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(tmpDir, "memory")
	fs, err := NewFileMemoryStore(memDir, db.DB, &NoopEmbedding{}, MemoryConfig{})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return fs, func() { _ = db.Close() }
}

func TestSecretMemoryExcludedFromSearch(t *testing.T) {
	fs, cleanup := setupTestFileStore(t)
	defer cleanup()
	ctx := context.Background()

	// Save a public memory
	publicEntry := Entry{
		ID:        "pub_001",
		Scope:     ScopeUser,
		UserID:    "user1",
		Content:   "My favorite programming language is Go",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"sensitivity": "public"},
	}
	if err := fs.Save(ctx, publicEntry); err != nil {
		t.Fatal(err)
	}

	// Save a secret memory
	secretEntry := Entry{
		ID:        "sec_001",
		Scope:     ScopeUser,
		UserID:    "user1",
		Content:   "My secret API key for the programming service",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"sensitivity": "secret"},
	}
	if err := fs.Save(ctx, secretEntry); err != nil {
		t.Fatal(err)
	}

	// Search for "programming" — should find public but not secret
	results, err := fs.Search(ctx, SearchQuery{
		Text:   "programming",
		Limit:  10,
		UserID: "user1",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range results {
		if r.Entry.ID == "sec_001" {
			t.Error("secret memory should be excluded from search results")
		}
	}

	// Verify the public memory is findable
	foundPublic := false
	for _, r := range results {
		if r.Entry.ID == "pub_001" {
			foundPublic = true
		}
	}
	if !foundPublic {
		t.Error("public memory should appear in search results")
	}
}

func TestPrivateMemoryFilteredByUserID(t *testing.T) {
	fs, cleanup := setupTestFileStore(t)
	defer cleanup()
	ctx := context.Background()

	// Save a private memory for user1
	privateEntry := Entry{
		ID:        "priv_001",
		Scope:     ScopeUser,
		UserID:    "user1",
		Content:   "My private notes about the project deadline",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"sensitivity": "private"},
	}
	if err := fs.Save(ctx, privateEntry); err != nil {
		t.Fatal(err)
	}

	// Search WITH UserID set — should find the private memory
	resultsWithUser, err := fs.Search(ctx, SearchQuery{
		Text:   "project deadline",
		Limit:  10,
		UserID: "user1",
	})
	if err != nil {
		t.Fatal(err)
	}

	foundWithUser := false
	for _, r := range resultsWithUser {
		if r.Entry.ID == "priv_001" {
			foundWithUser = true
		}
	}
	if !foundWithUser {
		t.Error("private memory should appear when UserID is set")
	}

	// Search WITHOUT UserID — should NOT find the private memory
	resultsWithoutUser, err := fs.Search(ctx, SearchQuery{
		Text:  "project deadline",
		Limit: 10,
		// No UserID
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range resultsWithoutUser {
		if r.Entry.ID == "priv_001" {
			t.Error("private memory should be excluded when no UserID is set")
		}
	}
}

func TestPublicMemoryVisibleToAll(t *testing.T) {
	fs, cleanup := setupTestFileStore(t)
	defer cleanup()
	ctx := context.Background()

	entry := Entry{
		ID:        "pub_002",
		Scope:     ScopeGlobal,
		Content:   "Public knowledge about system configuration",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"sensitivity": "public"},
	}
	if err := fs.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}

	// Should be visible with no UserID
	results, err := fs.Search(ctx, SearchQuery{
		Text:  "system configuration",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, r := range results {
		if r.Entry.ID == "pub_002" {
			found = true
		}
	}
	if !found {
		t.Error("public memory should be visible without UserID")
	}
}

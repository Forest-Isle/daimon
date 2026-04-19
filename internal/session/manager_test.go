package session

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestManager_Delete(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	sess, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected session to be created")
	}

	sess2, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess2.ID != sess.ID {
		t.Fatal("expected same session from cache")
	}

	if err := mgr.Delete(ctx, "subagent", "test_123"); err != nil {
		t.Fatal(err)
	}

	sess3, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess3.ID == sess.ID {
		t.Errorf("expected new session after Delete, got same ID %s", sess3.ID)
	}
}

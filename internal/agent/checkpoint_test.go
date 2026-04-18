package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteCheckpointStore_SaveAndLoad(t *testing.T) {
	db := openTestDB(t)
	cs := NewSQLiteCheckpointStore(db)
	ctx := context.Background()

	cp := &TaskCheckpoint{
		ID:               "cp-sess1-0",
		SessionID:        "sess1",
		SubTaskIndex:     2,
		ObservationsJSON: `[{"tool":"bash","ok":true}]`,
		PlanJSON:         `{"goal":"test"}`,
	}

	if err := cs.Save(ctx, cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := cs.Load(ctx, "sess1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if got.ID != cp.ID {
		t.Errorf("ID = %q, want %q", got.ID, cp.ID)
	}
	if got.SessionID != cp.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, cp.SessionID)
	}
	if got.SubTaskIndex != cp.SubTaskIndex {
		t.Errorf("SubTaskIndex = %d, want %d", got.SubTaskIndex, cp.SubTaskIndex)
	}
	if got.ObservationsJSON != cp.ObservationsJSON {
		t.Errorf("ObservationsJSON = %q, want %q", got.ObservationsJSON, cp.ObservationsJSON)
	}
	if got.PlanJSON != cp.PlanJSON {
		t.Errorf("PlanJSON = %q, want %q", got.PlanJSON, cp.PlanJSON)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be populated by SQLite default")
	}
}

func TestSQLiteCheckpointStore_Delete(t *testing.T) {
	db := openTestDB(t)
	cs := NewSQLiteCheckpointStore(db)
	ctx := context.Background()

	cp := &TaskCheckpoint{
		ID:               "cp-sess2-0",
		SessionID:        "sess2",
		SubTaskIndex:     0,
		ObservationsJSON: `[]`,
		PlanJSON:         `{}`,
	}

	if err := cs.Save(ctx, cp); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := cs.Delete(ctx, "sess2"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := cs.Load(ctx, "sess2")
	if err != nil {
		t.Fatalf("load after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestSQLiteCheckpointStore_LoadNonexistent(t *testing.T) {
	db := openTestDB(t)
	cs := NewSQLiteCheckpointStore(db)
	ctx := context.Background()

	got, err := cs.Load(ctx, "no-such-session")
	if err != nil {
		t.Fatalf("load nonexistent: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for nonexistent session, got %+v", got)
	}
}

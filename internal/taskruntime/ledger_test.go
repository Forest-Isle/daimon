package taskruntime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openTaskRuntimeTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestLedgerCreateRunComplete(t *testing.T) {
	db := openTaskRuntimeTestDB(t)
	ledger := NewLedger(db.DB)
	ctx := context.Background()

	entry, err := ledger.Create(ctx, CreateInput{
		Title:       "Ship task runtime",
		Description: "Add task ledger primitives",
		Metadata: Metadata{
			NextAction: "write tests",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if entry.ID == "" || entry.State != StatePending {
		t.Fatalf("entry = %#v", entry)
	}

	if err := ledger.MarkRunning(ctx, entry.ID, Metadata{SessionID: "sess_1"}, "started implementation"); err != nil {
		t.Fatalf("MarkRunning() error = %v", err)
	}
	if err := ledger.Complete(ctx, entry.ID, "done", "go test passed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	got, err := ledger.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != StateSucceeded {
		t.Fatalf("state = %s, want succeeded", got.State)
	}
	if got.Metadata.SessionID != "sess_1" {
		t.Fatalf("session id metadata = %q", got.Metadata.SessionID)
	}
	if len(got.Metadata.Evidence) != 2 {
		t.Fatalf("evidence = %#v", got.Metadata.Evidence)
	}
}

func TestLedgerCheckpointRoundTrip(t *testing.T) {
	db := openTaskRuntimeTestDB(t)
	ledger := NewLedger(db.DB)
	ctx := context.Background()

	if err := ledger.SaveCheckpoint(ctx, Checkpoint{
		SessionID:    "sess_1",
		SubtaskIndex: 3,
		Observations: []string{
			"read files",
			"tests passed",
		},
		PlanJSON: `{"goal":"resume"}`,
	}); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}
	got, err := ledger.GetCheckpoint(ctx, "sess_1")
	if err != nil {
		t.Fatalf("GetCheckpoint() error = %v", err)
	}
	if got.SubtaskIndex != 3 || got.PlanJSON != `{"goal":"resume"}` {
		t.Fatalf("checkpoint = %#v", got)
	}
	if len(got.Observations) != 2 || got.Observations[1] != "tests passed" {
		t.Fatalf("observations = %#v", got.Observations)
	}
}

func TestEnsureScheduledTaskUsesStableID(t *testing.T) {
	db := openTaskRuntimeTestDB(t)
	ledger := NewLedger(db.DB)
	ctx := context.Background()

	entry, err := ledger.EnsureScheduledTask(ctx, "sched_1", "Daily check", "check repo", "@daily", "telegram", "chat")
	if err != nil {
		t.Fatalf("EnsureScheduledTask() error = %v", err)
	}
	if entry.ID != ScheduledLedgerID("sched_1") {
		t.Fatalf("id = %q", entry.ID)
	}
	if entry.Metadata.ScheduledTaskID != "sched_1" || entry.Metadata.WakeupAt != "@daily" {
		t.Fatalf("metadata = %#v", entry.Metadata)
	}
}

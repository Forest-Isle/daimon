package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openWorldTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestApplyBatchAndRollback(t *testing.T) {
	db := openWorldTestDB(t)
	world := NewStore(db.DB)
	ctx := context.Background()

	err := world.Apply(ctx, "episode_1", []Mutation{
		{
			Op: "commitment.create",
			Body: mustJSON(t, Commitment{
				ID:    "commit_apply",
				Kind:  "project",
				Title: "Ship world model",
			}),
		},
		{
			Op:     "commitment.update",
			Target: "commit_apply",
			Body: mustJSON(t, map[string]any{
				"state":   "waiting",
				"due_at":  "2030-01-02T00:00:00Z",
				"horizon": "week",
			}),
		},
		{
			Op: "journal.append",
			Body: mustJSON(t, JournalEntry{
				ID:      "journal_apply",
				Kind:    "outcome",
				Summary: "World batch applied",
			}),
		},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	commitments, err := world.ListCommitments(ctx, []string{"waiting"}, "")
	if err != nil {
		t.Fatalf("ListCommitments() error = %v", err)
	}
	if len(commitments) != 1 {
		t.Fatalf("commitments len = %d, want 1", len(commitments))
	}
	got := commitments[0]
	if got.ID != "commit_apply" || got.SourceEpisode != "episode_1" || got.DueAt != "2030-01-02T00:00:00Z" {
		t.Fatalf("commitment = %#v", got)
	}

	journal, err := world.ListJournal(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(journal) != 1 || journal[0].EpisodeID != "episode_1" {
		t.Fatalf("journal = %#v", journal)
	}

	err = world.Apply(ctx, "episode_rollback", []Mutation{
		{
			Op: "commitment.create",
			Body: mustJSON(t, Commitment{
				ID:    "commit_rollback",
				Kind:  "promise",
				Title: "Should roll back",
			}),
		},
		{Op: "unknown.op", Body: mustJSON(t, map[string]any{})},
	})
	if err == nil {
		t.Fatal("Apply() bad op error = nil")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commitments WHERE id = ?`, "commit_rollback").Scan(&count); err != nil {
		t.Fatalf("count rollback commitment: %v", err)
	}
	if count != 0 {
		t.Fatalf("rollback commitment count = %d, want 0", count)
	}
}

func TestApplyOutcomeAppendsIdempotentJournal(t *testing.T) {
	db := openWorldTestDB(t)
	world := NewStore(db.DB)
	ctx := context.Background()

	if err := world.ApplyOutcome(ctx, "episode_outcome", []Mutation{
		{
			Op: "commitment.create",
			Body: mustJSON(t, Commitment{
				ID:    "commit_outcome",
				Kind:  "project",
				Title: "Persist outcome",
			}),
		},
	}, "Outcome summary", false); err != nil {
		t.Fatalf("ApplyOutcome() error = %v", err)
	}
	if err := world.ApplyOutcome(ctx, "episode_outcome", nil, "Outcome summary", false); err != nil {
		t.Fatalf("ApplyOutcome() duplicate journal error = %v", err)
	}

	commitments, err := world.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments() error = %v", err)
	}
	if len(commitments) != 1 || commitments[0].SourceEpisode != "episode_outcome" {
		t.Fatalf("commitments = %#v", commitments)
	}
	journal, err := world.ListJournal(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(journal) != 1 {
		t.Fatalf("journal len = %d, want 1: %#v", len(journal), journal)
	}
	if journal[0].ID != "journal_outcome_episode_outcome" || journal[0].Kind != "outcome" || journal[0].Summary != "Outcome summary" {
		t.Fatalf("journal = %#v", journal)
	}
}

func TestCommitmentCRUDAndStateFilter(t *testing.T) {
	db := openWorldTestDB(t)
	world := NewStore(db.DB)
	ctx := context.Background()

	if err := world.CreateCommitment(ctx, Commitment{
		Kind:  "routine",
		Title: "Daily review",
	}); err != nil {
		t.Fatalf("CreateCommitment() error = %v", err)
	}
	active, err := world.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments(active) error = %v", err)
	}
	if len(active) != 1 || !strings.HasPrefix(active[0].ID, "commitment_") {
		t.Fatalf("active commitments = %#v", active)
	}

	if err := world.UpdateCommitment(ctx, active[0].ID, map[string]any{
		"title": "Daily planning review",
		"state": "done",
	}); err != nil {
		t.Fatalf("UpdateCommitment() error = %v", err)
	}
	done, err := world.ListCommitments(ctx, []string{"done"}, "")
	if err != nil {
		t.Fatalf("ListCommitments(done) error = %v", err)
	}
	if len(done) != 1 || done[0].Title != "Daily planning review" || done[0].State != "done" {
		t.Fatalf("done commitments = %#v", done)
	}
	active, err = world.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments(active after update) error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active commitments after update = %#v", active)
	}
}

func TestJournalAppendAndListOrdering(t *testing.T) {
	db := openWorldTestDB(t)
	world := NewStore(db.DB)
	ctx := context.Background()

	entries := []JournalEntry{
		{ID: "journal_old", Kind: "fact", Summary: "old", OccurredAt: "2029-01-01T00:00:00Z"},
		{ID: "journal_mid", Kind: "decision", Summary: "mid", OccurredAt: "2030-01-02T00:00:00Z"},
		{ID: "journal_new", Kind: "outcome", Summary: "new", OccurredAt: "2030-01-03T00:00:00Z"},
	}
	for _, entry := range entries {
		if err := world.AppendJournal(ctx, entry); err != nil {
			t.Fatalf("AppendJournal(%s) error = %v", entry.ID, err)
		}
	}

	got, err := world.ListJournal(ctx, "2030-01-01T00:00:00Z", 2)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("journal len = %d, want 2", len(got))
	}
	if got[0].ID != "journal_new" || got[1].ID != "journal_mid" {
		t.Fatalf("journal order = %#v", got)
	}
}

func TestCommitmentsDigestFormattingAndCap(t *testing.T) {
	db := openWorldTestDB(t)
	world := NewStore(db.DB)
	ctx := context.Background()

	for i := 0; i < 22; i++ {
		if err := world.CreateCommitment(ctx, Commitment{
			ID:    "digest_active_" + string(rune('a'+i)),
			Kind:  "project",
			Title: "Ship item",
			State: "active",
			DueAt: "2030-01-02T00:00:00Z",
		}); err != nil {
			t.Fatalf("CreateCommitment(active %d) error = %v", i, err)
		}
	}
	if err := world.CreateCommitment(ctx, Commitment{
		ID:    "digest_waiting",
		Kind:  "promise",
		Title: "Wait item",
		State: "waiting",
		DueAt: "2030-01-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateCommitment(waiting) error = %v", err)
	}
	if err := world.CreateCommitment(ctx, Commitment{
		ID:    "digest_later",
		Kind:  "deadline",
		Title: "Later item",
		State: "active",
		DueAt: "2030-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateCommitment(later) error = %v", err)
	}

	digest, err := world.CommitmentsDigest(ctx, "2030-01-03T00:00:00Z")
	if err != nil {
		t.Fatalf("CommitmentsDigest() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(digest), "\n")
	if len(lines) != 20 {
		t.Fatalf("digest lines = %d, want 20:\n%s", len(lines), digest)
	}
	if lines[0] != "project/Ship item/active/2030-01-02T00:00:00Z" {
		t.Fatalf("digest first line = %q", lines[0])
	}
	if strings.Contains(digest, "Wait item") || strings.Contains(digest, "Later item") {
		t.Fatalf("digest included filtered commitment:\n%s", digest)
	}
}

func TestIdentityDigestMissingFile(t *testing.T) {
	identity := Identity{Dir: filepath.Join(t.TempDir(), "identity")}
	if got := identity.Digest(); got != "" {
		t.Fatalf("Digest() missing file = %q, want empty", got)
	}
	if err := identity.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	data := "name: Daimon\n"
	if err := os.WriteFile(filepath.Join(identity.Dir, "digest.md"), []byte(data), 0o644); err != nil {
		t.Fatalf("write digest: %v", err)
	}
	if got := identity.Digest(); got != data {
		t.Fatalf("Digest() = %q, want %q", got, data)
	}
}

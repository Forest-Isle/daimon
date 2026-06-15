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

	// The commitment.create mutation carries no id, so each apply would generate a
	// fresh uuid — a non-idempotent write. Applying the SAME outcome twice (a
	// re-delivery) must create it only once: the second ApplyOutcome sees the
	// outcome marker already claimed and skips its mutations entirely.
	outcomeMut := []Mutation{
		{
			Op: "commitment.create",
			Body: mustJSON(t, Commitment{
				Kind:  "project",
				Title: "Persist outcome",
			}),
		},
	}
	if err := world.ApplyOutcome(ctx, "episode_outcome", outcomeMut, "Outcome summary", false); err != nil {
		t.Fatalf("ApplyOutcome() error = %v", err)
	}
	if err := world.ApplyOutcome(ctx, "episode_outcome", outcomeMut, "Outcome summary", false); err != nil {
		t.Fatalf("ApplyOutcome() duplicate error = %v", err)
	}

	commitments, err := world.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments() error = %v", err)
	}
	if len(commitments) != 1 || commitments[0].SourceEpisode != "episode_outcome" {
		t.Fatalf("duplicate ApplyOutcome must not re-apply mutations; commitments = %#v", commitments)
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

func TestListFactsFiltersKindAndFactDeleteGuardsAuditRows(t *testing.T) {
	db := openWorldTestDB(t)
	w := NewStore(db.DB)
	ctx := context.Background()

	// Seed one fact and two append-only audit rows of other kinds.
	for _, e := range []JournalEntry{
		{ID: "f-1", Kind: "fact", Summary: "a durable fact"},
		{ID: "d-1", Kind: "decision", Summary: "a decision"},
		{ID: "o-1", Kind: "outcome", Summary: "an outcome"},
	} {
		if err := w.AppendJournal(ctx, e); err != nil {
			t.Fatalf("seed %s: %v", e.ID, err)
		}
	}

	// ListFacts returns only kind=fact rows.
	facts, err := w.ListFacts(ctx, 100)
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].ID != "f-1" {
		t.Fatalf("ListFacts = %+v, want only f-1", facts)
	}

	// fact.delete on an audit row's id is a guarded no-op: the decision row stays.
	if err := w.Apply(ctx, "sleep", []Mutation{{Op: "fact.delete", Target: "d-1"}}); err != nil {
		t.Fatalf("fact.delete (guarded): %v", err)
	}
	entries, err := w.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundDecision := false
	for _, e := range entries {
		if e.ID == "d-1" {
			foundDecision = true
		}
	}
	if !foundDecision {
		t.Fatal("fact.delete must NOT remove a non-fact (append-only audit) row")
	}

	// fact.delete on the fact removes it from retrieval.
	if err := w.Apply(ctx, "sleep", []Mutation{{Op: "fact.delete", Target: "f-1"}}); err != nil {
		t.Fatalf("fact.delete (fact): %v", err)
	}
	facts, err = w.ListFacts(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("fact f-1 should be deleted, ListFacts = %+v", facts)
	}

	// A blank target id is rejected (so it can never match an arbitrary row).
	if err := w.Apply(ctx, "sleep", []Mutation{{Op: "fact.delete", Target: ""}}); err == nil {
		t.Fatal("fact.delete with empty target must error")
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

// TestFactUpsertCannotClobberAuditRow guards invariants #1/#4: a fact.upsert with
// a caller-supplied id that collides with an append-only audit row (outcome /
// decision / correction) must NOT delete-and-replace it. The id is untrusted (a
// model can put any id in an episode_close WorldWrite), so the replacement delete
// is kind='fact'-guarded; it matches nothing here and the insert collides on the
// primary key, failing the whole Apply — the audit row survives intact.
func TestFactUpsertCannotClobberAuditRow(t *testing.T) {
	db := openWorldTestDB(t)
	w := NewStore(db.DB)
	ctx := context.Background()

	if err := w.AppendJournal(ctx, JournalEntry{ID: "o-keep", Kind: "outcome", Summary: "audit outcome"}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(JournalEntry{ID: "o-keep", Summary: "malicious fact"})
	if err := w.Apply(ctx, "episode", []Mutation{{Op: "fact.upsert", Body: body}}); err == nil {
		t.Fatal("fact.upsert onto an audit-row id must fail, not clobber it")
	}

	entries, err := w.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var got *JournalEntry
	for i := range entries {
		if entries[i].ID == "o-keep" {
			got = &entries[i]
		}
	}
	if got == nil {
		t.Fatal("audit row was destroyed by fact.upsert")
	}
	if got.Kind != "outcome" || got.Summary != "audit outcome" {
		t.Fatalf("audit row was mutated: %+v", *got)
	}
}

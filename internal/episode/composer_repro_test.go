package episode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/world"
)

func TestComposeSystemByteReproducibleForSameWorldSnapshot(t *testing.T) {
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	ctx := context.Background()

	id := &world.Identity{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(id.Dir, "digest.md"), []byte("name: Repro User\nvalues: deterministic replay\n"), 0o644); err != nil {
		t.Fatalf("write identity digest: %v", err)
	}

	for _, c := range []world.Commitment{
		{ID: "commit_alpha", Kind: "project", Title: "alpha replay audit", Body: "prove context reproduction", State: "active", DueAt: "2030-01-02T00:00:00Z"},
		{ID: "commit_beta", Kind: "watch", Title: "beta isolation watch", Body: "keep episode worlds separate", State: "active", DueAt: "2030-01-03T00:00:00Z"},
	} {
		if err := ws.CreateCommitment(ctx, c); err != nil {
			t.Fatalf("CreateCommitment(%s): %v", c.ID, err)
		}
	}

	for _, e := range []world.JournalEntry{
		{ID: "journal_alpha_decision", EpisodeID: "ep_seed_alpha", Kind: "decision", Summary: "alpha replay chose fixed world snapshots", Detail: "byte-for-byte composer reproduction", OccurredAt: "2030-01-01T10:00:00Z"},
		{ID: "journal_beta_fact", EpisodeID: "ep_seed_beta", Kind: "fact", Summary: "beta isolation requires per-episode ids", Detail: "parallel execution must not bleed goals", OccurredAt: "2030-01-01T11:00:00Z"},
		{ID: "journal_gamma_outcome", EpisodeID: "ep_seed_gamma", Kind: "outcome", Summary: "gamma replay validated deterministic ordering", Detail: "stable retrieval order", OccurredAt: "2030-01-01T12:00:00Z"},
	} {
		if err := ws.AppendJournal(ctx, e); err != nil {
			t.Fatalf("AppendJournal(%s): %v", e.ID, err)
		}
	}

	req := agent.CognitiveRequest{
		SessionID: "sess-repro",
		EpisodeID: "event-repro",
		Goal:      "alpha beta replay isolation",
		Trigger:   "fixed trigger",
		Persona:   "Use deterministic wording.",
		Rules:     "Do not invent unstable context.",
	}
	values := stubDigester{digest: "- [engineering] byte reproducibility is load-bearing (confidence 1.00)"}

	first := composeSystem(ctx, req, ws, id, values)
	second := composeSystem(ctx, req, ws, id, values)

	if first != second {
		t.Fatalf("composeSystem produced non-byte-identical output for the same world snapshot\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}
}

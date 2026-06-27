package sleep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

type stubEmbedder struct {
	errFor string
	seen   []string
}

func (s *stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	s.seen = append(s.seen, text)
	if s.errFor != "" && strings.Contains(text, s.errFor) {
		return nil, errors.New("embed failed")
	}
	return []float32{1, float32(len(text))}, nil
}

func TestEmbedJobRun(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		embedder     *stubEmbedder
		wantSummary  string
		wantMissing  []string
		wantEmbedded []string
	}{
		{
			name:         "embeds pending rows",
			embedder:     &stubEmbedder{},
			wantSummary:  "embedded 3 journal entries",
			wantEmbedded: []string{"j1", "j2", "j3"},
		},
		{
			name:         "skips failed rows and continues",
			embedder:     &stubEmbedder{errFor: "bad"},
			wantSummary:  "embedded 2 journal entries",
			wantMissing:  []string{"j2"},
			wantEmbedded: []string{"j1", "j3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := openWorldStore(t)
			entries := []world.JournalEntry{
				{ID: "j1", Kind: "fact", Summary: "good one", OccurredAt: "2030-01-01T00:00:00Z"},
				{ID: "j2", Kind: "decision", Summary: "bad row", Detail: "detail", OccurredAt: "2030-01-02T00:00:00Z"},
				{ID: "j3", Kind: "outcome", Summary: "good three", OccurredAt: "2030-01-03T00:00:00Z"},
			}
			for _, entry := range entries {
				if err := ws.AppendJournal(ctx, entry); err != nil {
					t.Fatalf("AppendJournal(%s) error = %v", entry.ID, err)
				}
			}

			summary, err := NewEmbedJob(ws, tc.embedder).Run(ctx)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if summary != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", summary, tc.wantSummary)
			}

			assertEmbeddingState(t, ws, tc.wantEmbedded, true)
			assertEmbeddingState(t, ws, tc.wantMissing, false)
		})
	}
}

func TestEmbedJobNilEmbedderNoop(t *testing.T) {
	ws := openWorldStore(t)
	ctx := context.Background()
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "fact", Summary: "pending"}); err != nil {
		t.Fatalf("AppendJournal() error = %v", err)
	}

	summary, err := NewEmbedJob(ws, nil).Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if summary != "semantic embedding disabled" {
		t.Fatalf("summary = %q", summary)
	}
	assertEmbeddingState(t, ws, []string{"j1"}, false)
}

func assertEmbeddingState(t *testing.T, ws *world.Store, ids []string, wantPresent bool) {
	t.Helper()
	pending, err := ws.ListJournalWithoutEmbedding(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListJournalWithoutEmbedding() error = %v", err)
	}
	pendingByID := make(map[string]bool, len(pending))
	for _, entry := range pending {
		pendingByID[entry.ID] = true
	}
	for _, id := range ids {
		gotPresent := !pendingByID[id]
		if gotPresent != wantPresent {
			t.Fatalf("embedding present for %s = %v, want %v; pending = %#v", id, gotPresent, wantPresent, pending)
		}
	}
}

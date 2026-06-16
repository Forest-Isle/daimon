package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

type fakeDraftSink struct {
	writes  map[string][]byte
	created map[string]bool
}

func newFakeDraftSink() *fakeDraftSink {
	return &fakeDraftSink{writes: map[string][]byte{}, created: map[string]bool{}}
}

func (f *fakeDraftSink) WriteDraft(_ context.Context, slug string, content []byte) (bool, error) {
	if _, ok := f.writes[slug]; ok {
		return false, nil
	}
	f.writes[slug] = content
	f.created[slug] = true
	return true, nil
}

func seedDistillCandidate(t *testing.T, ws *world.Store) {
	t.Helper()
	note, err := json.Marshal(world.JournalEntry{
		ID:      distillCandidateID("daily standup"),
		Kind:    "decision",
		Summary: "distill candidate: daily standup",
		Detail:  "post the standup summary | 3 episodes: ep1,ep2,ep3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Apply(context.Background(), "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
		t.Fatal(err)
	}
}

func TestPromoteStagesDraft(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedDistillCandidate(t, ws)
	sink := newFakeDraftSink()
	sum := &stubSummarizer{out: "1. Gather notes.\n2. Post summary."}

	msg, err := NewPromoteJob(ws, sum, sink).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "staged 1 distilled skill draft(s)" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.writes) != 1 {
		t.Fatalf("want 1 draft write, got %d", len(sink.writes))
	}
	slug := promoteStagingSlug("daily standup", distillCandidateID("daily standup"))
	if !strings.HasPrefix(slug, "daily-standup-") {
		t.Fatalf("slug %q lacks human-readable prefix", slug)
	}
	content := string(sink.writes[slug])
	if content == "" {
		t.Fatalf("no draft written at slug %q", slug)
	}
	for _, want := range []string{"name: daily standup", "distilled: true", "source_episodes:", "ep1", "Gather notes"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	exists, err := ws.JournalExists(ctx, "distill_draft_"+distillCandidateID("daily standup"))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("draft marker was not recorded")
	}
}

func TestPromoteIdempotent(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedDistillCandidate(t, ws)
	sink := newFakeDraftSink()
	job := NewPromoteJob(ws, &stubSummarizer{out: "1. Gather notes."}, sink)

	if _, err := job.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if msg != "no distill candidates to promote" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.writes) != 1 {
		t.Fatalf("idempotent run wrote %d drafts", len(sink.writes))
	}
}

func TestPromoteNoCandidates(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	sink := newFakeDraftSink()
	sum := &stubSummarizer{err: context.Canceled}

	msg, err := NewPromoteJob(ws, sum, sink).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no distill candidates to promote" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.writes) != 0 {
		t.Fatalf("unexpected writes: %+v", sink.writes)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called without candidates")
	}
}

func TestPromoteSkipsEmptyBody(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedDistillCandidate(t, ws)
	sink := newFakeDraftSink()
	sum := &stubSummarizer{out: "   "}

	msg, err := NewPromoteJob(ws, sum, sink).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no distill candidates to promote" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.writes) != 0 {
		t.Fatalf("empty body wrote drafts: %+v", sink.writes)
	}
	// Empty body is "not skill-worthy": a skip marker IS recorded so the candidate is
	// processed exactly once and never re-billed to the LLM on later cycles.
	exists, err := ws.JournalExists(ctx, "distill_draft_"+distillCandidateID("daily standup"))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("empty body must record a skip marker so it is not re-billed")
	}
}

func TestPromoteCapDoesNotStarveOlderCandidates(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// More candidates than one cycle's cap, so the cap must drain across cycles
	// without ever letting already-marked candidates block the rest (the bug where
	// the cap counted scans, not fresh work).
	total := promoteMaxPerCycle + 3
	names := make([]string, total)
	for i := range names {
		names[i] = fmt.Sprintf("recurring task %d", i)
		note, err := json.Marshal(world.JournalEntry{
			ID:      distillCandidateID(names[i]),
			Kind:    "decision",
			Summary: "distill candidate: " + names[i],
			Detail:  "automate it | 3 episodes: a,b,c",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Apply(ctx, "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
			t.Fatal(err)
		}
	}
	sink := newFakeDraftSink()
	job := NewPromoteJob(ws, &stubSummarizer{out: "1. Do the thing."}, sink)

	// Cycle 1 caps at promoteMaxPerCycle drafts.
	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if msg != fmt.Sprintf("staged %d distilled skill draft(s)", promoteMaxPerCycle) {
		t.Fatalf("cycle 1 msg = %q", msg)
	}
	// Cycle 2 must process the remaining 3 — NOT re-count the 10 already marked.
	msg, err = job.Run(ctx)
	if err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	if msg != "staged 3 distilled skill draft(s)" {
		t.Fatalf("cycle 2 msg = %q (older candidates starved?)", msg)
	}
	if len(sink.writes) != total {
		t.Fatalf("want %d total drafts, got %d", total, len(sink.writes))
	}
}

func TestPromoteSkipsAndMarksEmptyNameCandidate(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// A malformed candidate whose name collapses to empty: it must be marked processed
	// (so it never re-occupies a LIMIT slot) and must NOT call the LLM or write a draft.
	emptyID := "distill_candidate_empty_0000000000000099"
	note, err := json.Marshal(world.JournalEntry{
		ID: emptyID, Kind: "decision", Summary: "distill candidate:    ", Detail: "x | 3 episodes: a,b,c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Apply(ctx, "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
		t.Fatal(err)
	}
	sink := newFakeDraftSink()
	sum := &stubSummarizer{err: context.Canceled} // must NOT be called for an empty-name row

	msg, err := NewPromoteJob(ws, sum, sink).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no distill candidates to promote" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.writes) != 0 || sum.gotInput != "" {
		t.Fatalf("empty-name candidate must not draft or call LLM: writes=%d llm=%q", len(sink.writes), sum.gotInput)
	}
	exists, err := ws.JournalExists(ctx, "distill_draft_"+emptyID)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("empty-name candidate must be marked so it cannot starve the LIMIT")
	}
}

func TestPromoteRequiresDeps(t *testing.T) {
	ws := openWorldStore(t)
	sum := &stubSummarizer{}
	sink := newFakeDraftSink()
	if _, err := NewPromoteJob(nil, sum, sink).Run(context.Background()); err == nil {
		t.Fatal("nil world must error")
	}
	if _, err := NewPromoteJob(ws, nil, sink).Run(context.Background()); err == nil {
		t.Fatal("nil summarizer must error")
	}
	if _, err := NewPromoteJob(ws, sum, nil).Run(context.Background()); err == nil {
		t.Fatal("nil sink must error")
	}
}

func TestParsePromoteDetail(t *testing.T) {
	tests := []struct {
		name      string
		detail    string
		wantSkill string
		wantIDs   []string
	}{
		{name: "skill and ids", detail: "skill | 3 episodes: a,b,c", wantSkill: "skill", wantIDs: []string{"a", "b", "c"}},
		{name: "ids only", detail: "3 episodes: a,b", wantIDs: []string{"a", "b"}},
		{name: "no marker", detail: "no episodes here"},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkill, gotIDs := parsePromoteDetail(tt.detail)
			if gotSkill != tt.wantSkill || !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("parsePromoteDetail(%q) = (%q, %#v), want (%q, %#v)", tt.detail, gotSkill, gotIDs, tt.wantSkill, tt.wantIDs)
			}
		})
	}
}

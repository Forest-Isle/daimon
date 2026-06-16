package sleep

import (
	"context"
	"strings"
	"testing"
)

// stubCommitments is a fixed commitmentLister; it records the horizon it was
// queried with so tests can assert the look-ahead window.
type stubCommitments struct {
	due        []CommitmentBrief
	gotHorizon int64
}

func (s *stubCommitments) DueCommitments(_ context.Context, withinUnix int64) ([]CommitmentBrief, error) {
	s.gotHorizon = withinUnix
	return s.due, nil
}

// stubProposals is a fixed proposalWriter capturing what the job queued.
type stubProposals struct {
	pending   map[string]bool
	dismissed map[string]bool
	gotCooldn int64 // the `since` the job asked dismissed titles for
	added     []ProposedItem
}

func (s *stubProposals) PendingTitles(_ context.Context, _ int64) (map[string]bool, error) {
	if s.pending == nil {
		return map[string]bool{}, nil
	}
	return s.pending, nil
}

func (s *stubProposals) RecentlyDismissedTitles(_ context.Context, since int64) (map[string]bool, error) {
	s.gotCooldn = since
	if s.dismissed == nil {
		return map[string]bool{}, nil
	}
	return s.dismissed, nil
}

func (s *stubProposals) Add(_ context.Context, items []ProposedItem) error {
	s.added = append(s.added, items...)
	return nil
}

func fixedClock(t int64) func() int64 { return func() int64 { return t } }

func TestProposalsJobNoDueCommitments(t *testing.T) {
	c := &stubCommitments{due: nil}
	w := &stubProposals{}
	sum := &stubSummarizer{out: "should not be called"}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no upcoming commitments" {
		t.Fatalf("msg = %q, want no upcoming commitments", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called when nothing is due")
	}
	if c.gotHorizon != 1000+int64(proposalsHorizonHours)*3600 {
		t.Fatalf("horizon = %d, want now+72h", c.gotHorizon)
	}
}

func TestProposalsJobQueuesWithCapAndDedup(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{
		{ID: "commit_1", Kind: "deadline", Title: "Submit report", DueAt: 5000},
	}}
	// "Draft outline" is already pending (1 live), so the queue-depth budget is
	// proposalsDailyCap-1 = 4. The reply has a dup, a blank, and 6 fresh titles; the
	// dup+blank are dropped and only 4 of the 6 fresh ones fit the budget.
	w := &stubProposals{pending: map[string]bool{"Draft outline": true}}
	sum := &stubSummarizer{out: `Here you go:
[
  {"title":"Draft outline","body":"dup","action_plan":"x","urgency":2},
  {"title":"","body":"blank","action_plan":"x","urgency":2},
  {"title":"Gather sources","body":"b","action_plan":"collect refs","urgency":3},
  {"title":"Book review slot","body":"b","action_plan":"calendar","urgency":1},
  {"title":"Email co-author","body":"b","action_plan":"send","urgency":2},
  {"title":"Print draft","body":"b","action_plan":"print","urgency":0},
  {"title":"Reserve room","body":"b","action_plan":"reserve","urgency":1},
  {"title":"Backup files","body":"b","action_plan":"backup","urgency":0}
]`}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 4 proposal(s)" {
		t.Fatalf("msg = %q, want queued 4 proposal(s)", msg)
	}
	if len(w.added) != 4 {
		t.Fatalf("added = %d, want 4 (budget = cap - 1 pending): %#v", len(w.added), w.added)
	}
	wantHorizon := int64(1000) + int64(proposalsHorizonHours)*3600
	for _, it := range w.added {
		if it.Title == "Draft outline" || it.Title == "" {
			t.Fatalf("dup/blank leaked into queue: %#v", it)
		}
		if it.ExpiresAt != wantHorizon {
			t.Fatalf("ExpiresAt = %d, want horizon %d", it.ExpiresAt, wantHorizon)
		}
		if it.SourceCommitment != "commit_1" {
			t.Fatalf("SourceCommitment = %q, want commit_1", it.SourceCommitment)
		}
	}
	// The commitment must have reached the summarizer input.
	if !strings.Contains(sum.gotInput, "Submit report") {
		t.Fatalf("commitment not fed to summarizer:\n%s", sum.gotInput)
	}
}

func TestProposalsJobSuppressesRecentlyDismissed(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{
		{ID: "commit_1", Kind: "deadline", Title: "Submit report", DueAt: 5000},
	}}
	// "Book review slot" was dismissed inside the cooldown → must not be re-queued;
	// "Gather sources" is fresh → kept.
	w := &stubProposals{dismissed: map[string]bool{"Book review slot": true}}
	sum := &stubSummarizer{out: `[
	  {"title":"Book review slot","body":"b","action_plan":"calendar","urgency":1},
	  {"title":"Gather sources","body":"b","action_plan":"collect refs","urgency":3}
	]`}
	now := int64(1_000_000)
	job := NewProposalsJob(c, w, sum, fixedClock(now))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 1 proposal(s)" {
		t.Fatalf("msg = %q, want queued 1 proposal(s)", msg)
	}
	if len(w.added) != 1 || w.added[0].Title != "Gather sources" {
		t.Fatalf("dismissed title leaked or fresh dropped: %#v", w.added)
	}
	// The job must have asked for dismissals within the cooldown window.
	wantSince := now - int64(proposalsDismissCooldownDays)*86400
	if w.gotCooldn != wantSince {
		t.Fatalf("dismissed cooldown since = %d, want %d", w.gotCooldn, wantSince)
	}
}

func TestProposalsJobQueueFullAddsNothing(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	// Queue already at the cap → budget 0 → nothing queued, summarizer still ran.
	full := map[string]bool{}
	for i := 0; i < proposalsDailyCap; i++ {
		full[string(rune('A'+i))] = true
	}
	w := &stubProposals{pending: full}
	sum := &stubSummarizer{out: `[{"title":"New idea","body":"b","action_plan":"x","urgency":3}]`}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no new proposals (queue full)" {
		t.Fatalf("msg = %q, want queue full", msg)
	}
	if len(w.added) != 0 {
		t.Fatalf("nothing should be queued when full: %#v", w.added)
	}
}

func TestProposalsJobDedupsWithinBatch(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	w := &stubProposals{}
	// The model repeats a title within one reply; only one copy may be queued.
	sum := &stubSummarizer{out: `[
  {"title":"Same idea","body":"a","action_plan":"x","urgency":2},
  {"title":"Same idea","body":"b","action_plan":"y","urgency":1}
]`}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 1 proposal(s)" || len(w.added) != 1 {
		t.Fatalf("within-batch dup not deduped: msg=%q added=%#v", msg, w.added)
	}
}

func TestProposalsJobGarbageReplyDegrades(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	w := &stubProposals{}
	sum := &stubSummarizer{out: "I could not produce structured output, sorry."}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not error on garbage: %v", err)
	}
	if msg != "no proposals" {
		t.Fatalf("msg = %q, want no proposals", msg)
	}
	if len(w.added) != 0 {
		t.Fatalf("nothing should be queued: %#v", w.added)
	}
}

func TestProposalsJobParsesArrayAfterPreamble(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	w := &stubProposals{}
	// A bracketed phrase in the preamble (does not unmarshal) and an empty array
	// before the real one must not shadow the valid later array.
	sum := &stubSummarizer{out: "Here are ideas [see below]. First pass []. Final:\n[{\"title\":\"Real one\",\"body\":\"b\",\"action_plan\":\"go\",\"urgency\":2}]"}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 1 proposal(s)" {
		t.Fatalf("msg = %q, want queued 1 proposal(s)", msg)
	}
	if len(w.added) != 1 || w.added[0].Title != "Real one" {
		t.Fatalf("expected the real array to win: %#v", w.added)
	}
}

func TestProposalsJobNilClockErrors(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	job := NewProposalsJob(c, &stubProposals{}, &stubSummarizer{}, nil)
	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("a nil clock is a wiring error and Run must report it")
	}
}

func TestProposalsJobAllDuplicatesProducesNoNew(t *testing.T) {
	c := &stubCommitments{due: []CommitmentBrief{{ID: "commit_1", Title: "Do thing", DueAt: 5000}}}
	w := &stubProposals{pending: map[string]bool{"Only idea": true}}
	sum := &stubSummarizer{out: `[{"title":"Only idea","body":"b","action_plan":"x","urgency":1}]`}
	job := NewProposalsJob(c, w, sum, fixedClock(1000))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no new proposals" {
		t.Fatalf("msg = %q, want no new proposals", msg)
	}
	if len(w.added) != 0 {
		t.Fatalf("nothing should be queued: %#v", w.added)
	}
}

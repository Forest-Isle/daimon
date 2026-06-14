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
	pending map[string]bool
	added   []ProposedItem
}

func (s *stubProposals) PendingTitles(_ context.Context, _ int64) (map[string]bool, error) {
	if s.pending == nil {
		return map[string]bool{}, nil
	}
	return s.pending, nil
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
	// "Draft outline" is already pending — must be skipped. Seven items returned,
	// one a dup, one blank-titled: only proposalsDailyCap (5) of the rest survive.
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
	if msg != "queued 5 proposal(s)" {
		t.Fatalf("msg = %q, want queued 5 proposal(s)", msg)
	}
	if len(w.added) != 5 {
		t.Fatalf("added = %d, want 5: %#v", len(w.added), w.added)
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

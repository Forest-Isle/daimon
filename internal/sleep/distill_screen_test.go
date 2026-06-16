package sleep

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type stubDraftSource struct {
	drafts []DraftCandidate
	err    error
}

func (s *stubDraftSource) StagedDrafts(context.Context) ([]DraftCandidate, error) {
	return s.drafts, s.err
}

type stubScreenSink struct {
	pending    map[string]bool
	dismissed  map[string]bool
	gotNow     int64
	gotSince   int64
	added      []PromoteProposal
	pendingErr error
	dismissErr error
	addErr     error
}

func (s *stubScreenSink) PendingPromoteRefs(_ context.Context, now int64) (map[string]bool, error) {
	s.gotNow = now
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	if s.pending == nil {
		return map[string]bool{}, nil
	}
	return s.pending, nil
}

func (s *stubScreenSink) RecentlyDismissedPromoteRefs(_ context.Context, since int64) (map[string]bool, error) {
	s.gotSince = since
	if s.dismissErr != nil {
		return nil, s.dismissErr
	}
	if s.dismissed == nil {
		return map[string]bool{}, nil
	}
	return s.dismissed, nil
}

func (s *stubScreenSink) AddPromote(_ context.Context, items []PromoteProposal) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.added = append(s.added, items...)
	return nil
}

type queuedScreenSummarizer struct {
	outs      []string
	gotInputs []string
}

func (s *queuedScreenSummarizer) Complete(_ context.Context, _, userMessage string) (string, error) {
	s.gotInputs = append(s.gotInputs, userMessage)
	if len(s.outs) == 0 {
		return `{"promote":true,"reason":"ok"}`, nil
	}
	out := s.outs[0]
	s.outs = s.outs[1:]
	return out, nil
}

func screenDraft(slug, name string) DraftCandidate {
	return DraftCandidate{
		Slug:        slug,
		Name:        name,
		Description: "Useful repeatable work",
		Body:        "1. Do the repeatable work safely.",
		Episodes:    3,
	}
}

func TestDistillScreenJobQueuesPromoteProposal(t *testing.T) {
	sink := &stubScreenSink{}
	sum := &queuedScreenSummarizer{outs: []string{`noise {"promote":true,"reason":"captures the pattern"} done`}}
	job := NewDistillScreenJob(
		&stubDraftSource{drafts: []DraftCandidate{screenDraft("daily-summary", "Daily Summary")}},
		sink,
		sum,
		fixedClock(1_000_000),
	)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 1 skill-promotion proposal(s)" {
		t.Fatalf("msg = %q", msg)
	}
	if len(sink.added) != 1 {
		t.Fatalf("added = %#v", sink.added)
	}
	got := sink.added[0]
	if got.Slug != "daily-summary" || got.Title != "Promote distilled skill: Daily Summary" {
		t.Fatalf("unexpected promote proposal identity: %#v", got)
	}
	if !strings.Contains(got.Body, "Useful repeatable work") || !strings.Contains(got.Body, "covers 3 episodes") || !strings.Contains(got.Body, "captures the pattern") {
		t.Fatalf("unexpected body: %q", got.Body)
	}
	if !strings.Contains(sum.gotInputs[0], "Daily Summary") || !strings.Contains(sum.gotInputs[0], "1. Do the repeatable work safely.") {
		t.Fatalf("draft not rendered to judge:\n%s", sum.gotInputs[0])
	}
	wantSince := int64(1_000_000) - int64(distillScreenDismissCooldownDays)*86400
	if sink.gotNow != 1_000_000 || sink.gotSince != wantSince {
		t.Fatalf("dedup clocks wrong: now=%d since=%d", sink.gotNow, sink.gotSince)
	}
}

func TestDistillScreenJobConservativeNoQueue(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
	}{
		{name: "false", out: `{"promote":false,"reason":"too vague"}`},
		{name: "garbage", out: `I cannot decide`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &stubScreenSink{}
			job := NewDistillScreenJob(
				&stubDraftSource{drafts: []DraftCandidate{screenDraft("x", "X")}},
				sink,
				&queuedScreenSummarizer{outs: []string{tc.out}},
				fixedClock(100),
			)
			msg, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if msg != "no skill-promotion proposals" || len(sink.added) != 0 {
				t.Fatalf("expected no queue, msg=%q added=%#v", msg, sink.added)
			}
		})
	}
}

func TestDistillScreenJobSkipsPendingAndDismissed(t *testing.T) {
	sink := &stubScreenSink{
		pending:   map[string]bool{"pending": true},
		dismissed: map[string]bool{"dismissed": true},
	}
	sum := &queuedScreenSummarizer{outs: []string{`{"promote":true,"reason":"ok"}`}}
	job := NewDistillScreenJob(
		&stubDraftSource{drafts: []DraftCandidate{
			screenDraft("pending", "Pending"),
			screenDraft("dismissed", "Renamed Draft"),
			screenDraft("fresh", "Fresh"),
		}},
		sink,
		sum,
		fixedClock(100),
	)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 1 skill-promotion proposal(s)" || len(sink.added) != 1 || sink.added[0].Slug != "fresh" {
		t.Fatalf("skip logic wrong: msg=%q added=%#v", msg, sink.added)
	}
	if len(sum.gotInputs) != 1 {
		t.Fatalf("skipped drafts must not reach judge, calls=%d", len(sum.gotInputs))
	}
}

func TestDistillScreenJobCap(t *testing.T) {
	var drafts []DraftCandidate
	for i := 0; i < distillScreenCap+2; i++ {
		drafts = append(drafts, screenDraft(fmt.Sprintf("s%d", i), fmt.Sprintf("Skill %d", i)))
	}
	sink := &stubScreenSink{}
	sum := &queuedScreenSummarizer{}
	job := NewDistillScreenJob(&stubDraftSource{drafts: drafts}, sink, sum, fixedClock(100))

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "queued 3 skill-promotion proposal(s)" || len(sink.added) != distillScreenCap {
		t.Fatalf("cap not enforced: msg=%q added=%#v", msg, sink.added)
	}
	if len(sum.gotInputs) != distillScreenCap {
		t.Fatalf("judge calls = %d, want cap %d", len(sum.gotInputs), distillScreenCap)
	}
}

func TestDistillScreenJobNilDepsError(t *testing.T) {
	validDrafts := &stubDraftSource{drafts: []DraftCandidate{screenDraft("x", "X")}}
	validSink := &stubScreenSink{}
	validSum := &queuedScreenSummarizer{}
	validNow := fixedClock(100)
	for _, tc := range []struct {
		name string
		job  *DistillScreenJob
	}{
		{name: "drafts", job: NewDistillScreenJob(nil, validSink, validSum, validNow)},
		{name: "sink", job: NewDistillScreenJob(validDrafts, nil, validSum, validNow)},
		{name: "summarizer", job: NewDistillScreenJob(validDrafts, validSink, nil, validNow)},
		{name: "clock", job: NewDistillScreenJob(validDrafts, validSink, validSum, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.job.Run(context.Background()); err == nil {
				t.Fatal("Run should report nil dependency")
			}
		})
	}
}

func TestDistillScreenJobReadFailuresFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		sink *stubScreenSink
	}{
		{name: "pending", sink: &stubScreenSink{pendingErr: fmt.Errorf("db down")}},
		{name: "dismissed", sink: &stubScreenSink{dismissErr: fmt.Errorf("db down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := NewDistillScreenJob(
				&stubDraftSource{drafts: []DraftCandidate{screenDraft("x", "X")}},
				tc.sink,
				&queuedScreenSummarizer{},
				fixedClock(100),
			)
			if _, err := job.Run(context.Background()); err == nil {
				t.Fatal("read failure should fail closed")
			}
			if len(tc.sink.added) != 0 {
				t.Fatalf("must not queue on read failure: %#v", tc.sink.added)
			}
		})
	}
}

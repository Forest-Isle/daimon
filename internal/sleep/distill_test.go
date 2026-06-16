package sleep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

func seedOutcome(t *testing.T, ws *world.Store, episodeID, summary string, salvaged bool) {
	t.Helper()
	detail := ""
	if salvaged {
		detail = "salvaged=true"
	}
	if err := ws.AppendJournal(context.Background(), world.JournalEntry{
		ID: "journal_outcome_" + episodeID, EpisodeID: episodeID, Kind: "outcome",
		Summary: summary, Detail: detail,
	}); err != nil {
		t.Fatalf("seed outcome %s: %v", episodeID, err)
	}
}

func seedOutcomeDetail(t *testing.T, ws *world.Store, episodeID, summary, detail string) {
	t.Helper()
	if err := ws.AppendJournal(context.Background(), world.JournalEntry{
		ID: "journal_outcome_" + episodeID, EpisodeID: episodeID, Kind: "outcome",
		Summary: summary, Detail: detail,
	}); err != nil {
		t.Fatalf("seed outcome %s: %v", episodeID, err)
	}
}

func distillCandidates(t *testing.T, ws *world.Store) []world.JournalEntry {
	t.Helper()
	entries, err := ws.ListJournal(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("ListJournal: %v", err)
	}
	var out []world.JournalEntry
	for _, e := range entries {
		if e.Kind == "decision" && strings.HasPrefix(e.Summary, distillCandidate) {
			out = append(out, e)
		}
	}
	return out
}

func TestDistillSurfacesCandidate(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		seedOutcome(t, ws, id, "posted the daily standup summary", false)
	}
	sum := &stubSummarizer{out: `{"candidates":[{"name":"daily standup","skill":"post the standup summary automatically","episode_ids":["ep1","ep2","ep3"]}]}`}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "surfaced 1 distill candidate(s)" {
		t.Fatalf("msg = %q", msg)
	}
	cands := distillCandidates(t, ws)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate decision, got %d: %+v", len(cands), cands)
	}
	if cands[0].Summary != distillCandidate+"daily standup" {
		t.Fatalf("summary = %q", cands[0].Summary)
	}
	if !strings.Contains(cands[0].Detail, "post the standup summary automatically") ||
		!strings.Contains(cands[0].Detail, "3 episodes: ep1,ep2,ep3") {
		t.Fatalf("detail missing skill/episodes: %q", cands[0].Detail)
	}
}

// TestDistillWindowIndependentOverOutcomes is the teeth of the source swap: a
// pattern recurring across 3 clean outcomes must still be mined even when far more
// than the scan limit of newer non-outcome rows (decisions) have piled up since.
// On the old ListJournal(200) all-kinds source the 3 outcomes scroll out behind the
// 250 newer decisions, the judge sees none, and nothing is surfaced. ListOutcomes
// keeps detection window-independent over outcomes, so the candidate still surfaces.
func TestDistillWindowIndependentOverOutcomes(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)

	// 3 recurring clean outcomes, timestamped OLD.
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		if err := ws.AppendJournal(ctx, world.JournalEntry{
			ID: "journal_outcome_" + id, EpisodeID: id, Kind: "outcome",
			Summary: "posted the daily standup summary", OccurredAt: "2030-01-01T00:00:00Z",
		}); err != nil {
			t.Fatalf("seed outcome %s: %v", id, err)
		}
	}

	// 250 newer decisions bury the outcomes in any all-kinds recent-200 window.
	for i := 0; i < 250; i++ {
		if err := ws.AppendJournal(ctx, world.JournalEntry{
			ID: fmt.Sprintf("journal_decision_%03d", i), Kind: "decision",
			Summary: "some later decision", OccurredAt: fmt.Sprintf("2030-02-01T00:%02d:00Z", i%60),
		}); err != nil {
			t.Fatalf("seed decision %d: %v", i, err)
		}
	}

	sum := &stubSummarizer{out: `{"candidates":[{"name":"daily standup","skill":"post the standup summary automatically","episode_ids":["ep1","ep2","ep3"]}]}`}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "surfaced 1 distill candidate(s)" {
		t.Fatalf("outcomes buried behind 250 decisions must still be mined: msg = %q", msg)
	}
	// Assert the candidate exists window-independently (the distillCandidates helper
	// reads ListJournal(200), which the 250 future-dated decisions would itself bury).
	exists, err := ws.JournalExists(ctx, distillCandidateID("daily standup"))
	if err != nil {
		t.Fatalf("JournalExists: %v", err)
	}
	if !exists {
		t.Fatal("distill candidate was not recorded")
	}
	// The judge must actually have been handed the 3 buried outcomes.
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		if !strings.Contains(sum.gotInput, "id="+id) {
			t.Fatalf("judge input missing buried outcome %s:\n%s", id, sum.gotInput)
		}
	}
}

func TestDistillSkipsWhenTooFewOutcomes(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedOutcome(t, ws, "ep1", "did a thing", false)
	seedOutcome(t, ws, "ep2", "did another thing", false)
	sum := &stubSummarizer{err: context.Canceled} // must NOT be called

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "not enough successful episodes to distill" {
		t.Fatalf("msg = %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called below the outcome floor")
	}
}

func TestDistillExcludesSalvagedFromCount(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// 2 clean + 2 salvaged: only 2 clean < floor, so the job skips the judge.
	seedOutcome(t, ws, "ep1", "clean one", false)
	seedOutcome(t, ws, "ep2", "clean two", false)
	seedOutcome(t, ws, "ep3", "salvaged one", true)
	seedOutcome(t, ws, "ep4", "salvaged two", true)
	sum := &stubSummarizer{err: context.Canceled}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "not enough successful episodes to distill" {
		t.Fatalf("salvaged outcomes must not count: msg = %q", msg)
	}
}

func TestDistillExcludesFailedOutcomes(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// 2 clean + 2 framework-failure outcomes (non-salvaged but failed). Only 2 clean
	// remain below the floor, so the failures must not be mined as successes.
	seedOutcome(t, ws, "ep1", "clean one", false)
	seedOutcome(t, ws, "ep2", "clean two", false)
	seedOutcome(t, ws, "ep3", "episode stream error: provider down", false)
	seedOutcome(t, ws, "ep4", "did work [world write failed: bogus.op]", false)
	sum := &stubSummarizer{err: context.Canceled} // must NOT be called

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "not enough successful episodes to distill" {
		t.Fatalf("framework-failure outcomes must be excluded: msg = %q", msg)
	}
}

func TestDistillExcludesToolFailureOutcomes(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// 2 clean + 2 episodes that closed cleanly but had a failing tool call. Only the
	// 2 clean remain below the floor, so tool-failure episodes are not mined as
	// pristine patterns (the J11 conservative proxy for "all verified").
	seedOutcome(t, ws, "ep1", "clean one", false)
	seedOutcome(t, ws, "ep2", "clean two", false)
	seedOutcomeDetail(t, ws, "ep3", "had a tool error", "tool_failures=1")
	seedOutcomeDetail(t, ws, "ep4", "had two tool errors", "tool_failures=2")
	sum := &stubSummarizer{err: context.Canceled} // must NOT be called

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "not enough successful episodes to distill" {
		t.Fatalf("tool-failure outcomes must be excluded: msg = %q", msg)
	}
}

func TestDistillExcludesUnverifiedActionOutcomes(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	// 2 clean + 2 episodes that closed cleanly but took a governed action that was
	// not verified (touched a compensable/irreversible tool, or a reversible one
	// that failed). Only the 2 clean remain below the floor, so episodes with
	// unverified governed actions are not mined as pristine patterns (the J12
	// upgrade of the "all verified" proxy).
	seedOutcome(t, ws, "ep1", "clean one", false)
	seedOutcome(t, ws, "ep2", "clean two", false)
	seedOutcomeDetail(t, ws, "ep3", "took an unverified action", "unverified_actions=1")
	seedOutcomeDetail(t, ws, "ep4", "took two unverified actions", "unverified_actions=2")
	sum := &stubSummarizer{err: context.Canceled} // must NOT be called

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "not enough successful episodes to distill" {
		t.Fatalf("unverified-action outcomes must be excluded: msg = %q", msg)
	}
}

// TestDistillKeepsZeroCountDetails guards the parse-not-prefix exclusion: an
// outcome whose detail is "<key>=0" is NOT a failure and must remain minable.
func TestDistillKeepsZeroCountDetails(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	seedOutcomeDetail(t, ws, "ep1", "did the task", "unverified_actions=0")
	seedOutcomeDetail(t, ws, "ep2", "did the task", "tool_failures=0")
	seedOutcome(t, ws, "ep3", "did the task", false)
	sum := &stubSummarizer{out: `{"candidates":[{"name":"recurring task","skill":"automate it","episode_ids":["ep1","ep2","ep3"]}]}`}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "surfaced 1 distill candidate(s)" {
		t.Fatalf("=0 details must remain minable: msg = %q", msg)
	}
}

func TestDistillDropsCandidateWithTooFewRealIDs(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		seedOutcome(t, ws, id, "a task", false)
	}
	// Candidate cites 1 real id + 2 hallucinated ids → only 1 real < distillMinPattern.
	sum := &stubSummarizer{out: `{"candidates":[{"name":"ghost","skill":"x","episode_ids":["ep1","nope1","nope2"]}]}`}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no recurring patterns found" {
		t.Fatalf("msg = %q", msg)
	}
	if cands := distillCandidates(t, ws); len(cands) != 0 {
		t.Fatalf("hallucinated candidate must be dropped, got %+v", cands)
	}
}

func TestDistillDedupsExistingCandidate(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		seedOutcome(t, ws, id, "a task", false)
	}
	// A candidate with this name was already surfaced in a prior cycle, recorded
	// under its deterministic id (so dedup holds even if it has scrolled out of the
	// recent journal window).
	if err := ws.AppendJournal(ctx, world.JournalEntry{
		ID: distillCandidateID("Daily Standup"), Kind: "decision",
		Summary: distillCandidate + "Daily Standup", Detail: "old",
	}); err != nil {
		t.Fatal(err)
	}
	// Judge proposes the same pattern (case-insensitive match) → must not re-record.
	sum := &stubSummarizer{out: `{"candidates":[{"name":"daily standup","skill":"y","episode_ids":["ep1","ep2","ep3"]}]}`}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no recurring patterns found" {
		t.Fatalf("msg = %q", msg)
	}
	if cands := distillCandidates(t, ws); len(cands) != 1 {
		t.Fatalf("must not duplicate an existing candidate, got %d: %+v", len(cands), cands)
	}
}

func TestDistillUnparseableJudgeIsNoOp(t *testing.T) {
	ctx := context.Background()
	ws := openWorldStore(t)
	for _, id := range []string{"ep1", "ep2", "ep3"} {
		seedOutcome(t, ws, id, "a task", false)
	}
	sum := &stubSummarizer{out: "I could not find any patterns, sorry."}

	msg, err := NewDistillJob(ws, sum).Run(ctx)
	if err != nil {
		t.Fatalf("unparseable judge must not error: %v", err)
	}
	if msg != "no recurring patterns found" {
		t.Fatalf("msg = %q", msg)
	}
	if cands := distillCandidates(t, ws); len(cands) != 0 {
		t.Fatalf("no candidate should be written, got %+v", cands)
	}
}

func TestDistillCandidateIDHandlesNonASCIIAndNormalizes(t *testing.T) {
	// Distinct non-ASCII (e.g. Chinese) names must not collapse to the same id, or
	// the first such candidate would suppress all later ones.
	if a, b := distillCandidateID("每日站会"), distillCandidateID("周报总结"); a == b {
		t.Fatalf("distinct non-ASCII names share an id: %q == %q", a, b)
	}
	// A non-ASCII name still yields a non-degenerate id distinct from the empty slug.
	if got := distillCandidateID("每日站会"); got == "distill_candidate_" || !strings.HasPrefix(got, "distill_candidate_") {
		t.Fatalf("non-ASCII id degenerate: %q", got)
	}
	// Names equal after normalization (case/whitespace) share an id (dedup holds).
	if distillCandidateID("Daily Standup") != distillCandidateID("daily   standup") {
		t.Fatal("normalized-equal names must share an id")
	}
}

func TestDistillRequiresDeps(t *testing.T) {
	ws := openWorldStore(t)
	if _, err := NewDistillJob(nil, &stubSummarizer{}).Run(context.Background()); err == nil {
		t.Fatal("nil world must error")
	}
	if _, err := NewDistillJob(ws, nil).Run(context.Background()); err == nil {
		t.Fatal("nil summarizer must error")
	}
}

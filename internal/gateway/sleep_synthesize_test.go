package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/vcs"
)

func TestRulesFileSinkRoundTripAndDedup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attention", "rules.yaml")
	sink := rulesFileSink{path: path}
	ctx := context.Background()

	// Missing file → empty existing, no error.
	existing, err := sink.Existing(ctx)
	if err != nil || len(existing) != 0 {
		t.Fatalf("missing file should be empty: %v %+v", err, existing)
	}

	r := attention.Rule{Source: "telegram", Kind: "message", Action: "ignore"}
	if err := sink.Append(ctx, []attention.Rule{r}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Re-append the same rule: must dedup, not duplicate.
	if err := sink.Append(ctx, []attention.Rule{r}); err != nil {
		t.Fatalf("Append (dup): %v", err)
	}

	got, err := sink.Existing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate rule should not be appended twice, got %+v", got)
	}
	if got[0].Source != "telegram" || got[0].Action != "ignore" {
		t.Fatalf("roundtrip mismatch: %+v", got[0])
	}
}

func TestRulesFileSinkCommitsNewRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attention")
	path := filepath.Join(dir, "rules.yaml")
	sink := rulesFileSink{path: path}
	ctx := context.Background()

	r := attention.Rule{Source: "telegram", Kind: "message", Action: "ignore"}
	if err := sink.Append(ctx, []attention.Rule{r}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rules.yaml not written: %v", err)
	}
	commits, err := vcs.Log(ctx, dir, "rules.yaml", 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) < 1 {
		t.Fatal("expected at least one synthesize commit")
	}
	if !strings.Contains(commits[0].Subject, "synthesize") {
		t.Fatalf("expected synthesize commit, got %q", commits[0].Subject)
	}
}

func TestRulesFileSinkDoesNotCommitDuplicateRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attention")
	path := filepath.Join(dir, "rules.yaml")
	sink := rulesFileSink{path: path}
	ctx := context.Background()

	r := attention.Rule{Source: "telegram", Kind: "message", Action: "ignore"}
	if err := sink.Append(ctx, []attention.Rule{r}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	before, err := vcs.Log(ctx, dir, "rules.yaml", 10)
	if err != nil {
		t.Fatalf("Log before: %v", err)
	}
	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	if err := sink.Append(ctx, []attention.Rule{r}); err != nil {
		t.Fatalf("Append duplicate: %v", err)
	}
	after, err := vcs.Log(ctx, dir, "rules.yaml", 10)
	if err != nil {
		t.Fatalf("Log after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("duplicate append should not create a commit, before=%d after=%d", len(before), len(after))
	}
	// And the file itself must be byte-identical: a no-op append must not
	// silently re-marshal (stripping comments / reordering) without a commit.
	bytesAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(bytesAfter) != string(bytesBefore) {
		t.Fatalf("duplicate append rewrote rules.yaml content:\nbefore=%q\nafter=%q", bytesBefore, bytesAfter)
	}
}

func TestRulesFileSinkRevertRestoresPreviousRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attention")
	path := filepath.Join(dir, "rules.yaml")
	sink := rulesFileSink{path: path}
	ctx := context.Background()

	r1 := attention.Rule{Source: "telegram", Kind: "message", Action: "ignore"}
	if err := sink.Append(ctx, []attention.Rule{r1}); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	r2 := attention.Rule{Source: "email", Kind: "message", Action: "cognize"}
	if err := sink.Append(ctx, []attention.Rule{r2}); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if err := vcs.RevertFileToPrevious(ctx, dir, "rules.yaml"); err != nil {
		t.Fatalf("RevertFileToPrevious: %v", err)
	}
	got, err := sink.Existing(ctx)
	if err != nil {
		t.Fatalf("Existing: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("revert should restore one prior rule, got %+v", got)
	}
	if got[0].Source != "telegram" || got[0].Action != "ignore" {
		t.Fatalf("reverted content mismatch: %+v", got[0])
	}
}

func TestRulesFileSinkRejectsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml: list"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := rulesFileSink{path: path}
	if _, err := sink.Existing(context.Background()); err == nil {
		t.Fatal("a malformed rules file must surface an error, not silently empty")
	}
}

func TestFeedbackCorrectionSourceJoinsAndFilters(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "fb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	events := heart.NewStore(db.DB)
	if _, err := events.Persist(ctx, &heart.Event{ID: "e1", Source: "telegram", Kind: "message", OccurredAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatal(err)
	}

	fb := attention.NewFeedbackStore(db.DB)
	// A real correction (expected != given) on a known event.
	if err := fb.Record(ctx, attention.Feedback{EventID: "e1", ExpectedAction: "ignore", GivenAction: "cognize"}); err != nil {
		t.Fatal(err)
	}
	// A no-op (expected == given) must be filtered out.
	if err := fb.Record(ctx, attention.Feedback{EventID: "e1", ExpectedAction: "cognize", GivenAction: "cognize"}); err != nil {
		t.Fatal(err)
	}
	// A correction on an unknown event must be dropped (cannot key a rule).
	if err := fb.Record(ctx, attention.Feedback{EventID: "ghost", ExpectedAction: "ignore", GivenAction: "cognize"}); err != nil {
		t.Fatal(err)
	}

	src := feedbackCorrectionSource{feedback: fb, events: events}
	got, err := src.Corrections(ctx, 50)
	if err != nil {
		t.Fatalf("Corrections: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 joined correction (no-op + ghost filtered), got %+v", got)
	}
	c := got[0]
	if c.EventID != "e1" || c.Source != "telegram" || c.Kind != "message" || c.Expected != "ignore" {
		t.Fatalf("join mismatch: %+v", c)
	}
}

func TestEventsCanaryCorpusGroundTruthDerivation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "canary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	insertRouted := func(id, source, kind, verdict string) {
		t.Helper()
		_, err := db.DB.ExecContext(ctx, `
			INSERT INTO events (id, source, kind, payload, occurred_at, routed_at, verdict)
			VALUES (?, ?, ?, '', datetime('now'), datetime('now'), ?)`,
			id, source, kind, verdict)
		if err != nil {
			t.Fatalf("insert routed %s: %v", id, err)
		}
	}
	insertFeedback := func(eventID, expected, given string) {
		t.Helper()
		_, err := db.DB.ExecContext(ctx,
			`INSERT INTO attention_feedback (event_id, expected_action, given_action) VALUES (?, ?, ?)`,
			eventID, expected, given)
		if err != nil {
			t.Fatalf("insert feedback %s: %v", eventID, err)
		}
	}

	insertRouted("e1", "mail", "mail.received", "wake_user")
	insertRouted("e2", "calendar", "event.created", "cognize")
	insertRouted("e3", "news", "digest", "cognize")
	insertFeedback("e3", "ignore", "cognize")
	insertRouted("e4", "security", "alert", "wake_user")
	insertFeedback("e4", "ignore", "wake_user")
	insertRouted("e5", "timer", "internal.daily_brief", "brief")
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO events (id, source, kind, payload, occurred_at, routed_at, verdict)
		VALUES (?, ?, ?, '', datetime('now'), NULL, ?)`,
		"e6", "mail", "unrouted", "wake_user"); err != nil {
		t.Fatalf("insert unrouted e6: %v", err)
	}

	corpus, err := (eventsCanaryCorpus{db: db.DB}).CanaryEvents(ctx)
	if err != nil {
		t.Fatalf("CanaryEvents: %v", err)
	}
	if len(corpus) != 3 {
		t.Fatalf("want 3 canary events, got %+v", corpus)
	}
	counts := map[attention.Action]int{}
	for _, ev := range corpus {
		counts[ev.GroundTruth]++
		if ev.Source == "calendar" || ev.Source == "timer" || ev.Kind == "unrouted" {
			t.Fatalf("excluded event appeared in corpus: %+v", ev)
		}
	}
	if counts[attention.WakeUser] != 1 || counts[attention.Ignore] != 2 {
		t.Fatalf("ground truth distribution wrong: %+v corpus=%+v", counts, corpus)
	}
}

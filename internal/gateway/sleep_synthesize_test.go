package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/store"
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

package sleep

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/values"
	"github.com/Forest-Isle/daimon/internal/world"
)

func newValueStore(t *testing.T, entries ...values.Entry) *values.Store {
	t.Helper()
	vs := values.NewStore(t.TempDir())
	for _, e := range entries {
		if _, err := vs.Add(context.Background(), e); err != nil {
			t.Fatalf("seed value %s: %v", e.ID, err)
		}
	}
	return vs
}

func activeValueIDs(vs *values.Store) map[string]bool {
	m := map[string]bool{}
	for _, e := range vs.List() {
		if e.State == values.StateActive {
			m[e.ID] = true
		}
	}
	return m
}

func TestDriftJobMarksContradictedValue(t *testing.T) {
	ctx := context.Background()
	vs := newValueStore(t, values.Entry{
		ID: "v-test-frugal", Domain: "purchases",
		Statement: "Never spend over $100 without asking", Confidence: 0.9,
	})
	ws := openWorldStore(t)
	if err := ws.AppendJournal(ctx, world.JournalEntry{
		ID: "j1", Kind: "outcome", Summary: "autonomously bought a $500 gadget",
	}); err != nil {
		t.Fatal(err)
	}
	sum := &stubSummarizer{out: `{"drifting":[{"id":"v-test-frugal","reason":"a $500 autonomous purchase contradicts the spend cap"}]}`}
	job := NewDriftJob(vs, ws, sum)

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "marked 1 value(s) drifting" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	// The value must no longer authorize autonomous action.
	if _, ok := vs.Lookup("purchases"); ok {
		t.Fatal("drifting value should no longer be returned by Lookup")
	}
	if activeValueIDs(vs)["v-test-frugal"] {
		t.Fatal("value should be drifting, not active")
	}
	// The judge must have seen both the value and the contradicting activity.
	if !strings.Contains(sum.gotInput, "v-test-frugal") || !strings.Contains(sum.gotInput, "$500 gadget") {
		t.Fatalf("drift input missing value or activity:\n%s", sum.gotInput)
	}
	// A drift note must be journaled for the audit trail.
	entries, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDriftNote(entries) {
		t.Fatalf("expected a drift journal note, got %+v", entries)
	}
}

func TestDriftJobNoDriftLeavesValueActive(t *testing.T) {
	ctx := context.Background()
	vs := newValueStore(t, values.Entry{
		ID: "v-test-frugal", Domain: "purchases",
		Statement: "Never spend over $100 without asking", Confidence: 0.9,
	})
	ws := openWorldStore(t)
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "outcome", Summary: "bought a $5 coffee"}); err != nil {
		t.Fatal(err)
	}
	job := NewDriftJob(vs, ws, &stubSummarizer{out: `{"drifting":[]}`})

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no drift detected" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if _, ok := vs.Lookup("purchases"); !ok {
		t.Fatal("sound value should stay active")
	}
}

func TestDriftJobNoActiveValuesSkips(t *testing.T) {
	vs := newValueStore(t) // empty
	ws := openWorldStore(t)
	sum := &stubSummarizer{out: "should not be called"}
	job := NewDriftJob(vs, ws, sum)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no active values to check" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called when there are no active values")
	}
}

func TestDriftJobNoActivitySkips(t *testing.T) {
	vs := newValueStore(t, values.Entry{
		ID: "v-test-frugal", Domain: "purchases", Statement: "Spend carefully", Confidence: 0.9,
	})
	ws := openWorldStore(t) // empty journal
	sum := &stubSummarizer{out: "should not be called"}
	job := NewDriftJob(vs, ws, sum)

	msg, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no recent activity to compare" {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if sum.gotInput != "" {
		t.Fatal("summarizer must not be called when there is no activity")
	}
}

func TestDriftJobIgnoresHallucinatedID(t *testing.T) {
	ctx := context.Background()
	vs := newValueStore(t, values.Entry{
		ID: "v-test-frugal", Domain: "purchases", Statement: "Spend carefully", Confidence: 0.9,
	})
	ws := openWorldStore(t)
	if err := ws.AppendJournal(ctx, world.JournalEntry{ID: "j1", Kind: "outcome", Summary: "did something"}); err != nil {
		t.Fatal(err)
	}
	// Judge flags an id that is not a real value — must be ignored, not crash.
	job := NewDriftJob(vs, ws, &stubSummarizer{out: `{"drifting":[{"id":"v-ghost","reason":"x"}]}`})

	msg, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "no drift detected" {
		t.Fatalf("hallucinated id should not mark anything, got %q", msg)
	}
	if _, ok := vs.Lookup("purchases"); !ok {
		t.Fatal("real value must remain active")
	}
}

func TestParseDriftVerdictTolerance(t *testing.T) {
	cases := map[string]int{
		`{"drifting":[]}`: 0,
		"```json\n{\"drifting\":[{\"id\":\"a\"}]}\n```":         1,
		"Here is my verdict:\n{\"drifting\":[{\"id\":\"a\"}]}.": 1,
		"no json here": 0,
		"":             0,
		// Prose containing its own braces before the verdict must not corrupt parsing.
		"use {jq} to inspect: {\"drifting\":[{\"id\":\"a\"}]}": 1,
		// A brace inside a reason string must not throw off depth counting.
		`{"drifting":[{"id":"a","reason":"use } carefully"}]}`: 1,
	}
	for in, want := range cases {
		got, err := parseDriftVerdict(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if len(got) != want {
			t.Fatalf("parse %q: want %d flags, got %d", in, want, len(got))
		}
	}
}

func hasDriftNote(entries []world.JournalEntry) bool {
	for _, e := range entries {
		if e.Kind == "drift" {
			return true
		}
	}
	return false
}

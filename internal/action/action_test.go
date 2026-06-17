package action

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/store"
)

func openActionTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "action.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db.DB)
}

func TestClassStringAndParse(t *testing.T) {
	for _, c := range []Class{Reversible, Compensable, Irreversible} {
		got, err := ParseClass(c.String())
		if err != nil {
			t.Fatalf("ParseClass(%q) error = %v", c.String(), err)
		}
		if got != c {
			t.Fatalf("round-trip %v -> %q -> %v", c, c.String(), got)
		}
	}
	if _, err := ParseClass("bogus"); err == nil {
		t.Fatal("ParseClass(bogus) error = nil, want error")
	}
}

func TestLevelString(t *testing.T) {
	cases := map[Level]string{
		AskEvery: "ask_every", AskFirst: "ask_first",
		HoldThenAuto: "hold_then_auto", FullAuto: "full_auto",
	}
	for lvl, want := range cases {
		if lvl.String() != want {
			t.Fatalf("Level(%d).String() = %q, want %q", lvl, lvl.String(), want)
		}
	}
}

func TestTrustLevelDefaultsAskEvery(t *testing.T) {
	s := openActionTestStore(t)
	lvl, err := s.TrustLevel(context.Background(), Reversible, "unknown|ctx")
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if lvl != AskEvery {
		t.Fatalf("level = %v, want AskEvery", lvl)
	}
}

func TestListTrust(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()
	const key = "file.write|repo=daimon"
	if err := s.RecordAttempt(ctx, Reversible, key, true); err != nil {
		t.Fatalf("RecordAttempt() error = %v", err)
	}

	entries, err := s.ListTrust(ctx)
	if err != nil {
		t.Fatalf("ListTrust() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListTrust() len = %d, want 1: %#v", len(entries), entries)
	}
	got := entries[0]
	if got.ActionClass != Reversible.String() || got.ContextKey != key || got.Attempts != 1 || got.VerifiedOK != 1 || got.Corrected != 0 || Level(got.Level) != AskFirst {
		t.Fatalf("ListTrust()[0] = %#v", got)
	}
}

func TestPromotionWalksThresholds(t *testing.T) {
	s := openActionTestStore(t)
	const key = "file.write|repo=daimon"

	// 1 verified → AskFirst
	mustAttempt(t, s, Reversible, key, true)
	assertLevel(t, s, Reversible, key, AskFirst)

	// total 3 verified → HoldThenAuto
	mustAttempt(t, s, Reversible, key, true)
	mustAttempt(t, s, Reversible, key, true)
	assertLevel(t, s, Reversible, key, HoldThenAuto)

	// total 10 verified → FullAuto
	for i := 0; i < 7; i++ {
		mustAttempt(t, s, Reversible, key, true)
	}
	assertLevel(t, s, Reversible, key, FullAuto)
}

func TestIrreversibleCapsAtHoldThenAuto(t *testing.T) {
	s := openActionTestStore(t)
	const key = "payment|merchant=x"
	for i := 0; i < 25; i++ {
		mustAttempt(t, s, Irreversible, key, true)
	}
	assertLevel(t, s, Irreversible, key, HoldThenAuto)
}

func TestCorrectionDemotesAndFreezesPromotion(t *testing.T) {
	s := openActionTestStore(t)
	const key = "mail.send|to=boss"

	mustAttempt(t, s, Compensable, key, true) // → AskFirst
	mustAttempt(t, s, Compensable, key, true)
	mustAttempt(t, s, Compensable, key, true) // → HoldThenAuto
	assertLevel(t, s, Compensable, key, HoldThenAuto)

	if err := s.RecordCorrection(context.Background(), Compensable, key); err != nil {
		t.Fatalf("RecordCorrection() error = %v", err)
	}
	assertLevel(t, s, Compensable, key, AskFirst) // demoted one step

	// corrected>0 freezes promotion no matter how many verified attempts follow
	for i := 0; i < 20; i++ {
		mustAttempt(t, s, Compensable, key, true)
	}
	assertLevel(t, s, Compensable, key, AskFirst)
}

func TestUndoJournal(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()
	if err := s.RecordUndo(ctx, UndoRecord{ReceiptID: "r1", ToolName: "file_write", UndoSpec: `{"restore":"a.txt"}`}); err != nil {
		t.Fatalf("RecordUndo() error = %v", err)
	}
	if err := s.MarkUndone(ctx, "r1"); err != nil {
		t.Fatalf("MarkUndone() error = %v", err)
	}
	if err := s.MarkUndone(ctx, "missing"); err == nil {
		t.Fatal("MarkUndone(missing) error = nil, want not-found")
	}
}

func TestHoldsLifecycle(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()

	if err := s.CreateHold(ctx, Hold{ID: "h1", ToolName: "mail_send", ExecuteAt: "2030-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}
	if err := s.CreateHold(ctx, Hold{ID: "h2", ToolName: "mail_send", ExecuteAt: "2999-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	due, err := s.DueHolds(ctx, "2030-06-01T00:00:00Z")
	if err != nil {
		t.Fatalf("DueHolds() error = %v", err)
	}
	if len(due) != 1 || due[0].ID != "h1" {
		t.Fatalf("due = %#v, want only h1", due)
	}

	if err := s.MarkHoldState(ctx, "h1", "executed"); err != nil {
		t.Fatalf("MarkHoldState() error = %v", err)
	}
	if err := s.MarkHoldState(ctx, "h1", "bogus"); err == nil {
		t.Fatal("MarkHoldState(bogus) error = nil, want error")
	}

	// recalling an already-executed hold must fail
	if err := s.RecallHold(ctx, "h1"); err == nil {
		t.Fatal("RecallHold(executed) error = nil, want error")
	}
	// recalling a pending hold succeeds
	if err := s.RecallHold(ctx, "h2"); err != nil {
		t.Fatalf("RecallHold(h2) error = %v", err)
	}
	// recalling a missing hold fails
	if err := s.RecallHold(ctx, "missing"); err == nil {
		t.Fatal("RecallHold(missing) error = nil, want not-found")
	}
}

func TestClaimHoldCAS(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()
	if err := s.CreateHold(ctx, Hold{ID: "h1", ToolName: "http", ExecuteAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	claimed, err := s.ClaimHold(ctx, "h1")
	if err != nil {
		t.Fatalf("ClaimHold() error = %v", err)
	}
	if !claimed {
		t.Fatal("first ClaimHold() = false, want true")
	}
	claimed, err = s.ClaimHold(ctx, "h1")
	if err != nil {
		t.Fatalf("second ClaimHold() error = %v", err)
	}
	if claimed {
		t.Fatal("second ClaimHold() = true, want false")
	}
}

func TestRecallVsClaimRaceOutcomes(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()
	if err := s.CreateHold(ctx, Hold{ID: "claim-first", ToolName: "http", ExecuteAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold(claim-first) error = %v", err)
	}
	claimed, err := s.ClaimHold(ctx, "claim-first")
	if err != nil {
		t.Fatalf("ClaimHold(claim-first) error = %v", err)
	}
	if !claimed {
		t.Fatal("ClaimHold(claim-first) = false, want true")
	}
	if err := s.RecallHold(ctx, "claim-first"); err == nil {
		t.Fatal("RecallHold(after claim) error = nil, want failure")
	}

	if err := s.CreateHold(ctx, Hold{ID: "recall-first", ToolName: "http", ExecuteAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold(recall-first) error = %v", err)
	}
	if err := s.RecallHold(ctx, "recall-first"); err != nil {
		t.Fatalf("RecallHold(recall-first) error = %v", err)
	}
	claimed, err = s.ClaimHold(ctx, "recall-first")
	if err != nil {
		t.Fatalf("ClaimHold(recall-first) error = %v", err)
	}
	if claimed {
		t.Fatal("ClaimHold(after recall) = true, want false")
	}
}

func TestHoldDueAfterWindow(t *testing.T) {
	s := openActionTestStore(t)
	ctx := context.Background()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	window := 2 * time.Minute
	executeAt := base.Add(window).UTC().Format("2006-01-02 15:04:05")
	if err := s.CreateHold(ctx, Hold{ID: "h1", ToolName: "http", ExecuteAt: executeAt}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	before, err := s.DueHolds(ctx, base.Add(window-time.Second).UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("DueHolds(before) error = %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("DueHolds(before) = %#v, want none", before)
	}
	after, err := s.DueHolds(ctx, base.Add(window).UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("DueHolds(after) error = %v", err)
	}
	if len(after) != 1 || after[0].ID != "h1" {
		t.Fatalf("DueHolds(after) = %#v, want h1", after)
	}
}

func mustAttempt(t *testing.T, s *Store, class Class, key string, verified bool) {
	t.Helper()
	if err := s.RecordAttempt(context.Background(), class, key, verified); err != nil {
		t.Fatalf("RecordAttempt() error = %v", err)
	}
}

func assertLevel(t *testing.T, s *Store, class Class, key string, want Level) {
	t.Helper()
	got, err := s.TrustLevel(context.Background(), class, key)
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if got != want {
		t.Fatalf("level = %v, want %v", got, want)
	}
}

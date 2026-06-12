package action

import (
	"context"
	"path/filepath"
	"testing"

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

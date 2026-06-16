package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/sleep"
	"github.com/Forest-Isle/daimon/internal/store"
)

func TestProposalsStoreSinkAddPromoteSetsTTL(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "promote-proposals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ps := proposals.NewStore(db.DB)
	sink := proposalsStoreSink{store: ps, now: func() int64 { return 1000 }}
	if err := sink.AddPromote(context.Background(), []sleep.PromoteProposal{{
		Slug:  "daily-summary",
		Title: "Promote distilled skill: Daily Summary",
		Body:  "body",
	}}); err != nil {
		t.Fatalf("AddPromote: %v", err)
	}

	pending, err := ps.ListPending(context.Background(), 1000)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %#v", pending)
	}
	got := pending[0]
	wantExpires := int64(1000) + int64(promoteProposalTTLDays)*86400
	if got.ActionKind != proposals.ActionKindPromoteSkill || got.ActionRef != "daily-summary" {
		t.Fatalf("wrong action fields: %#v", got)
	}
	if got.ExpiresAt != wantExpires {
		t.Fatalf("ExpiresAt = %d, want %d", got.ExpiresAt, wantExpires)
	}
}

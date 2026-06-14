package heart

import (
	"context"
	"testing"
)

func TestKindsByID(t *testing.T) {
	s := openHeartTestStore(t)
	ctx := context.Background()

	if _, err := s.Persist(ctx, &Event{ID: "e1", Source: "telegram", Kind: "message", OccurredAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Persist(ctx, &Event{ID: "e2", Source: "mail", Kind: "mail.received", OccurredAt: "2030-01-01 00:00:01"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.KindsByID(ctx, []string{"e1", "e2", "missing"})
	if err != nil {
		t.Fatalf("KindsByID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 resolved events (missing absent), got %d: %+v", len(got), got)
	}
	if got["e1"].Source != "telegram" || got["e1"].Kind != "message" {
		t.Fatalf("e1 mismatch: %+v", got["e1"])
	}
	if got["e2"].Source != "mail" || got["e2"].Kind != "mail.received" {
		t.Fatalf("e2 mismatch: %+v", got["e2"])
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("missing id must be absent, not an error")
	}
}

func TestKindsByIDEmpty(t *testing.T) {
	s := openHeartTestStore(t)
	got, err := s.KindsByID(context.Background(), nil)
	if err != nil {
		t.Fatalf("KindsByID(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty id list should return empty map, got %+v", got)
	}
}

package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/store"
)

func newChatHeartSubsystem(t *testing.T, enabled, chatThrough bool) *HeartSubsystem {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "heart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hs := &HeartSubsystem{enabled: enabled, chatThroughHeart: chatThrough}
	hs.store = heart.NewStore(db.DB)
	// No dispatch handler: chat recording must never deliver to a handler.
	hs.heart = heart.New(hs.store, nil)
	return hs
}

func TestRecordChatEventDedups(t *testing.T) {
	hs := newChatHeartSubsystem(t, true, true)
	ctx := context.Background()
	msg := channel.InboundMessage{Channel: "telegram", ChannelID: "100", Text: "hello", MessageID: "42"}

	inserted, err := hs.RecordChatEvent(ctx, msg)
	if err != nil || !inserted {
		t.Fatalf("first record inserted=%v err=%v", inserted, err)
	}
	// Telegram redelivered the same update_id → must be skipped.
	inserted2, err := hs.RecordChatEvent(ctx, msg)
	if err != nil {
		t.Fatalf("second record err=%v", err)
	}
	if inserted2 {
		t.Fatal("redelivered chat message must report inserted=false (skip handling)")
	}

	// A distinct message id is a new event.
	msg2 := msg
	msg2.MessageID = "43"
	if inserted3, _ := hs.RecordChatEvent(ctx, msg2); !inserted3 {
		t.Fatal("distinct message id should record a new event")
	}
}

func TestRecordChatEventDisabledPassthrough(t *testing.T) {
	ctx := context.Background()
	msg := channel.InboundMessage{Channel: "tui", ChannelID: "local", Text: "hi", MessageID: "1"}

	// chat_through_heart off → always proceed (inserted=true), record nothing.
	hs := newChatHeartSubsystem(t, true, false)
	if inserted, err := hs.RecordChatEvent(ctx, msg); err != nil || !inserted {
		t.Fatalf("disabled chat-through-heart should pass through, got %v %v", inserted, err)
	}

	// nil subsystem (heart off entirely) → proceed.
	var nilHS *HeartSubsystem
	if inserted, err := nilHS.RecordChatEvent(ctx, msg); err != nil || !inserted {
		t.Fatalf("nil subsystem should pass through, got %v %v", inserted, err)
	}
}

func TestRecordChatEventEmptyIDNeverDedups(t *testing.T) {
	hs := newChatHeartSubsystem(t, true, true)
	ctx := context.Background()
	// No MessageID: keyless events must never collapse (each is a fresh record).
	msg := channel.InboundMessage{Channel: "tui", ChannelID: "local", Text: "same text"}
	for i := 0; i < 3; i++ {
		if inserted, err := hs.RecordChatEvent(ctx, msg); err != nil || !inserted {
			t.Fatalf("keyless record %d inserted=%v err=%v", i, inserted, err)
		}
	}
}

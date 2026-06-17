package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

type trustNotifyChannel struct {
	messages chan string
}

func (c *trustNotifyChannel) Name() string { return "test" }
func (c *trustNotifyChannel) Start(context.Context, channel.InboundHandler) error {
	return nil
}
func (c *trustNotifyChannel) Send(context.Context, channel.OutboundMessage) error {
	return nil
}
func (c *trustNotifyChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (c *trustNotifyChannel) Stop(context.Context) error { return nil }
func (c *trustNotifyChannel) SendNotification(_ context.Context, _ channel.MessageTarget, text string) error {
	c.messages <- text
	return nil
}

func TestGatewayTrustNotifierSendsAndAuditsPromotion(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trust.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ch := &trustNotifyChannel{messages: make(chan string, 1)}
	gw := &Gateway{
		config:   InitConfig(&config.Config{}, ""),
		channels: &ChannelSubsystem{channels: map[string]channel.Channel{"test": ch}},
		toolSub:  &ToolSubsystem{WorldStore: world.NewStore(db.DB)},
	}

	newGatewayTrustNotifier(gw).TrustPromoted(context.Background(), action.Reversible, "world_edit", action.AskEvery, action.AskFirst)

	var msg string
	select {
	case msg = <-ch.messages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for trust notification")
	}
	for _, want := range []string{"Trust raised:", "reversible", "world_edit", "ask_first", "ask_every"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("notification %q missing %q", msg, want)
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		entries, err := gw.toolSub.WorldStore.ListJournal(context.Background(), "", 10)
		if err != nil {
			t.Fatalf("ListJournal() error = %v", err)
		}
		for _, entry := range entries {
			if entry.Kind == "trust_promotion" && entry.Summary == msg {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("trust_promotion journal entry not found; entries=%#v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

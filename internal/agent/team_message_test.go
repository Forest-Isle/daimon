package agent

import (
	"testing"
	"time"
)

func TestMessageRouter_DirectMessage(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)
	team.AddMember(TeamMember{Name: "dev-1"})

	router := NewMessageRouter(team)
	leadInbox := router.Register("lead-1")
	devInbox := router.Register("dev-1")

	msg := TeamMessage{
		Type:    MsgDirect,
		From:    "dev-1",
		To:      "lead-1",
		Content: "hello lead",
	}
	if err := router.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case received := <-leadInbox:
		if received.Content != "hello lead" {
			t.Errorf("Content = %q, want %q", received.Content, "hello lead")
		}
		if received.From != "dev-1" {
			t.Errorf("From = %q, want %q", received.From, "dev-1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}

	// dev-1 should not have received it
	select {
	case <-devInbox:
		t.Fatal("dev-1 should not receive a direct message to lead-1")
	default:
	}
}

func TestMessageRouter_Broadcast(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)
	team.AddMember(TeamMember{Name: "dev-1"})
	team.AddMember(TeamMember{Name: "dev-2"})

	router := NewMessageRouter(team)
	leadInbox := router.Register("lead-1")
	dev1Inbox := router.Register("dev-1")
	dev2Inbox := router.Register("dev-2")

	msg := TeamMessage{
		Type:    MsgBroadcast,
		From:    "lead-1",
		Content: "attention all",
	}
	if err := router.Send(msg); err != nil {
		t.Fatalf("Send broadcast: %v", err)
	}

	// dev-1 and dev-2 should receive it
	for _, inbox := range []<-chan TeamMessage{dev1Inbox, dev2Inbox} {
		select {
		case received := <-inbox:
			if received.Content != "attention all" {
				t.Errorf("Content = %q, want %q", received.Content, "attention all")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timeout waiting for broadcast")
		}
	}

	// Sender should not receive their own broadcast
	select {
	case <-leadInbox:
		t.Fatal("sender should not receive own broadcast")
	default:
	}
}

func TestMessageRouter_UnknownRecipient(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)
	router := NewMessageRouter(team)

	msg := TeamMessage{
		Type:    MsgDirect,
		From:    "lead-1",
		To:      "nobody",
		Content: "hello?",
	}
	err := router.Send(msg)
	if err == nil {
		t.Fatal("expected error for unknown recipient")
	}
}

func TestMessageRouter_Unregister(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)
	team.AddMember(TeamMember{Name: "dev-1"})

	router := NewMessageRouter(team)
	devInbox := router.Register("dev-1")
	router.Register("lead-1")

	router.Unregister("dev-1")

	// Inbox channel should be closed
	_, open := <-devInbox
	if open {
		t.Error("inbox should be closed after Unregister")
	}

	// Sending to unregistered member should fail
	err := router.Send(TeamMessage{
		Type: MsgDirect,
		From: "lead-1",
		To:   "dev-1",
	})
	if err == nil {
		t.Error("expected error sending to unregistered member")
	}
}

func TestMessageRouter_TimestampAutoFill(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	router := NewMessageRouter(team)
	inbox := router.Register("lead-1")
	router.Register("dev-1")

	before := time.Now()
	msg := TeamMessage{
		Type:    MsgDirect,
		From:    "dev-1",
		To:      "lead-1",
		Content: "test",
	}
	_ = router.Send(msg)

	select {
	case received := <-inbox:
		if received.Timestamp.Before(before) {
			t.Error("auto-filled timestamp should be >= send time")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout")
	}
}

package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type routingTestChannel struct {
	approved       bool
	approvalTarget channel.MessageTarget
	approvalTool   string
	approvalInput  string
	approvalErr    error
	activityTarget channel.MessageTarget
	activityTool   string
	activityDone   bool
}

func (c *routingTestChannel) Name() string { return "test" }
func (c *routingTestChannel) Start(context.Context, channel.InboundHandler) error {
	return nil
}
func (c *routingTestChannel) Send(context.Context, channel.OutboundMessage) error { return nil }
func (c *routingTestChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return &testStreamUpdater{}, nil
}
func (c *routingTestChannel) Stop(context.Context) error { return nil }
func (c *routingTestChannel) SendApprovalRequest(_ context.Context, target channel.MessageTarget, toolName string, input string) (bool, error) {
	c.approvalTarget = target
	c.approvalTool = toolName
	c.approvalInput = input
	return c.approved, c.approvalErr
}
func (c *routingTestChannel) SendToolActivity(_ context.Context, target channel.MessageTarget, act channel.ToolActivity) error {
	c.activityTarget = target
	c.activityTool = act.ToolName
	c.activityDone = act.Done
	return nil
}

type testStreamUpdater struct{}

func (u *testStreamUpdater) Update(string) error { return nil }
func (u *testStreamUpdater) Finish(string) error { return nil }

func TestGatewayToolRoutingUsesSessionID(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	sessions := session.NewManager(db)
	sess, err := sessions.Get(context.Background(), "test", "chan-1")
	if err != nil {
		t.Fatal(err)
	}
	ch := &routingTestChannel{approved: true}
	channels := &ChannelSubsystem{channels: map[string]channel.Channel{"test": ch}}

	approver := NewGatewayToolApprover(sessions, channels, action.NewClassifierWithCompensableHTTP())
	approved, err := approver.RequestApproval(context.Background(), &tool.ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"echo ok"}`,
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if !approved {
		t.Fatal("expected approval response from routed channel")
	}
	if ch.approvalTool != "bash" {
		t.Fatalf("approval tool = %q, want bash", ch.approvalTool)
	}
	if ch.approvalTarget.Channel != "test" || ch.approvalTarget.ChannelID != "chan-1" {
		t.Fatalf("approval target = %+v, want test/chan-1", ch.approvalTarget)
	}

	reporter := NewGatewayToolActivityReporter(sessions, channels)
	reporter.ReportToolActivity(context.Background(), &tool.ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"echo ok"}`,
		SessionID: sess.ID,
	}, tool.ToolActivityEvent{ID: "act_test", Done: true, Result: &tool.ToolResult{Output: "ok"}})
	if ch.activityTool != "bash" {
		t.Fatalf("activity tool = %q, want bash", ch.activityTool)
	}
	if ch.activityTarget.Channel != "test" || ch.activityTarget.ChannelID != "chan-1" {
		t.Fatalf("activity target = %+v, want test/chan-1", ch.activityTarget)
	}
	if !ch.activityDone {
		t.Fatal("expected done activity event")
	}
}

func TestGatewayToolApproverAllowsAutonomousReadOnlyCalls(t *testing.T) {
	sessions, sess := newToolApproverTestSession(t, "internal", "auto-1")
	approver := NewGatewayToolApprover(sessions, &ChannelSubsystem{channels: map[string]channel.Channel{}}, action.NewClassifierWithCompensableHTTP())

	approved, err := approver.RequestApproval(context.Background(), &tool.ToolCall{
		ToolName:     "memory",
		Input:        `{"operation":"search","query":"x"}`,
		SessionID:    sess.ID,
		Capabilities: tool.ToolCapabilities{IsDestructive: true},
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if !approved {
		t.Fatal("expected autonomous read-only memory search approval")
	}
	if got := approver.DenyCount(); got != 0 {
		t.Fatalf("DenyCount() = %d, want 0", got)
	}
}

func TestGatewayToolApproverDeniesAutonomousSideEffects(t *testing.T) {
	sessions, sess := newToolApproverTestSession(t, "internal", "auto-2")
	approver := NewGatewayToolApprover(sessions, &ChannelSubsystem{channels: map[string]channel.Channel{}}, action.NewClassifierWithCompensableHTTP())

	tests := []struct {
		name string
		call *tool.ToolCall
	}{
		{
			name: "memory save",
			call: &tool.ToolCall{
				ToolName:     "memory",
				Input:        `{"operation":"save","content":"x"}`,
				SessionID:    sess.ID,
				Capabilities: tool.ToolCapabilities{IsDestructive: true},
			},
		},
		{
			name: "values record",
			call: &tool.ToolCall{
				ToolName:     "values",
				Input:        `{"operation":"record","content":"never self-authorize"}`,
				SessionID:    sess.ID,
				Capabilities: tool.ToolCapabilities{IsDestructive: true},
			},
		},
		{
			name: "http post",
			call: &tool.ToolCall{
				ToolName:  "http",
				Input:     `{"method":"POST","url":"https://example.test"}`,
				SessionID: sess.ID,
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approved, err := approver.RequestApproval(context.Background(), tt.call)
			if err != nil {
				t.Fatalf("RequestApproval() error = %v", err)
			}
			if approved {
				t.Fatal("expected autonomous side-effecting call to be denied")
			}
			if got, want := approver.DenyCount(), int64(i+1); got != want {
				t.Fatalf("DenyCount() = %d, want %d", got, want)
			}
		})
	}
}

func TestGatewayToolApproverDeniesWithoutClassifier(t *testing.T) {
	sessions, sess := newToolApproverTestSession(t, "internal", "auto-3")
	approver := NewGatewayToolApprover(sessions, &ChannelSubsystem{channels: map[string]channel.Channel{}}, nil)

	approved, err := approver.RequestApproval(context.Background(), &tool.ToolCall{
		ToolName:  "memory",
		Input:     `{"operation":"search","query":"x"}`,
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if approved {
		t.Fatal("expected nil classifier to deny")
	}
	if got := approver.DenyCount(); got != 1 {
		t.Fatalf("DenyCount() = %d, want 1", got)
	}
}

func TestGatewayToolApproverInteractiveApproval(t *testing.T) {
	tests := []struct {
		name         string
		channel      *routingTestChannel
		wantApproved bool
	}{
		{name: "approved", channel: &routingTestChannel{approved: true}, wantApproved: true},
		{name: "denied", channel: &routingTestChannel{approved: false}, wantApproved: false},
		{name: "error", channel: &routingTestChannel{approved: true, approvalErr: errors.New("boom")}, wantApproved: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions, sess := newToolApproverTestSession(t, "test", tt.name)
			channels := &ChannelSubsystem{channels: map[string]channel.Channel{"test": tt.channel}}
			approver := NewGatewayToolApprover(sessions, channels, action.NewClassifierWithCompensableHTTP())

			approved, err := approver.RequestApproval(context.Background(), &tool.ToolCall{
				ToolName:  "bash",
				Input:     `{"command":"echo ok"}`,
				SessionID: sess.ID,
			})
			if err != nil {
				t.Fatalf("RequestApproval() error = %v", err)
			}
			if approved != tt.wantApproved {
				t.Fatalf("approved = %v, want %v", approved, tt.wantApproved)
			}
			if tt.channel.approvalTool != "bash" {
				t.Fatalf("approval tool = %q, want bash", tt.channel.approvalTool)
			}
			if tt.channel.approvalInput != `{"command":"echo ok"}` {
				t.Fatalf("approval input = %q", tt.channel.approvalInput)
			}
			if tt.channel.approvalTarget.Channel != "test" || tt.channel.approvalTarget.ChannelID != tt.name {
				t.Fatalf("approval target = %+v, want test/%s", tt.channel.approvalTarget, tt.name)
			}
		})
	}
}

func newToolApproverTestSession(t *testing.T, channelName, channelID string) (*session.Manager, *session.Session) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "tool-approver.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sessions := session.NewManager(db)
	sess, err := sessions.Get(context.Background(), channelName, channelID)
	if err != nil {
		t.Fatal(err)
	}
	return sessions, sess
}

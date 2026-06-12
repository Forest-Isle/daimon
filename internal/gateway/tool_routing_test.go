package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type routingTestChannel struct {
	approved       bool
	approvalTarget channel.MessageTarget
	approvalTool   string
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
func (c *routingTestChannel) SendApprovalRequest(_ context.Context, target channel.MessageTarget, toolName string, _ string) (bool, error) {
	c.approvalTarget = target
	c.approvalTool = toolName
	return c.approved, nil
}
func (c *routingTestChannel) SendToolActivity(_ context.Context, target channel.MessageTarget, toolName, _ string, done bool) error {
	c.activityTarget = target
	c.activityTool = toolName
	c.activityDone = done
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

	approver := NewGatewayToolApprover(sessions, channels)
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
	}, true)
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

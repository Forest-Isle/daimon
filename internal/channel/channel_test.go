package channel

import (
	"context"
	"testing"
)

// Compile-time interface satisfaction checks
var (
	_ Channel       = (*mockChannel)(nil)
	_ StreamUpdater = (*mockStreamUpdater)(nil)
)

type mockChannel struct {
	name string
}

func (m *mockChannel) Name() string                                            { return m.name }
func (m *mockChannel) Start(ctx context.Context, handler InboundHandler) error { return nil }
func (m *mockChannel) Send(ctx context.Context, msg OutboundMessage) error     { return nil }
func (m *mockChannel) SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error) {
	return &mockStreamUpdater{}, nil
}
func (m *mockChannel) Stop(ctx context.Context) error { return nil }

type mockStreamUpdater struct{}

func (m *mockStreamUpdater) Update(text string) error { return nil }
func (m *mockStreamUpdater) Finish(text string) error { return nil }

func TestChannelInterface(t *testing.T) {
	c := &mockChannel{name: "test"}
	if c.Name() != "test" {
		t.Errorf("expected Name 'test', got %q", c.Name())
	}
}

func TestStreamUpdaterInterface(t *testing.T) {
	u := &mockStreamUpdater{}
	if err := u.Update("progress"); err != nil {
		t.Errorf("Update: %v", err)
	}
	if err := u.Finish("done"); err != nil {
		t.Errorf("Finish: %v", err)
	}
}

func TestInboundMessage_Fields(t *testing.T) {
	m := InboundMessage{
		Channel:      "telegram",
		ChannelID:    "123",
		UserID:       "user1",
		UserName:     "TestUser",
		Text:         "Hello",
		CallbackData: "cb_data",
		ReplyToMsgID: "456",
	}
	if m.Channel != "telegram" {
		t.Errorf("unexpected Channel: %q", m.Channel)
	}
	if m.UserName != "TestUser" {
		t.Errorf("unexpected UserName: %q", m.UserName)
	}
	if m.CallbackData != "cb_data" {
		t.Errorf("unexpected CallbackData: %q", m.CallbackData)
	}
}

func TestOutboundMessage_Fields(t *testing.T) {
	m := OutboundMessage{
		Channel:   "discord",
		ChannelID: "789",
		Text:      "Response",
		ParseMode: "Markdown",
	}
	if m.Channel != "discord" {
		t.Errorf("unexpected Channel: %q", m.Channel)
	}
	if m.ParseMode != "Markdown" {
		t.Errorf("unexpected ParseMode: %q", m.ParseMode)
	}
}

func TestMessageTarget(t *testing.T) {
	target := MessageTarget{
		Channel:   "telegram",
		ChannelID: "channel_1",
	}
	if target.Channel != "telegram" {
		t.Errorf("unexpected Channel: %q", target.Channel)
	}
	if target.ChannelID != "channel_1" {
		t.Errorf("unexpected ChannelID: %q", target.ChannelID)
	}
}

func TestApprovalSenderInterface(t *testing.T) {
	// Compile-time check
	var s ApprovalSender = &mockApprovalSender{}
	_, err := s.SendApprovalRequest(context.Background(), MessageTarget{}, "bash", "ls")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

type mockApprovalSender struct{}

func (m *mockApprovalSender) SendApprovalRequest(ctx context.Context, target MessageTarget, toolName string, input string) (bool, error) {
	return true, nil
}

func TestNotificationSenderInterface(t *testing.T) {
	var s NotificationSender = &mockNotificationSender{}
	err := s.SendNotification(context.Background(), MessageTarget{}, "notification text")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

type mockNotificationSender struct{}

func (m *mockNotificationSender) SendNotification(ctx context.Context, target MessageTarget, text string) error {
	return nil
}

func TestFeedbackSenderInterface(t *testing.T) {
	var s FeedbackSender = &mockFeedbackSender{}
	score, err := s.SendFeedbackRequest(context.Background(), MessageTarget{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

type mockFeedbackSender struct{}

func (m *mockFeedbackSender) SendFeedbackRequest(ctx context.Context, target MessageTarget) (float64, error) {
	return 1.0, nil
}

func TestToolStreamWriterInterface(t *testing.T) {
	var w ToolStreamWriter = &mockToolStreamWriter{}
	err := w.WriteToolStream(context.Background(), MessageTarget{}, "bash", "output chunk")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	err = w.FlushToolStream(context.Background(), MessageTarget{}, "bash")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

type mockToolStreamWriter struct{}

func (m *mockToolStreamWriter) WriteToolStream(ctx context.Context, target MessageTarget, toolName string, chunk string) error {
	return nil
}
func (m *mockToolStreamWriter) FlushToolStream(ctx context.Context, target MessageTarget, toolName string) error {
	return nil
}

func TestInboundHandlerType(t *testing.T) {
	// Verify InboundHandler function signature
	var handler InboundHandler = func(ctx context.Context, msg InboundMessage) {}
	handler(context.Background(), InboundMessage{Text: "test"})
}

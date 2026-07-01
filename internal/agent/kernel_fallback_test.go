package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type kernelCaptureChannel struct {
	sent []string
}

func (c *kernelCaptureChannel) Name() string                                        { return "test" }
func (c *kernelCaptureChannel) Start(context.Context, channel.InboundHandler) error { return nil }
func (c *kernelCaptureChannel) Stop(context.Context) error                          { return nil }
func (c *kernelCaptureChannel) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.sent = append(c.sent, msg.Text)
	return nil
}
func (c *kernelCaptureChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return &kernelCaptureUpdater{ch: c}, nil
}

type kernelCaptureUpdater struct {
	ch *kernelCaptureChannel
}

func (u *kernelCaptureUpdater) Update(string) error { return nil }
func (u *kernelCaptureUpdater) Finish(text string) error {
	u.ch.sent = append(u.ch.sent, text)
	return nil
}

func TestAgentKernelFailureDoesNotFallbackByDefault(t *testing.T) {
	db := newTestDB(t)
	provider := &testProvider{text: "legacy response should not run"}
	deps := AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Tools:    tool.NewRegistry(),
			Sessions: session.NewManager(db),
			DB:       db,
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	a.SetKernel(&fakeKernel{err: errors.New("kernel boom")}, true)
	ch := &kernelCaptureChannel{}

	err := a.HandleMessage(context.Background(), ch, channel.InboundMessage{Channel: "test", ChannelID: "u1", Text: "hello"})
	if err == nil {
		t.Fatal("strict kernel failure should return an error")
	}
	if provider.callCount != 0 {
		t.Fatalf("legacy provider calls = %d, want 0", provider.callCount)
	}
	if len(ch.sent) != 1 || !strings.Contains(ch.sent[0], "governed episode kernel") {
		t.Fatalf("failure reply = %#v", ch.sent)
	}
}

func TestAgentKernelFailureFallbackRequiresExplicitConfig(t *testing.T) {
	db := newTestDB(t)
	provider := &testProvider{text: "legacy response"}
	deps := AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Tools:    tool.NewRegistry(),
			Sessions: session.NewManager(db),
			DB:       db,
			Cfg:      config.AgentConfig{MaxIterations: 1, KernelFallbackEnabled: true},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	a.SetKernel(&fakeKernel{err: errors.New("kernel boom")}, true)
	ch := &kernelCaptureChannel{}

	err := a.HandleMessage(context.Background(), ch, channel.InboundMessage{Channel: "test", ChannelID: "u1", Text: "hello"})
	if err != nil {
		t.Fatalf("fallback-enabled HandleMessage() error = %v", err)
	}
	if provider.callCount == 0 {
		t.Fatal("legacy provider was not called with fallback enabled")
	}
	if len(ch.sent) == 0 || !strings.Contains(ch.sent[len(ch.sent)-1], "legacy response") {
		t.Fatalf("legacy reply not sent: %#v", ch.sent)
	}
}

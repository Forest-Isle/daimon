package eval

import (
	"context"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// EvalChannel is a headless channel adapter for running evaluations.
// It auto-approves all tool calls, captures sent messages, and provides
// a no-op streaming implementation.
type EvalChannel struct {
	mu       sync.Mutex
	messages []channel.OutboundMessage
}

var _ channel.Channel = (*EvalChannel)(nil)
var _ channel.ApprovalSender = (*EvalChannel)(nil)
var _ channel.ReflectionSender = (*EvalChannel)(nil)

func (c *EvalChannel) Name() string { return "eval" }

func (c *EvalChannel) Start(_ context.Context, _ channel.InboundHandler) error { return nil }

func (c *EvalChannel) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.mu.Lock()
	c.messages = append(c.messages, msg)
	c.mu.Unlock()
	return nil
}

func (c *EvalChannel) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return &evalStreamUpdater{ch: c}, nil
}

func (c *EvalChannel) Stop(_ context.Context) error { return nil }

// SendApprovalRequest auto-approves all tool calls during evaluation.
func (c *EvalChannel) SendApprovalRequest(_ context.Context, _ channel.MessageTarget, _ string, _ string) (bool, error) {
	return true, nil
}

// SendReflectionRequest always continues during evaluation.
func (c *EvalChannel) SendReflectionRequest(_ context.Context, _ channel.MessageTarget, _ string, _ float64) (channel.ReplanDecision, error) {
	return channel.ReplanContinue, nil
}

// Messages returns a copy of all captured outbound messages.
func (c *EvalChannel) Messages() []channel.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]channel.OutboundMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// LastMessage returns the text of the last captured message, or empty string.
func (c *EvalChannel) LastMessage() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return ""
	}
	return c.messages[len(c.messages)-1].Text
}

// Reset clears captured messages between task runs.
func (c *EvalChannel) Reset() {
	c.mu.Lock()
	c.messages = c.messages[:0]
	c.mu.Unlock()
}

type evalStreamUpdater struct {
	ch   *EvalChannel
	text string
}

func (u *evalStreamUpdater) Update(text string) error {
	u.text = text
	return nil
}

func (u *evalStreamUpdater) Finish(text string) error {
	u.text = text
	_ = u.ch.Send(context.Background(), channel.OutboundMessage{Text: text})
	return nil
}

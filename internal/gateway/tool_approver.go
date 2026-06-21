package gateway

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// GatewayToolApprover implements tool.ToolApprover by routing approval
// requests through active gateway channels. It holds a reference to the
// gateway's session manager and channel map so it can look up the
// appropriate channel at request time.
//
// Security note: When no ApprovalSender-capable channel is available,
// GatewayToolApprover DENIES the tool execution. This is a safer default
// than silently auto-approving (the previous behavior with nil approver).
type GatewayToolApprover struct {
	sessions   *session.Manager
	channels   *ChannelSubsystem
	classifier action.Classifier // read-only determination for autonomous (channel-less) calls
	denyCount  atomic.Int64
}

// NewGatewayToolApprover creates a GatewayToolApprover that will look up
// channels at approval time.
func NewGatewayToolApprover(sessions *session.Manager, channels *ChannelSubsystem, classifier action.Classifier) *GatewayToolApprover {
	return &GatewayToolApprover{
		sessions:   sessions,
		channels:   channels,
		classifier: classifier,
	}
}

func (a *GatewayToolApprover) RequestApproval(ctx context.Context, call *tool.ToolCall) (bool, error) {
	// Try to reach an interactive approval channel (the chat path: a human signs off).
	if sender, target, ok := a.interactiveApprover(ctx, call); ok {
		approved, err := sender.SendApprovalRequest(ctx, target, call.ToolName, call.Input)
		if err != nil {
			slog.Warn("gateway: approval request failed, denying", "tool", call.ToolName, "err", err)
			return false, nil
		}
		return approved, nil
	}

	// No interactive channel: this is an autonomous (channel="internal") episode.
	// Defer the decision to the action classifier - a read-only call has no side
	// effects and is safe to run without human sign-off; anything governed
	// (side-effecting: memory writes, values.record, http POST, send_email, ...) is
	// denied fail-closed, because an autonomous episode has no human to approve and
	// must never self-authorize a side effect.
	if a.classifier != nil {
		if _, governed := a.classifier.Classify(call); !governed {
			return true, nil
		}
	}
	a.denyCount.Add(1)
	slog.Warn("gateway: no interactive channel for autonomous tool call, denying side-effecting tool",
		"tool", call.ToolName)
	return false, nil
}

// interactiveApprover resolves the ApprovalSender + target for a call's session,
// or ok=false when no interactive approval channel is reachable (autonomous).
func (a *GatewayToolApprover) interactiveApprover(ctx context.Context, call *tool.ToolCall) (channel.ApprovalSender, channel.MessageTarget, bool) {
	sess, err := a.sessions.GetByID(ctx, call.SessionID)
	if err != nil || sess == nil {
		return nil, channel.MessageTarget{}, false
	}
	ch := a.channels.Channel(sess.Channel)
	if ch == nil {
		return nil, channel.MessageTarget{}, false
	}
	sender, ok := ch.(channel.ApprovalSender)
	if !ok {
		return nil, channel.MessageTarget{}, false
	}
	return sender, channel.MessageTarget{Channel: sess.Channel, ChannelID: sess.ChannelID}, true
}

// DenyCount returns the number of tool executions denied due to missing
// channel or ApprovalSender capability.
func (a *GatewayToolApprover) DenyCount() int64 {
	return a.denyCount.Load()
}

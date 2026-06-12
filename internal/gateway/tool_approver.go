package gateway

import (
	"context"
	"log/slog"
	"sync/atomic"

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
	sessions  *session.Manager
	channels  *ChannelSubsystem
	denyCount atomic.Int64 // tracks denials due to missing channel (observability)
}

// NewGatewayToolApprover creates a GatewayToolApprover that will look up
// channels at approval time.
func NewGatewayToolApprover(sessions *session.Manager, channels *ChannelSubsystem) *GatewayToolApprover {
	return &GatewayToolApprover{
		sessions: sessions,
		channels: channels,
	}
}

func (a *GatewayToolApprover) RequestApproval(ctx context.Context, call *tool.ToolCall) (bool, error) {
	// Look up the session to find which channel this tool execution belongs to.
	sess, err := a.sessions.GetByID(ctx, call.SessionID)
	if err != nil || sess == nil {
		slog.Warn("gateway: cannot find session for approval, denying",
			"session_id", call.SessionID, "tool", call.ToolName, "err", err)
		a.denyCount.Add(1)
		return false, nil
	}

	// Find the channel that owns this session.
	ch := a.channels.Channel(sess.Channel)
	if ch == nil {
		slog.Warn("gateway: channel not found for approval, denying",
			"channel", sess.Channel, "tool", call.ToolName)
		a.denyCount.Add(1)
		return false, nil
	}

	// Route through ApprovalSender if the channel supports it.
	if sender, ok := ch.(channel.ApprovalSender); ok {
		target := channel.MessageTarget{
			Channel:   sess.Channel,
			ChannelID: sess.ChannelID,
		}
		approved, err := sender.SendApprovalRequest(ctx, target, call.ToolName, call.Input)
		if err != nil {
			slog.Warn("gateway: approval request failed, denying",
				"tool", call.ToolName, "err", err)
			return false, nil
		}
		return approved, nil
	}

	// No ApprovalSender-capable channel available for this session.
	// Deny rather than silently auto-approve.
	slog.Warn("gateway: channel does not support approval, denying",
		"channel", sess.Channel, "tool", call.ToolName)
	a.denyCount.Add(1)
	return false, nil
}

// DenyCount returns the number of tool executions denied due to missing
// channel or ApprovalSender capability.
func (a *GatewayToolApprover) DenyCount() int64 {
	return a.denyCount.Load()
}

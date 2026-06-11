package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// GatewayToolActivityReporter implements tool.ToolActivityReporter by routing
// live tool-activity notifications through the active gateway channel for the
// session. It mirrors GatewayToolApprover's session→channel lookup, but is
// non-blocking and never affects execution: it only tells the user which tool
// is running right now.
type GatewayToolActivityReporter struct {
	sessions *session.Manager
	channels *ChannelSubsystem
}

// NewGatewayToolActivityReporter creates a reporter that resolves the channel
// at report time.
func NewGatewayToolActivityReporter(sessions *session.Manager, channels *ChannelSubsystem) *GatewayToolActivityReporter {
	return &GatewayToolActivityReporter{sessions: sessions, channels: channels}
}

func (r *GatewayToolActivityReporter) ReportToolActivity(ctx context.Context, call *tool.ToolCall, done bool) {
	if r.sessions == nil || r.channels == nil {
		return
	}
	sess, err := r.sessions.Get(ctx, "", call.SessionID)
	if err != nil || sess == nil {
		return
	}
	ch := r.channels.Channel(sess.Channel)
	if ch == nil {
		return
	}
	sender, ok := ch.(channel.ToolActivitySender)
	if !ok {
		return
	}
	summary := ""
	if !done {
		summary = summarizeToolInput(call.ToolName, call.Input)
	}
	_ = sender.SendToolActivity(ctx, channel.MessageTarget{ChannelID: sess.ChannelID},
		call.ToolName, summary, done)
}

// summarizeToolInput extracts a short, human-readable hint from a tool call's
// JSON input — the command for bash, the path for file ops, etc. Falls back to
// empty when no recognizable field is present.
func summarizeToolInput(toolName, input string) string {
	var m map[string]any
	if json.Unmarshal([]byte(input), &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "path", "file_path", "query", "url", "pattern"} {
		if v, ok := m[key].(string); ok && v != "" {
			return clampSummary(v)
		}
	}
	return ""
}

func clampSummary(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

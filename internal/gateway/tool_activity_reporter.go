package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// GatewayToolActivityReporter routes live tool-activity events through the
// active gateway channel for the session. Non-blocking; never affects execution.
type GatewayToolActivityReporter struct {
	sessions *session.Manager
	channels *ChannelSubsystem
}

func NewGatewayToolActivityReporter(sessions *session.Manager, channels *ChannelSubsystem) *GatewayToolActivityReporter {
	return &GatewayToolActivityReporter{sessions: sessions, channels: channels}
}

func (r *GatewayToolActivityReporter) ReportToolActivity(ctx context.Context, call *tool.ToolCall, evt tool.ToolActivityEvent) {
	if r.sessions == nil || r.channels == nil {
		return
	}
	sess, err := r.sessions.GetByID(ctx, call.SessionID)
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

	act := channel.ToolActivity{
		CallID:   evt.ID,
		ToolName: call.ToolName,
		Done:     evt.Done,
	}
	if !evt.Done {
		act.ArgSummary = summarizeToolInput(call.ToolName, call.Input)
	} else {
		errText := ""
		if evt.Err != nil {
			errText = evt.Err.Error()
		}
		output := ""
		if evt.Result != nil {
			if errText == "" {
				errText = evt.Result.Error
			}
			output = evt.Result.Output
		}
		act.Duration = evt.Duration
		act.OK = errText == ""
		act.ResultSummary = deriveResultSummary(errText, output)
		act.Output = capOutput(output)
	}

	_ = sender.SendToolActivity(ctx, channel.MessageTarget{Channel: sess.Channel, ChannelID: sess.ChannelID}, act)
}

// deriveResultSummary produces a short, tool-agnostic outcome hint: the error's
// first line on failure, a line count for multi-line output, or the clamped
// first line otherwise. Deliberately generic (no per-tool parsing).
func deriveResultSummary(errText, output string) string {
	if errText != "" {
		return "error: " + clampSummary(firstLine(errText))
	}
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return "done"
	}
	if n := strings.Count(output, "\n") + 1; n > 1 {
		return fmt.Sprintf("%d lines", n)
	}
	return clampSummary(output)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const (
	maxOutputBytes = 4096
	maxOutputLines = 50
)

// capOutput bounds raw tool output before it reaches the TUI: at most
// maxOutputLines lines and maxOutputBytes bytes, trimmed to a valid rune
// boundary, with a truncation marker when clipped.
func capOutput(s string) string {
	truncated := false
	if lines := strings.SplitN(s, "\n", maxOutputLines+1); len(lines) > maxOutputLines {
		s = strings.Join(lines[:maxOutputLines], "\n")
		truncated = true
	}
	if len(s) > maxOutputBytes {
		s = s[:maxOutputBytes]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		truncated = true
	}
	if truncated {
		s += "\n… (truncated)"
	}
	return s
}

// summarizeToolInput extracts a short hint from a tool call's JSON input.
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

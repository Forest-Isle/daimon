package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// GatewayToolNotifier implements tool.ToolNotifier by logging tool
// execution notifications via slog. When wired into PermissionInterceptor,
// this ensures that the user is at least informed (via logs/TUI) when a
// tool executes under PermissionNotify, even when interactive channels
// are not yet available at init_tools time.
type GatewayToolNotifier struct{}

func NewGatewayToolNotifier() *GatewayToolNotifier {
	return &GatewayToolNotifier{}
}

func (n *GatewayToolNotifier) NotifyToolExecution(_ context.Context, call *tool.ToolCall) error {
	slog.Info("gateway: tool notification",
		"tool", call.ToolName,
		"session", call.SessionID,
		"input_len", len(call.Input),
	)
	return nil
}

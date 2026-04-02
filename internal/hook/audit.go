package hook

import (
	"context"
	"log/slog"
)

// AuditLogHandler is a PostToolUse handler that logs all tool executions.
type AuditLogHandler struct{}

// NewAuditLogHandler creates an audit log handler.
func NewAuditLogHandler() *AuditLogHandler {
	return &AuditLogHandler{}
}

func (h *AuditLogHandler) OnPostToolUse(_ context.Context, event PostToolUseEvent) (PostToolUseResult, error) {
	inputSummary := event.Input
	if len(inputSummary) > 200 {
		inputSummary = inputSummary[:200] + "..."
	}

	slog.Info("hook: audit log",
		"tool", event.ToolName,
		"status", event.Status,
		"duration_ms", event.DurationMs,
		"input_summary", inputSummary,
	)

	return PostToolUseResult{}, nil
}

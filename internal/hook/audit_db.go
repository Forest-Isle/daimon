package hook

import (
	"context"
	"database/sql"
)

// PermissionAuditHandler is a PostToolUse handler that records permission
// decisions to a SQLite audit log table.
type PermissionAuditHandler struct {
	db *sql.DB
}

// NewPermissionAuditHandler creates a permission audit handler.
func NewPermissionAuditHandler(db *sql.DB) *PermissionAuditHandler {
	return &PermissionAuditHandler{db: db}
}

func (h *PermissionAuditHandler) OnPostToolUse(ctx context.Context, event PostToolUseEvent) (PostToolUseResult, error) {
	if h.db == nil {
		return PostToolUseResult{}, nil
	}

	inputSummary := TruncateInput(event.Input, 200)
	action := event.Status
	reason := ""
	matchedRule := ""

	// Extract permission metadata if available
	if event.PermissionAction != "" {
		action = event.PermissionAction
	}
	if event.PermissionReason != "" {
		reason = event.PermissionReason
	}
	if event.PermissionRule != "" {
		matchedRule = event.PermissionRule
	}

	_, err := h.db.ExecContext(ctx,
		`INSERT INTO permission_audit_log (session_id, tool_name, input_summary, action, matched_rule, reason) VALUES (?, ?, ?, ?, ?, ?)`,
		event.SessionID, event.ToolName, inputSummary, action, matchedRule, reason,
	)
	if err != nil {
		// Non-fatal — log but don't block tool execution
		return PostToolUseResult{}, nil
	}

	return PostToolUseResult{}, nil
}

// TruncateInput truncates input to maxLen chars, adding "..." if truncated.
func TruncateInput(input string, maxLen int) string {
	if len(input) <= maxLen {
		return input
	}
	return input[:maxLen] + "..."
}

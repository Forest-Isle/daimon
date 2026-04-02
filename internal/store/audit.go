package store

import (
	"context"
	"time"
)

// AuditLogEntry represents a single permission audit log entry.
type AuditLogEntry struct {
	ID           int64
	SessionID    string
	ToolName     string
	InputSummary string
	Action       string
	MatchedRule  string
	Reason       string
	CreatedAt    time.Time
}

// InsertAuditLog records a permission decision in the audit log.
func (db *DB) InsertAuditLog(ctx context.Context, sessionID, toolName, inputSummary, action, matchedRule, reason string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO permission_audit_log (session_id, tool_name, input_summary, action, matched_rule, reason) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, toolName, inputSummary, action, matchedRule, reason,
	)
	return err
}

// QueryAuditLogs retrieves audit log entries, optionally filtered by session or tool.
func (db *DB) QueryAuditLogs(ctx context.Context, sessionID, toolName string, limit int) ([]AuditLogEntry, error) {
	query := `SELECT id, session_id, tool_name, input_summary, action, matched_rule, reason, created_at FROM permission_audit_log WHERE 1=1`
	var args []any

	if sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if toolName != "" {
		query += ` AND tool_name = ?`
		args = append(args, toolName)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var e AuditLogEntry
		var matchedRule, reason *string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.ToolName, &e.InputSummary, &e.Action, &matchedRule, &reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		if matchedRule != nil {
			e.MatchedRule = *matchedRule
		}
		if reason != nil {
			e.Reason = *reason
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

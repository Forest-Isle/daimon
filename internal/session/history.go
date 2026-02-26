package session

import (
	"context"
	"log/slog"

	"github.com/punkopunko/ironclaw/internal/store"
)

// LogToolExecution records a tool call in the audit log.
func LogToolExecution(ctx context.Context, db *store.DB, sessionID, toolName, input, output, status string, durationMs int64) {
	_, err := db.ExecContext(ctx,
		`INSERT INTO tool_log (id, session_id, tool_name, input, output, status, duration_ms)
		 VALUES (hex(randomblob(8)), ?, ?, ?, ?, ?, ?)`,
		sessionID, toolName, input, output, status, durationMs)
	if err != nil {
		slog.Error("failed to log tool execution", "err", err)
	}
}

package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AuditLogger logs memory operations to the memory_audit_log table.
// It provides a reusable helper for audit logging across the memory subsystem.
type AuditLogger struct {
	db *sql.DB
}

// NewAuditLogger creates a new AuditLogger.
func NewAuditLogger(db *sql.DB) *AuditLogger {
	return &AuditLogger{db: db}
}

// Log records an audit event for a memory operation.
func (al *AuditLogger) Log(ctx context.Context, memoryID, action, actor, details string) {
	if al.db == nil {
		return
	}
	id := fmt.Sprintf("audit_%d", time.Now().UnixNano())
	_, _ = al.db.ExecContext(ctx, `
		INSERT INTO memory_audit_log (id, memory_id, action, actor, timestamp, details)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, memoryID, action, actor, time.Now(), details)
}

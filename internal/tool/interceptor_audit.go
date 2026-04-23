package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry represents a single audit log record.
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	SessionID  string    `json:"session_id"`
	ToolName   string    `json:"tool_name"`
	InputHash  string    `json:"input_hash"`
	Decision   string    `json:"decision"` // "allowed", "denied", "approved", "error"
	ResultOK   bool      `json:"result_ok"`
	DurationMs int64     `json:"duration_ms"`
	ErrorMsg   string    `json:"error,omitempty"`
}

// AuditInterceptor implements ToolInterceptor and logs all tool executions
// to JSONL files in the audit directory, rotated daily.
type AuditInterceptor struct {
	logDir string
	mu     sync.Mutex
	file   *os.File
	curDay string
}

// NewAuditInterceptor creates an audit interceptor that writes to the given directory.
// The directory is created if it doesn't exist.
func NewAuditInterceptor(logDir string) (*AuditInterceptor, error) {
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		logDir = filepath.Join(home, ".ironclaw", "audit")
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	return &AuditInterceptor{logDir: logDir}, nil
}

func (a *AuditInterceptor) Name() string { return "audit" }

// Intercept logs the tool call and its result to the audit log.
func (a *AuditInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	start := time.Now()

	result, err := next(ctx, call)

	duration := time.Since(start)

	entry := AuditEntry{
		Timestamp:  start,
		SessionID:  call.SessionID,
		ToolName:   call.ToolName,
		InputHash:  hashInput(call.Input),
		DurationMs: duration.Milliseconds(),
	}

	if err != nil {
		entry.Decision = "error"
		entry.ResultOK = false
		entry.ErrorMsg = err.Error()
	} else if result != nil && result.Error != "" {
		entry.Decision = "denied"
		entry.ResultOK = false
		entry.ErrorMsg = result.Error
	} else {
		entry.Decision = "allowed"
		entry.ResultOK = true
	}

	// Write asynchronously to avoid blocking tool execution
	go a.writeEntry(entry)

	return result, err
}

// Close flushes and closes the current log file.
func (a *AuditInterceptor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		err := a.file.Close()
		a.file = nil
		return err
	}
	return nil
}

func (a *AuditInterceptor) writeEntry(entry AuditEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()

	day := entry.Timestamp.Format("2006-01-02")
	if err := a.ensureFile(day); err != nil {
		return
	}

	_, _ = a.file.Write(data)
}

func (a *AuditInterceptor) ensureFile(day string) error {
	if a.curDay == day && a.file != nil {
		return nil
	}
	if a.file != nil {
		_ = a.file.Close()
	}
	path := filepath.Join(a.logDir, fmt.Sprintf("audit-%s.jsonl", day))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	a.file = f
	a.curDay = day
	return nil
}

// hashInput returns a SHA-256 hash of the input for audit logging
// without storing sensitive data.
func hashInput(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:8]) // first 8 bytes = 16 hex chars
}

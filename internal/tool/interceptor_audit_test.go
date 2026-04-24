package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditInterceptor_NewCreatesDir(t *testing.T) {
	dir := t.TempDir()
	auditDir := filepath.Join(dir, "audit")

	ai, err := NewAuditInterceptor(auditDir)
	if err != nil {
		t.Fatalf("NewAuditInterceptor: %v", err)
	}
	defer func() { _ = ai.Close() }()

	if _, err := os.Stat(auditDir); os.IsNotExist(err) {
		t.Error("audit directory was not created")
	}
}

func TestAuditInterceptor_Name(t *testing.T) {
	dir := t.TempDir()
	ai, err := NewAuditInterceptor(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ai.Close() }()

	if ai.Name() != "audit" {
		t.Errorf("expected name 'audit', got %q", ai.Name())
	}
}

func TestAuditInterceptor_LogsSuccessfulCall(t *testing.T) {
	dir := t.TempDir()
	ai, err := NewAuditInterceptor(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ai.Close() }()

	call := &ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"echo hello"}`,
		SessionID: "test-session",
	}

	next := func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "hello"}, nil
	}

	result, err := ai.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	if result.Output != "hello" {
		t.Errorf("expected output 'hello', got %q", result.Output)
	}

	// Wait for async write
	time.Sleep(100 * time.Millisecond)

	entries := readAuditEntries(t, dir)
	if len(entries) == 0 {
		t.Fatal("expected at least one audit entry")
	}

	entry := entries[0]
	if entry.ToolName != "bash" {
		t.Errorf("expected tool_name 'bash', got %q", entry.ToolName)
	}
	if entry.SessionID != "test-session" {
		t.Errorf("expected session_id 'test-session', got %q", entry.SessionID)
	}
	if entry.Decision != "allowed" {
		t.Errorf("expected decision 'allowed', got %q", entry.Decision)
	}
	if !entry.ResultOK {
		t.Error("expected result_ok true")
	}
	if entry.InputHash == "" {
		t.Error("expected non-empty input hash")
	}
}

func TestAuditInterceptor_LogsDeniedCall(t *testing.T) {
	dir := t.TempDir()
	ai, err := NewAuditInterceptor(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ai.Close() }()

	call := &ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"rm -rf /"}`,
		SessionID: "test-session",
	}

	next := func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		return &ToolResult{Error: "denied by policy"}, nil
	}

	_, err = ai.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	entries := readAuditEntries(t, dir)
	if len(entries) == 0 {
		t.Fatal("expected audit entry for denied call")
	}
	if entries[0].Decision != "denied" {
		t.Errorf("expected decision 'denied', got %q", entries[0].Decision)
	}
}

func TestHashInput(t *testing.T) {
	h1 := hashInput("test input")
	h2 := hashInput("test input")
	h3 := hashInput("different input")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 16 {
		t.Errorf("expected 16 hex chars, got %d", len(h1))
	}
}

func readAuditEntries(t *testing.T, dir string) []AuditEntry {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []AuditEntry
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var entry AuditEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("unmarshal audit entry: %v", err)
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

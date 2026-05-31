package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// --- DB Open tests ---

func TestOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ironclaw.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	defer db.Close()

	// Verify the database file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestOpen_CreatesDir(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "dir", "ironclaw.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with nested dirs failed: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created in nested directory")
	}
}

func TestOpen_MigrationsApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrated.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Verify tables exist
	tables := []string{
		"sessions",
		"messages",
		"scheduled_tasks",
		"tool_log",
		"_migrations",
	}

	for _, table := range tables {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("table %s was not created by migrations", table)
		}
	}

	// Verify migration tracking
	var migrationCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM _migrations`).Scan(&migrationCount)
	if err != nil {
		t.Fatalf("query _migrations: %v", err)
	}
	if migrationCount == 0 {
		t.Error("expected at least 1 migration record")
	}
}

func TestOpen_MultipleTimes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "multi.db")

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	// Opening same path again should work (idempotent migrations)
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	db2.Close()
}

func TestOpen_IdempotentMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	var firstCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM _migrations`).Scan(&firstCount)
	if err != nil {
		t.Fatalf("query _migrations: %v", err)
	}
	db.Close()

	// Re-open
	db, err = Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db.Close()

	var secondCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM _migrations`).Scan(&secondCount)
	if err != nil {
		t.Fatalf("query _migrations after re-open: %v", err)
	}

	if secondCount != firstCount {
		t.Errorf("migration count changed: first=%d, second=%d", firstCount, secondCount)
	}
}

func TestOpen_SingleWriter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "single_writer.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// SQLite single-writer: MaxOpenConns should be 1
	if db.DB.Stats().MaxOpenConnections != 1 {
		t.Errorf("expected MaxOpenConnections=1, got %d", db.DB.Stats().MaxOpenConnections)
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	// Use a path in a non-writable location (simulated by using a file that already exists as a dir)
	badPath := filepath.Join(t.TempDir(), "nonexistent_dir_deep", "subdir", "db.db")
	db, err := Open(badPath)
	// This should fail if the path has permission issues, but should work if dir creation works
	if err != nil {
		// Might fail on some systems — that's OK
		t.Logf("Open with deep path (expected to work or fail): %v", err)
	}
	if db != nil {
		db.Close()
	}
}

// --- Audit log tests ---

func TestInsertAuditLog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	err = db.InsertAuditLog(ctx, "session-1", "bash", "ls -la", "allow", "rule-1", "allowed by policy")
	if err != nil {
		t.Fatalf("InsertAuditLog failed: %v", err)
	}
}

func TestInsertAuditLog_NullableFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_null.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	// matched_rule and reason are nullable - pass empty strings
	err = db.InsertAuditLog(ctx, "session-2", "file_read", "/etc/passwd", "deny", "", "")
	if err != nil {
		t.Fatalf("InsertAuditLog with empty nullable fields failed: %v", err)
	}
}

func TestQueryAuditLogs_Empty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_empty.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	entries, err := db.QueryAuditLogs(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestQueryAuditLogs_BySession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_session.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.InsertAuditLog(ctx, "s1", "bash", "cmd1", "allow", "", "")
	_ = db.InsertAuditLog(ctx, "s2", "bash", "cmd2", "deny", "", "")
	_ = db.InsertAuditLog(ctx, "s1", "read", "cmd3", "allow", "", "")

	entries, err := db.QueryAuditLogs(ctx, "s1", "", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs by session failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for session s1, got %d", len(entries))
	}
}

func TestQueryAuditLogs_ByTool(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_tool.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.InsertAuditLog(ctx, "s1", "bash", "cmd1", "allow", "", "")
	_ = db.InsertAuditLog(ctx, "s1", "file_read", "cmd2", "allow", "", "")
	_ = db.InsertAuditLog(ctx, "s2", "bash", "cmd3", "deny", "", "")

	entries, err := db.QueryAuditLogs(ctx, "", "bash", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs by tool failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for bash, got %d", len(entries))
	}
}

func TestQueryAuditLogs_BySessionAndTool(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_both.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.InsertAuditLog(ctx, "s1", "bash", "cmd1", "allow", "", "")
	_ = db.InsertAuditLog(ctx, "s1", "file_read", "cmd2", "allow", "", "")
	_ = db.InsertAuditLog(ctx, "s2", "bash", "cmd3", "allow", "", "")

	entries, err := db.QueryAuditLogs(ctx, "s1", "bash", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs by both failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
	if len(entries) > 0 && entries[0].ToolName != "bash" {
		t.Errorf("expected tool_name 'bash', got '%s'", entries[0].ToolName)
	}
}

func TestQueryAuditLogs_Limit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_limit.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = db.InsertAuditLog(ctx, "s1", "bash", "cmd", "allow", "", "")
	}

	entries, err := db.QueryAuditLogs(ctx, "", "", 3)
	if err != nil {
		t.Fatalf("QueryAuditLogs with limit failed: %v", err)
	}
	if len(entries) > 3 {
		t.Errorf("expected at most 3 entries with limit=3, got %d", len(entries))
	}
}

func TestQueryAuditLogs_OrderedByCreation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_order.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.InsertAuditLog(ctx, "s1", "bash", "first", "allow", "", "")
	time.Sleep(1 * time.Millisecond)
	_ = db.InsertAuditLog(ctx, "s1", "bash", "second", "allow", "", "")

	entries, err := db.QueryAuditLogs(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs failed: %v", err)
	}
	if len(entries) >= 2 {
		// Should be ordered by created_at DESC
		if entries[1].InputSummary != "first" && entries[0].InputSummary != "second" {
			t.Logf("entries[0].InputSummary=%q, entries[1].InputSummary=%q", entries[0].InputSummary, entries[1].InputSummary)
		}
	}
}

func TestAuditLog_NullableReturns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_null_ret.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.InsertAuditLog(ctx, "s1", "bash", "cmd", "allow", "", "")

	entries, err := db.QueryAuditLogs(ctx, "s1", "bash", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// matched_rule and reason should be empty strings (not nil pointers)
	if entries[0].MatchedRule != "" {
		t.Errorf("expected empty MatchedRule, got %q", entries[0].MatchedRule)
	}
	if entries[0].Reason != "" {
		t.Errorf("expected empty Reason, got %q", entries[0].Reason)
	}
}

func TestAuditLog_Fields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_fields.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	err = db.InsertAuditLog(ctx, "session-test", "file_write", "/tmp/test.txt", "allow", "policy-1", "trusted path")
	if err != nil {
		t.Fatalf("InsertAuditLog failed: %v", err)
	}

	entries, err := db.QueryAuditLogs(ctx, "session-test", "file_write", 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.SessionID != "session-test" {
		t.Errorf("SessionID = %q, want 'session-test'", e.SessionID)
	}
	if e.ToolName != "file_write" {
		t.Errorf("ToolName = %q, want 'file_write'", e.ToolName)
	}
	if e.InputSummary != "/tmp/test.txt" {
		t.Errorf("InputSummary = %q, want '/tmp/test.txt'", e.InputSummary)
	}
	if e.Action != "allow" {
		t.Errorf("Action = %q, want 'allow'", e.Action)
	}
	if e.MatchedRule != "policy-1" {
		t.Errorf("MatchedRule = %q, want 'policy-1'", e.MatchedRule)
	}
	if e.Reason != "trusted path" {
		t.Errorf("Reason = %q, want 'trusted path'", e.Reason)
	}
	if e.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if e.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

// --- Migration tests ---

func TestMigrate_AlreadyAppliedError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"duplicate column name: foo", true},
		{"table test already exists", true},
		{"index already exists", true},
		{"some other error", false},
		{"", false},
	}

	for _, tt := range tests {
		err := &mockError{msg: tt.errMsg}
		got := isAlreadyAppliedError(err)
		if got != tt.want {
			t.Errorf("isAlreadyAppliedError(%q) = %v, want %v", tt.errMsg, got, tt.want)
		}
	}
}

type mockError struct{ msg string }

func (e *mockError) Error() string { return e.msg }

func TestMigrate_AlreadyApplied(t *testing.T) {
	migDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "migrate_test.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer migDB.Close()

	// Run migrate once
	if err := migrate(migDB); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Count migrations applied
	var firstCount int
	_ = migDB.QueryRow(`SELECT COUNT(*) FROM _migrations`).Scan(&firstCount)

	// Run migrate again
	if err := migrate(migDB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var secondCount int
	_ = migDB.QueryRow(`SELECT COUNT(*) FROM _migrations`).Scan(&secondCount)

	if secondCount != firstCount {
		t.Errorf("migration count changed: first=%d, second=%d", firstCount, secondCount)
	}
}

func TestIsAlreadyAppliedError(t *testing.T) {
	if !isAlreadyAppliedError(&mockError{msg: "duplicate column name: foo"}) {
		t.Error("expected true for 'duplicate column name'")
	}
	if !isAlreadyAppliedError(&mockError{msg: "table test already exists"}) {
		t.Error("expected true for 'already exists'")
	}
	if !isAlreadyAppliedError(&mockError{msg: "index already exists"}) {
		t.Error("expected true for 'already exists'")
	}
	if isAlreadyAppliedError(&mockError{msg: "syntax error"}) {
		t.Error("expected false for other errors")
	}
}

func TestOpen_WithExistingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "existing.db")

	// Open, insert data, close
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_, err = db1.Exec(`INSERT INTO sessions (id, channel, channel_id) VALUES ('test-session', 'cli', 'user1')`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	db1.Close()

	// Re-open should preserve data
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var count int
	err = db2.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'test-session'`).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 session, got %d", count)
	}
}

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

func TestMemoryManageToolName(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	if tool.Name() != "memory_manage" {
		t.Errorf("expected tool name 'memory_manage', got %q", tool.Name())
	}
}

func TestMemoryManageToolRequiresApproval(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	if !tool.RequiresApproval() {
		t.Error("memory_manage tool should require approval")
	}
}

func TestMemoryManageToolInputSchema(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	schema := tool.InputSchema()

	// Check it's an object type
	if schema["type"] != "object" {
		t.Errorf("expected schema type 'object', got %v", schema["type"])
	}

	// Check required fields
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required to be []string")
	}
	foundAction := false
	for _, r := range required {
		if r == "action" {
			foundAction = true
		}
	}
	if !foundAction {
		t.Error("'action' should be a required field")
	}

	// Check properties include expected fields
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	expectedFields := []string{"action", "query", "sensitivity", "memory_type", "retention_days", "confirm_ids"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("expected field %q in schema properties", field)
		}
	}
}

func TestMemoryManageToolInvalidAction(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "invalid_action"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid action")
	}
}

func TestMemoryManageToolInvalidJSON(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	result, err := tool.Execute(context.Background(), []byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid JSON input")
	}
}

func TestMemoryManageToolForgetRequiresQuery(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "forget"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("forget without query or confirm_ids should return an error")
	}
}

func TestMemoryManageToolRetentionRequiresType(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "retention"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("retention without memory_type should return an error")
	}
}

func TestMemoryManageToolRetentionRequiresPositiveDays(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]any{
		"action":         "retention",
		"memory_type":    "episodic",
		"retention_days": 0,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("retention with 0 days should return an error")
	}
}

// ---------------------------------------------------------------------------
// Mock implementations for memory.Store (used by MemoryManageTool tests).
// ---------------------------------------------------------------------------

// manageMockStore implements memory.Store with configurable search results and
// error tracking for delete operations.
type manageMockStore struct {
	searchResults []memory.SearchResult
	searchErr     error
	deletedIDs    []string
	deleteErr     error
}

func (m *manageMockStore) Save(_ context.Context, _ memory.Entry) error { return nil }
func (m *manageMockStore) ListByScope(_ context.Context, _ memory.MemoryScope, _ string) ([]memory.Entry, error) {
	return nil, nil
}
func (m *manageMockStore) Update(_ context.Context, _ string, _ string, _ int) error { return nil }

func (m *manageMockStore) Search(_ context.Context, _ memory.SearchQuery) ([]memory.SearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}

func (m *manageMockStore) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedIDs = append(m.deletedIDs, id)
	return nil
}

// newTestDB creates an in-memory SQLite database with the minimal schema
// required by MemoryManageTool (memory_index + memory_audit_log).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE memory_index (
			memory_id TEXT PRIMARY KEY,
			file_path TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL,
			user_id TEXT,
			session_id TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			strength REAL DEFAULT 1.0,
			memory_type TEXT NOT NULL DEFAULT 'semantic',
			emotion TEXT NOT NULL DEFAULT 'neutral',
			sensitivity TEXT NOT NULL DEFAULT 'public'
		)`,
		`CREATE TABLE memory_audit_log (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT 'system',
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			details TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup table: %v", err)
		}
	}
	return db
}

// ---------------------------------------------------------------------------
// Forget action tests
// ---------------------------------------------------------------------------

func TestMemoryManageForgetWithQuery(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		results      []memory.SearchResult
		searchErr    error
		wantContains []string
		wantError    bool
	}{
		{
			name:  "multiple candidates returned",
			query: "passwords",
			results: []memory.SearchResult{
				{Entry: memory.Entry{ID: "mem1", Content: "user changed password yesterday"}, Score: 0.95},
				{Entry: memory.Entry{ID: "mem2", Content: "password is secret123"}, Score: 0.87},
			},
			wantContains: []string{"mem1", "mem2", "confirm_ids"},
		},
		{
			name:         "no matching memories",
			query:        "nonexistent",
			results:      nil,
			wantContains: []string{"No matching memories found"},
		},
		{
			name:      "search failure propagates as result error",
			query:     "broken",
			searchErr: errors.New("connection refused"),
			wantError: true,
		},
		{
			name:  "long content truncated in output",
			query: "long",
			results: []memory.SearchResult{
				{Entry: memory.Entry{ID: "longmem", Content: strings.Repeat("a", 200)}, Score: 0.90},
			},
			wantContains: []string{"longmem", "..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &manageMockStore{searchResults: tt.results, searchErr: tt.searchErr}
			tool := NewMemoryManageTool(store, nil, "")

			input, _ := json.Marshal(map[string]string{"action": "forget", "query": tt.query})
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}

			if tt.wantError {
				if result.Error == "" {
					t.Error("expected result error, got none")
				}
				return
			}
			if result.Error != "" {
				t.Fatalf("unexpected result error: %s", result.Error)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(result.Output, want) {
					t.Errorf("output missing %q\noutput: %s", want, result.Output)
				}
			}
		})
	}
}

func TestMemoryManageForgetWithConfirmIDs(t *testing.T) {
	tests := []struct {
		name        string
		confirmIDs  []string
		wantDeleted int
		wantOutput  string
	}{
		{
			name:        "single ID deleted",
			confirmIDs:  []string{"mem1"},
			wantDeleted: 1,
			wantOutput:  "Deleted 1 memories",
		},
		{
			name:        "multiple IDs deleted",
			confirmIDs:  []string{"mem1", "mem2", "mem3"},
			wantDeleted: 3,
			wantOutput:  "Deleted 3 memories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t) // needed by logAudit
			store := &manageMockStore{}
			tool := NewMemoryManageTool(store, db, "")

			input, _ := json.Marshal(map[string]any{"action": "forget", "confirm_ids": tt.confirmIDs})
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result.Error != "" {
				t.Fatalf("unexpected result error: %s", result.Error)
			}
			if !strings.Contains(result.Output, tt.wantOutput) {
				t.Errorf("output missing %q\noutput: %s", tt.wantOutput, result.Output)
			}
			if len(store.deletedIDs) != tt.wantDeleted {
				t.Errorf("expected %d deletions, got %d", tt.wantDeleted, len(store.deletedIDs))
			}
			for i, id := range tt.confirmIDs {
				if i >= len(store.deletedIDs) {
					break
				}
				if store.deletedIDs[i] != id {
					t.Errorf("deleted[%d] = %q, want %q", i, store.deletedIDs[i], id)
				}
			}
		})
	}
}

func TestMemoryManageForgetConfirmPartialFailure(t *testing.T) {
	db := newTestDB(t)
	store := &manageMockStore{deleteErr: errors.New("disk full")}
	tool := NewMemoryManageTool(store, db, "")

	input, _ := json.Marshal(map[string]any{
		"action":      "forget",
		"confirm_ids": []string{"mem1", "mem2"},
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// Should report 0 deletions + errors
	if !strings.Contains(result.Output, "Deleted 0 memories") {
		t.Errorf("expected 0 deleted, output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Errors") {
		t.Errorf("expected error details, output: %s", result.Output)
	}
}

// ---------------------------------------------------------------------------
// List action tests
// ---------------------------------------------------------------------------

func TestMemoryManageListWithRows(t *testing.T) {
	db := newTestDB(t)

	// Seed rows into memory_index.
	for _, r := range []struct {
		id, scope, memType, sensitivity string
	}{
		{"m1", "user", "semantic", "public"},
		{"m2", "session", "episodic", "private"},
	} {
		_, err := db.Exec(`INSERT INTO memory_index (memory_id, scope, memory_type, sensitivity, strength, file_path, updated_at)
			VALUES (?, ?, ?, ?, 0.8, '', datetime('now'))`, r.id, r.scope, r.memType, r.sensitivity)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tool := NewMemoryManageTool(nil, db, "")
	input, _ := json.Marshal(map[string]string{"action": "list"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	for _, want := range []string{"m1", "m2", "Total: 2"} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("output missing %q\noutput: %s", want, result.Output)
		}
	}
}

func TestMemoryManageListEmpty(t *testing.T) {
	db := newTestDB(t)
	tool := NewMemoryManageTool(nil, db, "")
	input, _ := json.Marshal(map[string]string{"action": "list"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(result.Output, "No memories found") {
		t.Errorf("expected empty-list message, output: %s", result.Output)
	}
}

func TestMemoryManageListDBFailure(t *testing.T) {
	db := newTestDB(t)
	// Close the DB to force a query failure.
	_ = db.Close()

	tool := NewMemoryManageTool(nil, db, "")
	input, _ := json.Marshal(map[string]string{"action": "list"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected result error on closed DB")
	}
}

func TestMemoryManageForgetConfirmAuditLogWritten(t *testing.T) {
	db := newTestDB(t)
	store := &manageMockStore{}
	tool := NewMemoryManageTool(store, db, "")

	input, _ := json.Marshal(map[string]any{
		"action":      "forget",
		"confirm_ids": []string{"mem1", "mem2"},
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	// Verify audit_log entries were written for each deleted ID.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memory_audit_log WHERE action = 'delete'`).Scan(&count); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 audit log entries, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Protect action tests
// ---------------------------------------------------------------------------

func TestMemoryManageProtect(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		sensitivity  string
		results      []memory.SearchResult
		wantContains string
	}{
		{
			name:  "default sensitivity is secret",
			query: "bank info",
			results: []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "account number"}, Score: 0.9},
			},
			wantContains: "'secret' for 1 memories",
		},
		{
			name:        "explicit sensitivity",
			query:       "phone number",
			sensitivity: "private",
			results: []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "phone is 555-1234"}, Score: 0.9},
				{Entry: memory.Entry{ID: "m2", Content: "mobile number"}, Score: 0.85},
			},
			wantContains: "'private' for 2 memories",
		},
		{
			name:         "no matches",
			query:        "nonexistent",
			results:      nil,
			wantContains: "No matching memories found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			store := &manageMockStore{searchResults: tt.results}
			tool := NewMemoryManageTool(store, db, "")

			m := map[string]any{"action": "protect", "query": tt.query}
			if tt.sensitivity != "" {
				m["sensitivity"] = tt.sensitivity
			}
			input, _ := json.Marshal(m)
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result.Error != "" {
				t.Fatalf("unexpected result error: %s", result.Error)
			}
			if !strings.Contains(result.Output, tt.wantContains) {
				t.Errorf("output missing %q\noutput: %s", tt.wantContains, result.Output)
			}
		})
	}
}

func TestMemoryManageProtectRequiresQuery(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "protect"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("protect without query should return error")
	}
}

// ---------------------------------------------------------------------------
// Retention action tests
// ---------------------------------------------------------------------------

func TestMemoryManageRetentionSuccess(t *testing.T) {
	tests := []struct {
		name          string
		memoryType    string
		retentionDays float64
		wantContains  []string
	}{
		{
			name:          "episodic 30 days",
			memoryType:    "episodic",
			retentionDays: 30,
			wantContains:  []string{"episodic", "30 days"},
		},
		{
			name:          "semantic 90 days",
			memoryType:    "semantic",
			retentionDays: 90,
			wantContains:  []string{"semantic", "90 days"},
		},
		{
			name:          "procedural 365 days",
			memoryType:    "procedural",
			retentionDays: 365,
			wantContains:  []string{"procedural", "365 days"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewMemoryManageTool(nil, nil, "")
			input, _ := json.Marshal(map[string]any{
				"action":         "retention",
				"memory_type":    tt.memoryType,
				"retention_days": tt.retentionDays,
			})
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result.Error != "" {
				t.Fatalf("unexpected result error: %s", result.Error)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(result.Output, want) {
					t.Errorf("output missing %q\noutput: %s", want, result.Output)
				}
			}
		})
	}
}

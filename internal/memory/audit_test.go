package memory

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestAuditLoggerNilDB(t *testing.T) {
	al := NewAuditLogger(nil)
	// Should not panic
	al.Log(context.Background(), "mem1", "ADD", "lifecycle", "test content")
}

func TestAuditLoggerWritesToDB(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create the audit table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_audit_log (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			details TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	al := NewAuditLogger(db)
	al.Log(context.Background(), "fact_123", "ADD", "lifecycle", "user prefers dark mode")

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM memory_audit_log").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit row, got %d", count)
	}

	var memoryID, action, actor, details string
	err = db.QueryRow("SELECT memory_id, action, actor, details FROM memory_audit_log LIMIT 1").
		Scan(&memoryID, &action, &actor, &details)
	if err != nil {
		t.Fatal(err)
	}
	if memoryID != "fact_123" {
		t.Errorf("expected memory_id=fact_123, got %s", memoryID)
	}
	if action != "ADD" {
		t.Errorf("expected action=ADD, got %s", action)
	}
	if actor != "lifecycle" {
		t.Errorf("expected actor=lifecycle, got %s", actor)
	}
	if details != "user prefers dark mode" {
		t.Errorf("expected details='user prefers dark mode', got %s", details)
	}
}

func TestAuditLoggerMultipleEntries(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_audit_log (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			timestamp DATETIME NOT NULL,
			details TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	al := NewAuditLogger(db)
	al.Log(context.Background(), "fact_1", "ADD", "lifecycle", "first")
	al.Log(context.Background(), "fact_2", "UPDATE", "lifecycle", "second")
	al.Log(context.Background(), "fact_3", "DELETE", "lifecycle", "third")

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM memory_audit_log").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 audit rows, got %d", count)
	}
}

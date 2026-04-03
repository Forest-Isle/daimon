package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTruncateAtLineBoundary(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxChars int
		expected string
	}{
		{
			name:     "within limit",
			input:    "hello\nworld",
			maxChars: 20,
			expected: "hello\nworld",
		},
		{
			name:     "truncate at line boundary",
			input:    "line1\nline2\nline3\nline4",
			maxChars: 12,
			expected: "line1\nline2",
		},
		{
			name:     "no newline in range",
			input:    "a very long line without newlines at all",
			maxChars: 10,
			expected: "a very lon",
		},
		{
			name:     "exact boundary",
			input:    "abc\ndef",
			maxChars: 3,
			expected: "abc",
		},
		{
			name:     "empty string",
			input:    "",
			maxChars: 10,
			expected: "",
		},
		{
			name:     "single newline at start",
			input:    "\nlong content here that exceeds",
			maxChars: 10,
			expected: "\nlong cont",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateAtLineBoundary(tt.input, tt.maxChars)
			if got != tt.expected {
				t.Errorf("TruncateAtLineBoundary() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResultStoreLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	rs := NewResultStore(tmpDir, 100, 50, 1)

	// Small output — should not be persisted
	if rs.ShouldPersist("small output") {
		t.Error("small output should not need persistence")
	}

	// Large output — should be persisted
	largeOutput := ""
	for i := 0; i < 20; i++ {
		largeOutput += "line of text that makes the output quite large\n"
	}

	if !rs.ShouldPersist(largeOutput) {
		t.Error("large output should need persistence")
	}

	// Store
	stored, err := rs.Store("session1", "tool_use_1", largeOutput)
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	if stored.FullSize != len(largeOutput) {
		t.Errorf("FullSize = %d, want %d", stored.FullSize, len(largeOutput))
	}

	if len(stored.Preview) >= len(largeOutput) {
		t.Error("preview should be shorter than full output")
	}

	// Load
	loaded, err := rs.Load(stored.DiskPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded != largeOutput {
		t.Error("loaded output should match original")
	}

	// Verify file exists
	if _, err := os.Stat(stored.DiskPath); os.IsNotExist(err) {
		t.Error("persisted file should exist")
	}
}

func TestResultStoreCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	rs := NewResultStore(tmpDir, 100, 50, 1) // 1 hour TTL

	// Create a session dir with an old file
	sessionDir := filepath.Join(tmpDir, "old_session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(sessionDir, "old_result.txt")
	if err := os.WriteFile(oldFile, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set file modification time to 2 hours ago
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Create a recent file
	recentDir := filepath.Join(tmpDir, "recent_session")
	if err := os.MkdirAll(recentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recentFile := filepath.Join(recentDir, "recent_result.txt")
	if err := os.WriteFile(recentFile, []byte("recent content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Cleanup
	err := rs.Cleanup()
	if err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	// Old file should be removed
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should be removed after cleanup")
	}

	// Recent file should still exist
	if _, err := os.Stat(recentFile); os.IsNotExist(err) {
		t.Error("recent file should still exist after cleanup")
	}
}

func TestResultStoreErrorNotPersisted(t *testing.T) {
	rs := NewResultStore(t.TempDir(), 100, 50, 24)

	// Error strings are typically short, so they should not be persisted
	errorOutput := "error: file not found"
	if rs.ShouldPersist(errorOutput) {
		t.Error("short error output should not be persisted")
	}
}

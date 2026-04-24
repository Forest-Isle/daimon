package evolution

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupTrajectories(t *testing.T) {
	dir := t.TempDir()

	// Create some trajectory files with date-stamped names.
	oldDate := time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02")
	recentDate := time.Now().Add(-1 * 24 * time.Hour).Format("2006-01-02")

	oldFile := filepath.Join(dir, oldDate+".jsonl")
	recentFile := filepath.Join(dir, recentDate+".jsonl")

	if err := os.WriteFile(oldFile, []byte(`{"test":"old"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentFile, []byte(`{"test":"recent"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Retain only 7 days.
	removed, err := CleanupTrajectories(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Old file should be gone, recent should remain.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should have been removed")
	}
	if _, err := os.Stat(recentFile); err != nil {
		t.Error("recent file should still exist")
	}
}

func TestCleanupTrajectories_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	removed, err := CleanupTrajectories(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestCleanupTrajectories_NonExistentDir(t *testing.T) {
	removed, err := CleanupTrajectories("/tmp/nonexistent-ironclaw-test-dir-xyz", 7*24*time.Hour)
	if err != nil {
		t.Fatal("should not error on non-existent dir")
	}
	if removed != 0 {
		t.Errorf("expected 0, got %d", removed)
	}
}

func TestCleanupTrajectories_SkipsNonJsonl(t *testing.T) {
	dir := t.TempDir()

	oldDate := time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02")
	txtFile := filepath.Join(dir, oldDate+".txt")
	if err := os.WriteFile(txtFile, []byte("not jsonl"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := CleanupTrajectories(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed (non-jsonl), got %d", removed)
	}
}

func TestCompactTrajectories(t *testing.T) {
	dir := t.TempDir()

	oldDate := time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02")
	oldFile := filepath.Join(dir, oldDate+".jsonl")
	if err := os.WriteFile(oldFile, []byte(`{"test":"data"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CompactTrajectories(dir, 7)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should have been removed by CompactTrajectories")
	}
}

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileListDirectory(t *testing.T) {
	dir := t.TempDir()

	// Create some files and a subdir
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewFileListTool()
	input, _ := json.Marshal(map[string]any{"path": dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if result.Type != ResultText {
		t.Errorf("expected Type=ResultText, got %q", result.Type)
	}

	// Files should have "  " prefix, dirs should have "d " prefix
	if !strings.Contains(result.Output, "  file1.txt") {
		t.Errorf("expected '  file1.txt' in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "  file2.txt") {
		t.Errorf("expected '  file2.txt' in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "d subdir") {
		t.Errorf("expected 'd subdir' in output, got:\n%s", result.Output)
	}
}

func TestFileListNonexistent(t *testing.T) {
	tool := NewFileListTool()
	input, _ := json.Marshal(map[string]any{"path": "/nonexistent/directory/path"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error in Result for nonexistent directory")
	}
}

func TestFileListEmptyDir(t *testing.T) {
	dir := t.TempDir()

	tool := NewFileListTool()
	input, _ := json.Marshal(map[string]any{"path": dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if result.Output != "" {
		t.Errorf("expected empty output for empty dir, got: %q", result.Output)
	}
}

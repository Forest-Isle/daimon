package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileReadBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool()
	input, _ := json.Marshal(map[string]any{"path": path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if result.FilePath != path {
		t.Errorf("expected FilePath=%q, got %q", path, result.FilePath)
	}
	if result.Type != ResultFile {
		t.Errorf("expected Type=ResultFile, got %q", result.Type)
	}
	// Verify line numbers appear in output
	if !strings.Contains(result.Output, "1\t") {
		t.Errorf("expected line number 1 in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "line one") {
		t.Errorf("expected 'line one' in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "3\t") {
		t.Errorf("expected line number 3 in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "line three") {
		t.Errorf("expected 'line three' in output, got:\n%s", result.Output)
	}
}

func TestFileReadWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")

	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool()
	input, _ := json.Marshal(map[string]any{"path": path, "offset": 3, "limit": 3})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	// Should have lines 3, 4, 5 only
	if !strings.Contains(result.Output, "line 3") {
		t.Errorf("expected 'line 3' in output, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "line 5") {
		t.Errorf("expected 'line 5' in output, got:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "line 6") {
		t.Errorf("did not expect 'line 6' in output, got:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "line 2") {
		t.Errorf("did not expect 'line 2' in output, got:\n%s", result.Output)
	}
	// Line numbers should start at 3
	if !strings.Contains(result.Output, "3\t") {
		t.Errorf("expected line number 3 in output, got:\n%s", result.Output)
	}
}

func TestFileReadNonexistent(t *testing.T) {
	tool := NewFileReadTool()
	input, _ := json.Marshal(map[string]any{"path": "/nonexistent/path/file.txt"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error in Result for nonexistent file")
	}
}

func TestFileReadLargeFileTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")

	// Generate file larger than 64KB
	var sb strings.Builder
	line := strings.Repeat("x", 200) + "\n"
	for sb.Len() < maxOutputSize+1000 {
		sb.WriteString(line)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool()
	input, _ := json.Marshal(map[string]any{"path": path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !result.IsPartial {
		t.Error("expected IsPartial=true for large file")
	}
	if !strings.Contains(result.Output, "[truncated]") {
		t.Error("expected '[truncated]' marker in output")
	}
	if len(result.Output) > maxOutputSize+len("\n[truncated]")+10 {
		t.Errorf("output too large: %d bytes", len(result.Output))
	}
}

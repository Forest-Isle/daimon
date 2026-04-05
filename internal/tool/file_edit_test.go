package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileEditSingleMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("foo bar baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(false)
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "bar",
		"new_string": "qux",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "1 replacement(s) made") {
		t.Errorf("unexpected output: %q", result.Output)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foo qux baz\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestFileEditNoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(false)
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "nonexistent",
		"new_string": "replacement",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for no match")
	}
	if !strings.Contains(result.Error, "not found") {
		t.Errorf("unexpected error message: %q", result.Error)
	}
}

func TestFileEditMultipleMatchError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("foo foo foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(false)
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "bar",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for multiple matches without replace_all")
	}
	if !strings.Contains(result.Error, "3 times") {
		t.Errorf("unexpected error message: %q", result.Error)
	}
	if !strings.Contains(result.Error, "replace_all") {
		t.Errorf("expected replace_all hint in error: %q", result.Error)
	}
}

func TestFileEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("foo foo foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(false)
	input, _ := json.Marshal(map[string]any{
		"path":        path,
		"old_string":  "foo",
		"new_string":  "bar",
		"replace_all": true,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "3 replacement(s) made") {
		t.Errorf("unexpected output: %q", result.Output)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bar bar bar\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestFileEditIdenticalStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(false)
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "hello",
		"new_string": "hello",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for identical old_string and new_string")
	}
	if !strings.Contains(result.Error, "identical") {
		t.Errorf("unexpected error message: %q", result.Error)
	}
}

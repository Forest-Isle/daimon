package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilePatchSingleHunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path": path,
		"patch": strings.Join([]string{
			"@@ -1,3 +1,3 @@",
			" one",
			"-two",
			"+TWO",
			" three",
		}, "\n"),
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "one\nTWO\nthree\n" {
		t.Fatalf("unexpected content: %q", got)
	}
	if result.Metadata["success"] != true {
		t.Fatalf("expected success metadata, got %#v", result.Metadata["success"])
	}
}

func TestFilePatchMultiHunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path": path,
		"patch": strings.Join([]string{
			"@@ -1,2 +1,2 @@",
			" a",
			"-b",
			"+B",
			"@@ -4,2 +4,2 @@",
			" d",
			"-e",
			"+E",
		}, "\n"),
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "a\nB\nc\nd\nE\n" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestFilePatchAddAtEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path": path,
		"patch": strings.Join([]string{
			"@@ -1,2 +1,4 @@",
			" alpha",
			" beta",
			"+gamma",
			"+delta",
		}, "\n"),
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "alpha\nbeta\ngamma\ndelta\n" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestFilePatchFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("left\nright\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path": path,
		"patch": strings.Join([]string{
			"@@ -1,2 +1,2 @@",
			" left",
			"-missing",
			"+present",
		}, "\n"),
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected patch failure")
	}
	if !strings.Contains(result.Error, "failed to apply hunk") {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "left\nright\n" {
		t.Fatalf("file should be unchanged, got %q", got)
	}
}

func TestFilePatchDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path":    path,
		"dry_run": true,
		"patch": strings.Join([]string{
			"@@ -1,2 +1,2 @@",
			" foo",
			"-bar",
			"+baz",
		}, "\n"),
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "dry run") {
		t.Fatalf("unexpected output: %q", result.Output)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "foo\nbar\n" {
		t.Fatalf("dry run should not change file, got %q", got)
	}
}

func TestFilePatchEmptyPatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilePatchTool(dir)
	input, _ := json.Marshal(map[string]any{
		"path":  path,
		"patch": "",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if result.Output != "no changes applied" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepCode(t *testing.T) {
	t.Run("basic pattern match", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main.go"), "package main\nfunc target() {}\n")

		tool := NewGrepCodeTool(dir)
		input := mustJSON(t, map[string]any{"pattern": "target"})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "main.go:2:func target() {}") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
		if got := result.Metadata["match_count"]; got != 1 {
			t.Fatalf("match_count = %v, want 1", got)
		}
	})

	t.Run("file type filtering", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main.go"), "package main\nfunc target() {}\n")
		writeTestFile(t, filepath.Join(dir, "notes.txt"), "target in text\n")

		tool := NewGrepCodeTool(dir)
		input := mustJSON(t, map[string]any{
			"pattern": "target",
			"include": "*.go",
		})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if strings.Contains(result.Output, "notes.txt") {
			t.Fatalf("unexpected filtered match in output: %q", result.Output)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Fatalf("expected go match in output: %q", result.Output)
		}
	})

	t.Run("max results capping", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "many.go"), "target1\ntarget2\ntarget3\n")

		tool := NewGrepCodeTool(dir)
		input := mustJSON(t, map[string]any{
			"pattern":     "target",
			"max_results": 2,
		})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}

		lines := splitNonEmptyLines(result.Output)
		if len(lines) != 2 {
			t.Fatalf("returned lines = %d, want 2; output=%q", len(lines), result.Output)
		}
		if got := result.Metadata["match_count"]; got != 3 {
			t.Fatalf("match_count = %v, want 3", got)
		}
		if got := result.Metadata["returned_count"]; got != 2 {
			t.Fatalf("returned_count = %v, want 2", got)
		}
	})

	t.Run("rejects path outside workdir", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir()
		writeTestFile(t, filepath.Join(outside, "secret.go"), "package secret\n")

		tool := NewGrepCodeTool(dir)
		result, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
			"pattern": "secret",
			"path":    outside,
		}))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error == "" {
			t.Fatalf("expected path fence error, got %+v", result)
		}
	})
}

func TestFindSymbol(t *testing.T) {
	t.Run("go function definition", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main.go"), "package main\nfunc LoadConfig() error { return nil }\n")

		tool := NewFindSymbolTool(dir)
		input := mustJSON(t, map[string]any{
			"name":    "LoadConfig",
			"include": "*.go",
			"kind":    "function",
		})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "function ") || !strings.Contains(result.Output, "func LoadConfig() error") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
	})

	t.Run("go type definition", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "types.go"), "package main\ntype Config struct{}\n")

		tool := NewFindSymbolTool(dir)
		input := mustJSON(t, map[string]any{
			"name":    "Config",
			"include": "*.go",
			"kind":    "type",
		})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "type ") || !strings.Contains(result.Output, "type Config struct{}") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
	})

	t.Run("fallback pattern", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "config.txt"), "service_name = primary\n")

		tool := NewFindSymbolTool(dir)
		input := mustJSON(t, map[string]any{
			"name":    "service_name",
			"include": "*.txt",
		})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "config.txt:1:service_name = primary") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
	})
}

func TestListImports(t *testing.T) {
	t.Run("go imports", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n\nimport (\n\t\"fmt\"\n\talias \"strings\"\n)\n")

		tool := NewListImportsTool(dir)
		input := mustJSON(t, map[string]any{"file_path": "main.go"})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "4:import:fmt") || !strings.Contains(result.Output, "5:import:strings") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
	})

	t.Run("python imports", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "app.py"), "import os, sys\nfrom pkg.sub import thing\n")

		tool := NewListImportsTool(dir)
		input := mustJSON(t, map[string]any{"file_path": "app.py"})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error != "" {
			t.Fatalf("Execute() result.Error = %q", result.Error)
		}
		if !strings.Contains(result.Output, "1:import:os") || !strings.Contains(result.Output, "1:import:sys") || !strings.Contains(result.Output, "2:from:pkg.sub") {
			t.Fatalf("unexpected output: %q", result.Output)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		tool := NewListImportsTool(dir)
		input := mustJSON(t, map[string]any{"file_path": "missing.go"})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error == "" {
			t.Fatalf("expected missing file error, got %+v", result)
		}
	})

	t.Run("rejects relative escape", func(t *testing.T) {
		dir := t.TempDir()
		tool := NewListImportsTool(dir)
		input := mustJSON(t, map[string]any{"file_path": "../outside.go"})

		result, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Error == "" {
			t.Fatalf("expected path fence error, got %+v", result)
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

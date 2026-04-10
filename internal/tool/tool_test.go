package tool

import (
	"context"
	"testing"
)

// mockReadOnlyTool implements both Tool and ReadOnlyTool.
type mockReadOnlyTool struct {
	readOnly bool
}

func (m *mockReadOnlyTool) Name() string                                        { return "mock_readonly" }
func (m *mockReadOnlyTool) Description() string                                 { return "mock" }
func (m *mockReadOnlyTool) InputSchema() map[string]any                         { return nil }
func (m *mockReadOnlyTool) Execute(_ context.Context, _ []byte) (Result, error) { return Result{}, nil }
func (m *mockReadOnlyTool) RequiresApproval() bool                              { return false }
func (m *mockReadOnlyTool) IsReadOnly() bool                                    { return m.readOnly }

// mockBasicTool implements only Tool (no ReadOnlyTool).
type mockBasicTool struct{}

func (m *mockBasicTool) Name() string                                        { return "mock_basic" }
func (m *mockBasicTool) Description() string                                 { return "mock" }
func (m *mockBasicTool) InputSchema() map[string]any                         { return nil }
func (m *mockBasicTool) Execute(_ context.Context, _ []byte) (Result, error) { return Result{}, nil }
func (m *mockBasicTool) RequiresApproval() bool                              { return false }

func TestIsToolReadOnly(t *testing.T) {
	tests := []struct {
		name     string
		tool     Tool
		expected bool
	}{
		{"read-only tool returning true", &mockReadOnlyTool{readOnly: true}, true},
		{"read-only tool returning false", &mockReadOnlyTool{readOnly: false}, false},
		{"basic tool without ReadOnlyTool interface", &mockBasicTool{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsToolReadOnly(tt.tool)
			if got != tt.expected {
				t.Errorf("IsToolReadOnly() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResultDefaults(t *testing.T) {
	// Zero-value Result should behave like current behavior
	r := Result{Output: "hello", Error: ""}
	if r.Type != "" {
		t.Errorf("default Type should be empty string (zero value), got %q", r.Type)
	}
	if r.IsPartial {
		t.Error("default IsPartial should be false")
	}
	if r.Metadata != nil {
		t.Error("default Metadata should be nil")
	}
	if r.FilePath != "" {
		t.Error("default FilePath should be empty")
	}
}

func TestResultWithMetadata(t *testing.T) {
	r := Result{
		Output:   "data",
		Type:     ResultFile,
		FilePath: "/tmp/test.txt",
		Metadata: map[string]any{"size": 1024},
	}
	if r.Type != ResultFile {
		t.Errorf("Type = %q, want %q", r.Type, ResultFile)
	}
	if r.FilePath != "/tmp/test.txt" {
		t.Errorf("FilePath = %q, want /tmp/test.txt", r.FilePath)
	}
	if r.Metadata["size"] != 1024 {
		t.Errorf("Metadata[size] = %v, want 1024", r.Metadata["size"])
	}
}

func TestGetCapabilities(t *testing.T) {
	// Tool with CapableTool interface
	bash := NewBashTool(0, false, NewPolicy(nil))
	caps := GetCapabilities(bash)
	if !caps.IsDestructive {
		t.Error("BashTool should be destructive")
	}
	if caps.IsReadOnly {
		t.Error("BashTool should not be read-only")
	}

	// Tool with CapableTool (browser)
	browser := NewBrowserTool()
	caps = GetCapabilities(browser)
	if !caps.IsReadOnly {
		t.Error("BrowserTool should be read-only")
	}

	// Tool without any optional interface
	basic := &mockBasicTool{}
	caps = GetCapabilities(basic)
	if caps.IsReadOnly {
		t.Error("basic tool should not be read-only")
	}
	if caps.ApprovalMode != "auto" {
		t.Errorf("default ApprovalMode = %q, want auto", caps.ApprovalMode)
	}
}

func TestBuiltinToolsReadOnly(t *testing.T) {
	// BrowserTool should be read-only
	browser := NewBrowserTool()
	if !IsToolReadOnly(browser) {
		t.Error("BrowserTool should be read-only")
	}

	// BashTool should not be read-only
	bash := NewBashTool(0, false, NewPolicy(nil))
	if IsToolReadOnly(bash) {
		t.Error("BashTool should not be read-only")
	}

	// FileWriteTool should not be read-only
	file := NewFileWriteTool(false)
	if IsToolReadOnly(file) {
		t.Error("FileWriteTool should not be read-only")
	}

	// FileReadTool should be read-only
	fileRead := NewFileReadTool()
	if !IsToolReadOnly(fileRead) {
		t.Error("FileReadTool should be read-only")
	}

	// HTTPTool should not be read-only
	httpTool := NewHTTPTool(0, false)
	if IsToolReadOnly(httpTool) {
		t.Error("HTTPTool should not be read-only")
	}
}

func TestPathScopedToolImplementation(t *testing.T) {
	// file_write and file_edit should implement PathScopedTool
	writeT := NewFileWriteTool(false)
	editT := NewFileEditTool(false)

	// Verify they implement the interface
	var _ PathScopedTool = writeT
	var _ PathScopedTool = editT

	// Verify Capabilities return ParallelPathScoped
	if caps := GetCapabilities(writeT); caps.ParallelSafety != ParallelPathScoped {
		t.Errorf("FileWriteTool.ParallelSafety = %q, want %q", caps.ParallelSafety, ParallelPathScoped)
	}
	if caps := GetCapabilities(editT); caps.ParallelSafety != ParallelPathScoped {
		t.Errorf("FileEditTool.ParallelSafety = %q, want %q", caps.ParallelSafety, ParallelPathScoped)
	}

	// Verify ExtractPaths works correctly
	input := []byte(`{"path": "/tmp/test.go", "content": "hello"}`)
	paths, err := writeT.ExtractPaths(input)
	if err != nil {
		t.Fatalf("FileWriteTool.ExtractPaths error: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/tmp/test.go" {
		t.Errorf("FileWriteTool.ExtractPaths = %v, want [/tmp/test.go]", paths)
	}

	// Verify path canonicalization (relative → absolute)
	relInput := []byte(`{"path": "relative/file.go", "old_string": "a", "new_string": "b"}`)
	paths, err = editT.ExtractPaths(relInput)
	if err != nil {
		t.Fatalf("FileEditTool.ExtractPaths error: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	// Should be absolute
	if paths[0] == "relative/file.go" {
		t.Error("ExtractPaths should return canonical absolute path, got relative path")
	}

	// Empty path should return nil
	emptyInput := []byte(`{"path": ""}`)
	paths, err = writeT.ExtractPaths(emptyInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paths != nil {
		t.Errorf("expected nil for empty path, got %v", paths)
	}

	// Invalid JSON should return error
	_, err = writeT.ExtractPaths([]byte(`{invalid}`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReadOnlyToolsNotPathScoped(t *testing.T) {
	// file_read and file_list should remain ParallelSafe (read-only, no conflict possible)
	readT := NewFileReadTool()
	listT := NewFileListTool()

	if caps := GetCapabilities(readT); caps.ParallelSafety != ParallelSafe {
		t.Errorf("FileReadTool.ParallelSafety = %q, want %q", caps.ParallelSafety, ParallelSafe)
	}
	if caps := GetCapabilities(listT); caps.ParallelSafety != ParallelSafe {
		t.Errorf("FileListTool.ParallelSafety = %q, want %q", caps.ParallelSafety, ParallelSafe)
	}

	// They should NOT implement PathScopedTool
	if _, ok := Tool(readT).(PathScopedTool); ok {
		t.Error("FileReadTool should NOT implement PathScopedTool")
	}
	if _, ok := Tool(listT).(PathScopedTool); ok {
		t.Error("FileListTool should NOT implement PathScopedTool")
	}
}

func TestCanonicalizePath(t *testing.T) {
	// Empty string
	if got := CanonicalizePath(""); got != "" {
		t.Errorf("CanonicalizePath(\"\") = %q, want \"\"", got)
	}

	// Absolute path stays absolute
	if got := CanonicalizePath("/tmp/test.go"); got != "/tmp/test.go" {
		t.Errorf("CanonicalizePath(\"/tmp/test.go\") = %q, want \"/tmp/test.go\"", got)
	}

	// Dot-dot resolution
	got := CanonicalizePath("/tmp/foo/../test.go")
	if got != "/tmp/test.go" {
		t.Errorf("CanonicalizePath(\"/tmp/foo/../test.go\") = %q, want \"/tmp/test.go\"", got)
	}
}

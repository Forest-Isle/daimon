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

// mockAvailableTool implements Tool + AvailableTool.
type mockAvailableTool struct {
	available bool
	name      string
}

func (m *mockAvailableTool) Name() string                                        { return m.name }
func (m *mockAvailableTool) Description() string                                 { return "mock" }
func (m *mockAvailableTool) InputSchema() map[string]any                         { return nil }
func (m *mockAvailableTool) Execute(_ context.Context, _ []byte) (Result, error) { return Result{}, nil }
func (m *mockAvailableTool) RequiresApproval() bool                              { return false }
func (m *mockAvailableTool) Available() bool                                     { return m.available }

func TestIsToolAvailable(t *testing.T) {
	tests := []struct {
		name     string
		tool     Tool
		expected bool
	}{
		{"available tool", &mockAvailableTool{available: true, name: "a"}, true},
		{"unavailable tool", &mockAvailableTool{available: false, name: "b"}, false},
		{"tool without AvailableTool interface", &mockBasicTool{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsToolAvailable(tt.tool)
			if got != tt.expected {
				t.Errorf("IsToolAvailable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRegistryFiltersUnavailable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockAvailableTool{available: true, name: "tool_a"})
	reg.Register(&mockAvailableTool{available: false, name: "tool_b"})
	reg.Register(&mockBasicTool{})

	tools := reg.All()
	// tool_b should be excluded, tool_a and mock_basic should be present
	if len(tools) != 2 {
		t.Fatalf("expected 2 available tools, got %d", len(tools))
	}

	// Get should fail for unavailable tool
	_, err := reg.Get("tool_b")
	if err == nil {
		t.Error("expected error for unavailable tool")
	}

	// Get should succeed for available tool
	_, err = reg.Get("tool_a")
	if err != nil {
		t.Errorf("unexpected error for available tool: %v", err)
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

package hook

import (
	"context"
	"testing"
)

func TestTruncateInput(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 200, "short"},
		{"a very long input that exceeds the limit", 10, "a very lon..."},
		{"", 200, ""},
		{"exactly10!", 10, "exactly10!"},
	}
	for _, tt := range tests {
		got := TruncateInput(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("TruncateInput(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}

func TestPermissionAuditHandlerNilDB(t *testing.T) {
	h := NewPermissionAuditHandler(nil)
	result, err := h.OnPostToolUse(context.Background(), PostToolUseEvent{
		ToolName: "bash",
		Status:   "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic with nil DB
	_ = result
}

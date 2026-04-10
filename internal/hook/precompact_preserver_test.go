package hook

import (
	"context"
	"testing"
)

func TestBuildPreCompactHandler_MessagePreserver(t *testing.T) {
	cfg := HandlerConfig{
		Type:   "message_preserver",
		Config: map[string]any{"preserve_patterns": []any{"error", "critical"}},
	}
	h, err := buildPreCompactHandler(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("handler should not be nil")
	}

	// Verify it produces a valid result
	result, err := h.OnPreCompact(context.Background(), PreCompactEvent{
		SessionID:      "test-session",
		MessageCount:   10,
		EstUtilization: 0.75,
	})
	if err != nil {
		t.Fatalf("OnPreCompact error: %v", err)
	}
	// With no message content in the event, no IDs should be preserved
	if len(result.PreserveMessageIDs) != 0 {
		t.Errorf("expected 0 preserved IDs, got %d", len(result.PreserveMessageIDs))
	}
}

func TestBuildPreCompactHandler_UnknownType(t *testing.T) {
	cfg := HandlerConfig{Type: "nonexistent"}
	_, err := buildPreCompactHandler(cfg)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestMessagePreserver_NilPatterns(t *testing.T) {
	h := NewMessagePreserver(nil)
	result, err := h.OnPreCompact(context.Background(), PreCompactEvent{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreserveMessageIDs != nil {
		t.Errorf("expected nil preserved IDs for nil patterns")
	}
}

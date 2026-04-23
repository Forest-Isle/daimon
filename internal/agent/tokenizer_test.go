package agent

import (
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

func TestRatioTokenizer_Count(t *testing.T) {
	tok := &RatioTokenizer{Ratio: 0.25}

	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"empty", "", 0},
		{"short", "hello", 1},        // 5 * 0.25 = 1
		{"medium", "hello world!!", 3}, // 13 * 0.25 = 3
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tok.Count(tt.text)
			if got != tt.expected {
				t.Errorf("Count(%q) = %d, want %d", tt.text, got, tt.expected)
			}
		})
	}
}

func TestRatioTokenizer_CountMessages(t *testing.T) {
	tok := &RatioTokenizer{Ratio: 0.25}

	msgs := []session.Message{
		{Content: "hello", ToolInput: ""},
		{Content: "world", ToolInput: "input"},
	}
	// total chars: 10(prompt) + 5+0+20 + 5+5+20 = 65
	// 65 * 0.25 = 16
	got := tok.CountMessages(msgs, "sys prompt")
	if got != 16 {
		t.Errorf("CountMessages() = %d, want 16", got)
	}
}

func TestNewTokenizer_ReturnsTokenizer(t *testing.T) {
	// NewTokenizer should return a working tokenizer regardless of model.
	tok := NewTokenizer("test-unknown-model", 0.25)
	if tok == nil {
		t.Fatal("NewTokenizer returned nil")
	}

	count := tok.Count("hello world")
	if count <= 0 {
		t.Errorf("Count() = %d, expected > 0", count)
	}
}

func TestNewTokenizer_FallbackRatio(t *testing.T) {
	// With ratio <= 0, should default to 0.25
	tok := NewTokenizer("impossible-model-xyz", 0)
	if tok == nil {
		t.Fatal("NewTokenizer returned nil")
	}

	// If tiktoken works, this is fine; if it falls back, ratio should be 0.25
	count := tok.Count("hello")
	if count <= 0 {
		t.Errorf("Count() = %d, expected > 0", count)
	}
}

func TestTiktokenTokenizer_CountMessages(t *testing.T) {
	tok := NewTokenizer("gpt-4", 0.25)

	msgs := []session.Message{
		{Content: "Hello, how are you?"},
		{Content: "I am fine, thanks!"},
	}
	total := tok.CountMessages(msgs, "You are a helpful assistant.")

	// We just verify it returns a reasonable non-zero count.
	if total <= 0 {
		t.Errorf("CountMessages() = %d, expected > 0", total)
	}
	// The system prompt + 2 messages should produce at least 10 tokens.
	if total < 10 {
		t.Errorf("CountMessages() = %d, expected >= 10 for non-trivial messages", total)
	}
}

func TestTiktokenTokenizer_LargeTextSampling(t *testing.T) {
	tok := NewTokenizer("gpt-4", 0.25)

	// Create a large text (> maxDirectEncodeLen = 32768)
	largeText := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 1000) // ~45k chars
	count := tok.Count(largeText)

	if count <= 0 {
		t.Errorf("Count() = %d for large text, expected > 0", count)
	}
	// Should complete in a reasonable time (the sampling optimization prevents O(n^2))
	// Rough sanity: ~45k chars / 4 chars per token ≈ 11k tokens
	if count < 1000 || count > 50000 {
		t.Errorf("Count() = %d, expected between 1000 and 50000 for ~45k chars", count)
	}
}

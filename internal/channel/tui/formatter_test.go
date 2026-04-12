package tui

import (
	"strings"
	"testing"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		expected string
	}{
		{
			name:     "short text no wrap",
			text:     "Hello world",
			width:    80,
			expected: "Hello world",
		},
		{
			name:     "long text with wrap",
			text:     "This is a very long line that should be wrapped when it exceeds the specified width limit",
			width:    40,
			expected: "This is a very long line that should be\nwrapped when it exceeds the specified\nwidth limit",
		},
		{
			name:     "text with newlines preserved",
			text:     "Line 1\nLine 2\nLine 3",
			width:    80,
			expected: "Line 1\nLine 2\nLine 3",
		},
		{
			name:     "empty text",
			text:     "",
			width:    80,
			expected: "",
		},
		{
			name:     "single long word",
			text:     "verylongwordthatexceedswidth",
			width:    10,
			expected: "verylongwordthatexceedswidth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapText(tt.text, tt.width)
			if result != tt.expected {
				t.Errorf("wrapText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestUpdateRendererWidth(t *testing.T) {
	tests := []struct {
		name          string
		width         int
		expectedWidth int
	}{
		{
			name:          "normal width",
			width:         100,
			expectedWidth: 96, // 100 - 4
		},
		{
			name:          "small width",
			width:         30,
			expectedWidth: 40, // minimum width
		},
		{
			name:          "zero width",
			width:         0,
			expectedWidth: 76, // default 80 - 4
		},
		{
			name:          "negative width",
			width:         -10,
			expectedWidth: 76, // default 80 - 4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateRendererWidth(tt.width)
			if rendererWidth != tt.expectedWidth {
				t.Errorf("updateRendererWidth(%d) set rendererWidth = %d, want %d",
					tt.width, rendererWidth, tt.expectedWidth)
			}
		})
	}
}

func TestRenderMarkdown(t *testing.T) {
	// Test that markdown rendering doesn't panic and returns some output
	tests := []struct {
		name string
		text string
	}{
		{
			name: "plain text",
			text: "Hello world",
		},
		{
			name: "markdown with bold",
			text: "This is **bold** text",
		},
		{
			name: "markdown with code",
			text: "Use `code` for inline code",
		},
		{
			name: "empty text",
			text: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderMarkdown(tt.text)
			// Just verify it doesn't panic and returns something
			if tt.text != "" && result == "" {
				t.Errorf("renderMarkdown(%q) returned empty string", tt.text)
			}
		})
	}
}

func TestWrapTextPreservesWords(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	width := 20
	result := wrapText(text, width)

	// Verify that words are not broken
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if len(line) > width {
			// Allow some overflow for words that can't be broken
			words := strings.Fields(line)
			if len(words) > 1 {
				t.Errorf("Line exceeds width and contains multiple words: %q (len=%d, width=%d)",
					line, len(line), width)
			}
		}
	}

	// Verify all words are present
	originalWords := strings.Fields(text)
	resultWords := strings.Fields(result)
	if len(originalWords) != len(resultWords) {
		t.Errorf("Word count mismatch: original=%d, result=%d", len(originalWords), len(resultWords))
	}
}

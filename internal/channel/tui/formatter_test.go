package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func displayWidth(s string) int { return runewidth.StringWidth(s) }

func utf8ValidString(s string) bool { return utf8.ValidString(s) }

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
			if tt.text != "" && result == "" {
				t.Errorf("renderMarkdown(%q) returned empty string", tt.text)
			}
		})
	}
}

func TestWrapTextCJK(t *testing.T) {
	result := wrapText("你好世界测试", 8)
	lines := strings.Split(result, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected CJK text to wrap at width 8, got %q (%d line)", result, len(lines))
	}
	for _, line := range lines {
		if w := displayWidth(line); w > 8 {
			t.Errorf("wrapped line exceeds display width: %q (width=%d)", line, w)
		}
	}
}

func TestShortenPathRuneSafe(t *testing.T) {
	got := shortenPath("项目目录路径名称很长很长", 8)
	if !utf8ValidString(got) {
		t.Errorf("shortenPath produced invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected ellipsis in shortened path, got %q", got)
	}
}

func TestTruncateTail(t *testing.T) {
	if got := truncateTail("hello", 10); got != "hello" {
		t.Errorf("short string should pass through, got %q", got)
	}
	got := truncateTail("hello world", 7)
	if displayWidth(got) > 7 {
		t.Errorf("truncated string exceeds width: %q (width=%d)", got, displayWidth(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	cjk := truncateTail("命令执行中读取文件", 6)
	if !utf8ValidString(cjk) {
		t.Errorf("truncateTail produced invalid UTF-8: %q", cjk)
	}
	if displayWidth(cjk) > 6 {
		t.Errorf("CJK truncation exceeds width: %q (width=%d)", cjk, displayWidth(cjk))
	}
}

func TestWrappedRowCount(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		width int
		min   int
	}{
		{"empty", "", 20, 1},
		{"fits", "hello", 20, 1},
		{"two rows", "the quick brown fox jumps over", 10, 3},
		{"exact boundary word", "abcde fghij", 5, 2},
		{"long token", "verylongtokenwithnobreaks", 5, 5},
		{"cjk", "你好世界测试一下换行", 8, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrappedRowCount(tt.line, tt.width)
			if got < tt.min {
				t.Errorf("wrappedRowCount(%q, %d) = %d, want >= %d", tt.line, tt.width, got, tt.min)
			}
		})
	}
}

func TestWrapTextPreservesWords(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	width := 20
	result := wrapText(text, width)

	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if len(line) > width {
			words := strings.Fields(line)
			if len(words) > 1 {
				t.Errorf("Line exceeds width and contains multiple words: %q (len=%d, width=%d)",
					line, len(line), width)
			}
		}
	}

	originalWords := strings.Fields(text)
	resultWords := strings.Fields(result)
	if len(originalWords) != len(resultWords) {
		t.Errorf("Word count mismatch: original=%d, result=%d", len(originalWords), len(resultWords))
	}
}

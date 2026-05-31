package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownIngester_CanHandle(t *testing.T) {
	ing := &MarkdownIngester{}
	if !ing.CanHandle("markdown") {
		t.Error("expected to handle 'markdown'")
	}
	if ing.CanHandle("text") {
		t.Error("expected to not handle 'text'")
	}
	if ing.CanHandle("") {
		t.Error("expected to not handle empty string")
	}
}

func TestMarkdownIngester_Extract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	// Build content with code fence without using raw strings containing backticks
	content := "# Hello World\n\nThis is a **markdown** document with [a link](https://example.com).\n\n- Item 1\n- Item 2\n\n```go\npackage main\n```\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ing := &MarkdownIngester{}
	title, body, err := ing.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if title != "Hello World" {
		t.Errorf("expected title 'Hello World', got %q", title)
	}
	if body == "" {
		t.Error("expected non-empty body")
	}
	if contains(body, "**") {
		t.Error("body should not contain markdown bold syntax")
	}
	if contains(body, "https://") {
		t.Error("body should not contain raw URLs from links")
	}
}

func TestMarkdownIngester_Extract_NoTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my_doc.md")
	content := `Just some text without a heading.`
	os.WriteFile(path, []byte(content), 0644)

	ing := &MarkdownIngester{}
	title, body, err := ing.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if title != "my_doc" {
		t.Errorf("expected filename fallback title 'my_doc', got %q", title)
	}
	if body != "Just some text without a heading." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestMarkdownIngester_Extract_NoFile(t *testing.T) {
	ing := &MarkdownIngester{}
	_, _, err := ing.Extract(context.Background(), "/nonexistent/file.md")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "headings",
			input:    "# H1\n## H2\n### H3",
			expected: "H1\nH2\nH3",
		},
		{
			name:     "bold and italic",
			input:    "**bold** and *italic*",
			expected: "bold and italic",
		},
		{
			name:     "links",
			input:    "a [link](https://example.com) here",
			expected: "a link here",
		},
		{
			name:     "images removed",
			input:    "![alt](img.png) text",
			expected: "text",
		},
		{
			name:     "code blocks",
			input:    "text\n```\ncode here\n```\nmore",
			expected: "text\ncode here\nmore",
		},
		{
			name:     "inline code",
			input:    "use `fmt.Println()` here",
			expected: "use fmt.Println() here",
		},
		{
			name:     "strikethrough",
			input:    "~~struck~~",
			expected: "struck",
		},
		{
			name:     "HTML tags",
			input:    "<div>content</div>",
			expected: "content",
		},
		{
			name:     "multiple newlines condensed",
			input:    "a\n\n\n\n\nb",
			expected: "a\n\nb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdown(tt.input)
			if got != tt.expected {
				t.Errorf("stripMarkdown:\n  expected: %q\n  got:      %q", tt.expected, got)
			}
		})
	}
}

func TestExtractMarkdownTitle(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		fallback string
		expected string
	}{
		{
			name:     "h1 heading",
			content:  "# Hello World\nsome text",
			fallback: "file.md",
			expected: "Hello World",
		},
		{
			name:     "no h1, use fallback",
			content:  "## Subheading\nsome text",
			fallback: "document.md",
			expected: "document",
		},
		{
			name:     "h1 not first line",
			content:  "\n\n# Late Heading",
			fallback: "file.md",
			expected: "Late Heading",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMarkdownTitle(tt.content, tt.fallback)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

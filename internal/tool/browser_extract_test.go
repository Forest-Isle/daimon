package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const cannedArticleHTML = `<!DOCTYPE html>
<html>
<head><title>Test Article</title></head>
<body>
<nav><a href="/">Home</a> | <a href="/about">About</a></nav>
<header><h1>Site Header</h1></header>
<article>
  <h1>Main Article Title</h1>
  <p>This is the first paragraph of the article with some <strong>bold</strong> text.</p>
  <p>Second paragraph with a <a href="https://example.com">link</a> inside.</p>
  <h2>Subheading</h2>
  <ul>
    <li>Item one</li>
    <li>Item two</li>
    <li>Item three</li>
  </ul>
  <pre><code>func main() {
    fmt.Println("Hello")
}</code></pre>
  <p>Final paragraph with <code>inline code</code> example.</p>
</article>
<footer><p>Copyright 2024</p></footer>
<script>var x = 1;</script>
<style>body { color: red; }</style>
</body>
</html>`

func TestBrowserExtractTool_Name(t *testing.T) {
	tool := NewBrowserExtractTool(10*time.Second, false)
	if got := tool.Name(); got != "browser_extract" {
		t.Errorf("Name() = %q, want %q", got, "browser_extract")
	}

	caps := tool.Capabilities()
	if !caps.IsReadOnly {
		t.Error("Capabilities().IsReadOnly should be true")
	}
	if !caps.RequiresNetwork {
		t.Error("Capabilities().RequiresNetwork should be true")
	}
}

func TestBrowserExtractTool_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cannedArticleHTML))
	}))
	defer srv.Close()

	tool := NewBrowserExtractTool(10*time.Second, false)

	input, _ := json.Marshal(browserExtractInput{URL: srv.URL})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	content := result.Output

	if !strings.Contains(content, "# Main Article Title") {
		t.Errorf("expected h1 heading in output, got:\n%s", content)
	}
	if !strings.Contains(content, "## Subheading") {
		t.Errorf("expected h2 heading in output, got:\n%s", content)
	}
	if !strings.Contains(content, "first paragraph") {
		t.Errorf("expected paragraph text in output, got:\n%s", content)
	}
	if !strings.Contains(content, "[link](https://example.com)") {
		t.Errorf("expected markdown link in output, got:\n%s", content)
	}
	if !strings.Contains(content, "- Item one") {
		t.Errorf("expected list items in output, got:\n%s", content)
	}
	if !strings.Contains(content, "```") {
		t.Errorf("expected code block in output, got:\n%s", content)
	}
	if !strings.Contains(content, "`inline code`") {
		t.Errorf("expected inline code in output, got:\n%s", content)
	}

	// Nav, footer, script, style should be stripped
	if strings.Contains(content, "Home") {
		t.Errorf("nav content should be stripped, got:\n%s", content)
	}
	if strings.Contains(content, "Copyright") {
		t.Errorf("footer content should be stripped, got:\n%s", content)
	}
	if strings.Contains(content, "var x") {
		t.Errorf("script content should be stripped, got:\n%s", content)
	}
	if strings.Contains(content, "color: red") {
		t.Errorf("style content should be stripped, got:\n%s", content)
	}

	if result.Metadata == nil {
		t.Fatal("metadata is nil")
	}
	if result.Metadata["page"] != 1 {
		t.Errorf("metadata page = %v, want 1", result.Metadata["page"])
	}
	if result.Metadata["url"] != srv.URL {
		t.Errorf("metadata url = %v, want %q", result.Metadata["url"], srv.URL)
	}
}

func TestBrowserExtractTool_Pagination(t *testing.T) {
	// Generate content larger than one page
	longParagraph := strings.Repeat("This is a long paragraph of text. ", 200)
	longHTML := `<html><body><article><p>` + longParagraph + `</p></article></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML))
	}))
	defer srv.Close()

	tool := NewBrowserExtractTool(10*time.Second, false)

	// Page 1
	input, _ := json.Marshal(browserExtractInput{URL: srv.URL, Page: 1})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	if len(result.Output) > extractPageSize {
		t.Errorf("page 1 output length %d exceeds page size %d", len(result.Output), extractPageSize)
	}
	if !result.IsPartial {
		t.Error("expected IsPartial=true for multi-page content")
	}

	totalPages, ok := result.Metadata["total_pages"].(int)
	if !ok || totalPages < 2 {
		t.Errorf("expected total_pages >= 2, got %v", result.Metadata["total_pages"])
	}

	// Page 2
	input2, _ := json.Marshal(browserExtractInput{URL: srv.URL, Page: 2})
	result2, err := tool.Execute(context.Background(), input2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.Output == result.Output {
		t.Error("page 2 output should differ from page 1")
	}
}

func TestBrowserExtractTool_InvalidURL(t *testing.T) {
	tool := NewBrowserExtractTool(10*time.Second, false)

	tests := []struct {
		name string
		url  string
	}{
		{"ftp scheme", "ftp://example.com/file"},
		{"file scheme", "file:///etc/passwd"},
		{"empty url", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal(browserExtractInput{URL: tc.url})
			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" {
				t.Error("expected error for invalid URL, got none")
			}
		})
	}
}

func TestBrowserExtractTool_HeaderStripping(t *testing.T) {
	htmlWithHeader := `<html><body>
<header><div class="site-brand">My Website</div></header>
<article><h2>Real Content</h2><p>Important text here.</p></article>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(htmlWithHeader))
	}))
	defer srv.Close()

	tool := NewBrowserExtractTool(10*time.Second, false)
	input, _ := json.Marshal(browserExtractInput{URL: srv.URL})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Output, "My Website") {
		t.Errorf("header content should be stripped, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "Real Content") {
		t.Errorf("article content should be preserved, got:\n%s", result.Output)
	}
}

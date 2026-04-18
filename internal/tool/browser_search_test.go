package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const cannedDDGHTML = `<!DOCTYPE html>
<html>
<body>
<div class="result results_links results_links_deep web-result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc%2F&amp;rut=abc">
    Go Documentation
  </a>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc%2F">
    The Go programming language documentation page.
  </a>
</div>
<div class="result results_links results_links_deep web-result">
  <a class="result__a" href="https://go.dev/learn/">
    Learn Go
  </a>
  <a class="result__snippet" href="https://go.dev/learn/">
    Resources for learning Go from scratch.
  </a>
</div>
</body>
</html>`

const cannedFallbackHTML = `<!DOCTYPE html>
<html>
<body>
<div>
  <a href="https://example.com/page1">Example Page One</a>
  <a href="https://example.com/page2">Example Page Two</a>
</div>
</body>
</html>`

func TestBrowserSearchTool_Name(t *testing.T) {
	tool := NewBrowserSearchTool(10*time.Second, false)
	if got := tool.Name(); got != "browser_search" {
		t.Errorf("Name() = %q, want %q", got, "browser_search")
	}
}

func TestBrowserSearchTool_IsReadOnly(t *testing.T) {
	tool := NewBrowserSearchTool(10*time.Second, false)
	if !tool.IsReadOnly() {
		t.Error("IsReadOnly() should return true")
	}
	caps := tool.Capabilities()
	if !caps.IsReadOnly {
		t.Error("Capabilities().IsReadOnly should be true")
	}
	if !caps.RequiresNetwork {
		t.Error("Capabilities().RequiresNetwork should be true")
	}
}

func TestBrowserSearchTool_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cannedDDGHTML))
	}))
	defer srv.Close()

	tool := NewBrowserSearchTool(10*time.Second, false)
	tool.searchURL = srv.URL + "/?q=%s"

	input, _ := json.Marshal(browserSearchInput{Query: "golang"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var items []searchResultItem
	if err := json.Unmarshal([]byte(result.Output), &items); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if len(items) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(items))
	}

	if items[0].Title != "Go Documentation" {
		t.Errorf("items[0].Title = %q, want %q", items[0].Title, "Go Documentation")
	}
	if items[0].URL != "https://golang.org/doc/" {
		t.Errorf("items[0].URL = %q, want %q", items[0].URL, "https://golang.org/doc/")
	}
	if items[0].Snippet != "The Go programming language documentation page." {
		t.Errorf("items[0].Snippet = %q, want %q", items[0].Snippet, "The Go programming language documentation page.")
	}

	if items[1].URL != "https://go.dev/learn/" {
		t.Errorf("items[1].URL = %q, want %q", items[1].URL, "https://go.dev/learn/")
	}

	if result.Metadata == nil {
		t.Fatal("metadata is nil")
	}
	if result.Metadata["query"] != "golang" {
		t.Errorf("metadata query = %v, want %q", result.Metadata["query"], "golang")
	}
}

func TestBrowserSearchTool_FallbackParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cannedFallbackHTML))
	}))
	defer srv.Close()

	tool := NewBrowserSearchTool(10*time.Second, false)
	tool.searchURL = srv.URL + "/?q=%s"

	input, _ := json.Marshal(browserSearchInput{Query: "example"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var items []searchResultItem
	if err := json.Unmarshal([]byte(result.Output), &items); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 results, got %d", len(items))
	}
	if items[0].Title != "Example Page One" {
		t.Errorf("items[0].Title = %q, want %q", items[0].Title, "Example Page One")
	}
	if items[0].URL != "https://example.com/page1" {
		t.Errorf("items[0].URL = %q, want %q", items[0].URL, "https://example.com/page1")
	}
}

func TestBrowserSearchTool_EmptyQuery(t *testing.T) {
	tool := NewBrowserSearchTool(10*time.Second, false)

	input, _ := json.Marshal(browserSearchInput{Query: ""})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for empty query, got none")
	}
}

func TestBrowserSearchTool_Pagination(t *testing.T) {
	var requestedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURL = r.URL.String()
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cannedDDGHTML))
	}))
	defer srv.Close()

	tool := NewBrowserSearchTool(10*time.Second, false)
	tool.searchURL = srv.URL + "/?q=%s"

	input, _ := json.Marshal(browserSearchInput{Query: "golang", Page: 2})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if requestedURL == "" {
		t.Fatal("no request was made")
	}
	if !contains(requestedURL, "s=30") {
		t.Errorf("expected pagination offset s=30 in URL %q", requestedURL)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

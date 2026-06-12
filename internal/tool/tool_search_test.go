package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type deferredTestTool struct {
	name        string
	description string
	schema      map[string]any
}

func (t *deferredTestTool) Name() string                { return t.name }
func (t *deferredTestTool) Description() string         { return t.description }
func (t *deferredTestTool) InputSchema() map[string]any { return t.schema }
func (t *deferredTestTool) Execute(context.Context, []byte) (Result, error) {
	return Result{Output: "ok"}, nil
}
func (t *deferredTestTool) RequiresApproval() bool { return false }

func TestDeferredCatalogSearchRanksKeywordMatches(t *testing.T) {
	catalog := NewDeferredCatalog()
	for _, spec := range []DeferredToolSpec{
		{
			Name:        "mcp_browser_click",
			Description: "Click an element in a browser page",
			Source:      "mcp:browser",
			Load: func(context.Context) (Tool, error) {
				return &deferredTestTool{name: "mcp_browser_click"}, nil
			},
		},
		{
			Name:        "mcp_docs_search",
			Description: "Search documentation pages",
			Source:      "mcp:docs",
			Load: func(context.Context) (Tool, error) {
				return &deferredTestTool{name: "mcp_docs_search"}, nil
			},
		},
	} {
		if err := catalog.Add(spec); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	matches := catalog.Search("browser", 5)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "mcp_browser_click" {
		t.Fatalf("match name = %q, want mcp_browser_click", matches[0].Name)
	}
	if matches[0].InputSchema != nil {
		t.Fatal("keyword search should not load input schemas")
	}
}

func TestToolSearchDoesNotRegisterKeywordResults(t *testing.T) {
	registry := NewRegistry()
	catalog := NewDeferredCatalog()
	if err := catalog.Add(DeferredToolSpec{
		Name:        "mcp_browser_snapshot",
		Description: "Capture a browser accessibility snapshot",
		Source:      "mcp:browser",
		Load: func(context.Context) (Tool, error) {
			return &deferredTestTool{name: "mcp_browser_snapshot"}, nil
		},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	search := NewToolSearchTool(catalog, registry)
	result, err := search.Execute(context.Background(), []byte(`{"query":"browser","limit":5}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Execute() returned tool error: %s", result.Error)
	}
	if _, err := registry.Get("mcp_browser_snapshot"); err == nil {
		t.Fatal("keyword search should not register deferred tool")
	}

	var resp toolSearchResponse
	if err := json.Unmarshal([]byte(result.Output), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Mode != "search" {
		t.Fatalf("mode = %q, want search", resp.Mode)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].Name != "mcp_browser_snapshot" {
		t.Fatalf("unexpected matches: %+v", resp.Matches)
	}
}

func TestToolSearchSelectRegistersDeferredToolWithSchema(t *testing.T) {
	registry := NewRegistry()
	catalog := NewDeferredCatalog()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required": []string{"url"},
	}
	loads := 0
	if err := catalog.Add(DeferredToolSpec{
		Name:        "mcp_browser_open",
		Description: "Open a URL in a browser",
		Source:      "mcp:browser",
		Load: func(context.Context) (Tool, error) {
			loads++
			return &deferredTestTool{
				name:        "mcp_browser_open",
				description: "Open a URL in a browser",
				schema:      schema,
			}, nil
		},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	registry.Register(NewToolSearchTool(catalog, registry))

	if _, err := registry.Get("mcp_browser_open"); err == nil {
		t.Fatal("deferred tool should be hidden before select")
	}

	searchTool, err := registry.Get("tool_search")
	if err != nil {
		t.Fatalf("tool_search not registered: %v", err)
	}
	result, err := searchTool.Execute(context.Background(), []byte(`{"query":"select:mcp_browser_open"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Execute() returned tool error: %s", result.Error)
	}

	resolved, err := registry.Get("mcp_browser_open")
	if err != nil {
		t.Fatalf("selected deferred tool should be registered: %v", err)
	}
	if resolved.Description() != "Open a URL in a browser" {
		t.Fatalf("resolved description = %q", resolved.Description())
	}
	if loads != 1 {
		t.Fatalf("loader called %d times, want 1", loads)
	}

	var resp toolSearchResponse
	if err := json.Unmarshal([]byte(result.Output), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Mode != "select" {
		t.Fatalf("mode = %q, want select", resp.Mode)
	}
	if len(resp.Resolved) != 1 {
		t.Fatalf("resolved count = %d, want 1", len(resp.Resolved))
	}
	if resp.Resolved[0].InputSchema == nil {
		t.Fatal("select response should include input schema")
	}
	if !strings.Contains(result.Output, `"mcp_browser_open"`) {
		t.Fatalf("output should mention selected tool, got %s", result.Output)
	}
}

func TestParseToolSelectQuery(t *testing.T) {
	names, ok := parseToolSelectQuery("select:alpha, beta\tgamma alpha")
	if !ok {
		t.Fatal("expected select query")
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(names) != len(want) {
		t.Fatalf("names = %#v, want %#v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %#v, want %#v", names, want)
		}
	}
}

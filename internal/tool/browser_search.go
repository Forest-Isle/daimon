package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// BrowserSearchTool performs structured web searches and returns results as JSON.
type BrowserSearchTool struct {
	client    *http.Client
	approval  bool
	searchURL string // configurable for testing; defaults to DuckDuckGo HTML
}

type browserSearchInput struct {
	Query string `json:"query"`
	Page  int    `json:"page"`
}

type searchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

const defaultSearchURL = "https://html.duckduckgo.com/html/?q=%s"

func NewBrowserSearchTool(timeout time.Duration, requiresApproval bool) *BrowserSearchTool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &BrowserSearchTool{
		client:    &http.Client{Timeout: timeout},
		approval:  requiresApproval,
		searchURL: defaultSearchURL,
	}
}

func (b *BrowserSearchTool) Name() string           { return "browser_search" }
func (b *BrowserSearchTool) RequiresApproval() bool { return b.approval }
func (b *BrowserSearchTool) IsReadOnly() bool       { return true }

func (b *BrowserSearchTool) Description() string {
	return "Search the web and return structured results with title, URL, and snippet."
}

func (b *BrowserSearchTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (b *BrowserSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"page": map[string]any{
				"type":        "integer",
				"description": "Page number (default 1)",
			},
		},
		"required": []string{"query"},
	}
}

func (b *BrowserSearchTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in browserSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	if strings.TrimSpace(in.Query) == "" {
		return Result{Error: "query is required"}, nil
	}

	if in.Page < 1 {
		in.Page = 1
	}

	searchURL := fmt.Sprintf(b.searchURL, url.QueryEscape(in.Query))
	if in.Page > 1 {
		if strings.Contains(searchURL, "?") {
			searchURL += fmt.Sprintf("&s=%d", (in.Page-1)*30)
		} else {
			searchURL += fmt.Sprintf("?s=%d", (in.Page-1)*30)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return Result{Error: "failed to create request: " + err.Error()}, nil
	}
	req.Header.Set("User-Agent", "IronClaw/1.0")

	resp, err := b.client.Do(req)
	if err != nil {
		return Result{Error: "failed to fetch search results: " + err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxOutputSize)))
	if err != nil {
		return Result{Error: "failed to read response: " + err.Error()}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{
			Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	html := string(body)
	results := parseDuckDuckGoResults(html)
	if len(results) == 0 {
		results = parseFallbackResults(html)
	}

	out, err := json.Marshal(results)
	if err != nil {
		return Result{Error: "failed to marshal results: " + err.Error()}, nil
	}

	return Result{
		Output: string(out),
		Metadata: map[string]any{
			"query":        in.Query,
			"page":         in.Page,
			"result_count": len(results),
		},
	}, nil
}

var (
	ddgResultRe  = regexp.MustCompile(`(?s)<a[^>]+class="[^"]*result__a[^"]*"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	htmlEntityRe = regexp.MustCompile(`&[a-zA-Z0-9#]+;`)
)

func parseDuckDuckGoResults(html string) []searchResultItem {
	linkMatches := ddgResultRe.FindAllStringSubmatch(html, -1)
	if len(linkMatches) == 0 {
		return nil
	}

	snippetMatches := ddgSnippetRe.FindAllStringSubmatch(html, -1)

	var results []searchResultItem
	for i, m := range linkMatches {
		rawURL := m[1]
		title := stripHTML(m[2])

		cleanedURL := cleanDDGRedirectURL(rawURL)
		if cleanedURL == "" || title == "" {
			continue
		}

		snippet := ""
		if i < len(snippetMatches) {
			snippet = stripHTML(snippetMatches[i][1])
		}

		results = append(results, searchResultItem{
			Title:   title,
			URL:     cleanedURL,
			Snippet: snippet,
		})
	}
	return results
}

var fallbackLinkRe = regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*>(.*?)</a>`)

func parseFallbackResults(html string) []searchResultItem {
	matches := fallbackLinkRe.FindAllStringSubmatch(html, 20)
	seen := make(map[string]bool)
	var results []searchResultItem

	for _, m := range matches {
		u := m[1]
		title := stripHTML(m[2])
		if title == "" || seen[u] {
			continue
		}
		seen[u] = true
		results = append(results, searchResultItem{
			Title:   title,
			URL:     u,
			Snippet: "",
		})
	}
	return results
}

func cleanDDGRedirectURL(rawURL string) string {
	if strings.Contains(rawURL, "duckduckgo.com/l/?") {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return rawURL
		}
		if uddg := parsed.Query().Get("uddg"); uddg != "" {
			return uddg
		}
	}
	if strings.HasPrefix(rawURL, "//duckduckgo.com/l/?") {
		parsed, err := url.Parse("https:" + rawURL)
		if err != nil {
			return rawURL
		}
		if uddg := parsed.Query().Get("uddg"); uddg != "" {
			return uddg
		}
	}
	return rawURL
}

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = decodeHTMLEntities(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func decodeHTMLEntities(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
	)
	s = replacer.Replace(s)
	s = htmlEntityRe.ReplaceAllStringFunc(s, func(match string) string {
		return ""
	})
	return s
}

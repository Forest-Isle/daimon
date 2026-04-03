package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// WebIngester fetches and parses web pages using stdlib net/http.
type WebIngester struct {
	client *http.Client
}

// CanHandle returns true for web source type.
func (w *WebIngester) CanHandle(sourceType string) bool {
	return sourceType == "web"
}

// Extract fetches a URL and returns its title and plain text content.
func (w *WebIngester) Extract(ctx context.Context, uri string) (string, string, error) {
	client := w.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", "", fmt.Errorf("web ingest: create request: %w", err)
	}
	req.Header.Set("User-Agent", "IronClaw-KnowledgeBot/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("web ingest: fetch %s: %w", uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("web ingest: HTTP %d for %s", resp.StatusCode, uri)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return "", "", fmt.Errorf("web ingest: read body: %w", err)
	}

	htmlStr := string(body)
	title := extractHTMLTitle(htmlStr)
	if title == "" {
		title = uri
	}
	content := htmlToText(htmlStr)
	return title, content, nil
}

var (
	htmlTitleRe  = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	htmlScriptRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStyleRe  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagsRe   = regexp.MustCompile(`<[^>]+>`)
	htmlEntityRe = regexp.MustCompile(`&[a-z]+;`)
	htmlMultiSp  = regexp.MustCompile(`[ \t]+`)
	htmlMultiNl  = regexp.MustCompile(`\n{3,}`)
)

func extractHTMLTitle(html string) string {
	if m := htmlTitleRe.FindStringSubmatch(html); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func htmlToText(html string) string {
	s := htmlScriptRe.ReplaceAllString(html, "")
	s = htmlStyleRe.ReplaceAllString(s, "")
	s = htmlTagsRe.ReplaceAllString(s, " ")
	s = htmlEntityRe.ReplaceAllStringFunc(s, decodeHTMLEntity)
	s = htmlMultiSp.ReplaceAllString(s, " ")
	s = htmlMultiNl.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func decodeHTMLEntity(entity string) string {
	switch entity {
	case "&amp;":
		return "&"
	case "&lt;":
		return "<"
	case "&gt;":
		return ">"
	case "&quot;":
		return "\""
	case "&apos;", "&#39;":
		return "'"
	case "&nbsp;":
		return " "
	default:
		return ""
	}
}

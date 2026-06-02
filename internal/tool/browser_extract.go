package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const extractPageSize = 4000

// BrowserExtractTool fetches a URL and returns clean Markdown content with pagination.
type BrowserExtractTool struct {
	client   *http.Client
	approval bool
}

type browserExtractInput struct {
	URL  string `json:"url"`
	Page int    `json:"page"`
}

func NewBrowserExtractTool(timeout time.Duration, requiresApproval bool) *BrowserExtractTool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &BrowserExtractTool{
		client:   &http.Client{Timeout: timeout},
		approval: requiresApproval,
	}
}

func (b *BrowserExtractTool) Name() string           { return "browser_extract" }
func (b *BrowserExtractTool) RequiresApproval() bool { return b.approval }
func (b *BrowserExtractTool) IsReadOnly() bool       { return true }

func (b *BrowserExtractTool) Description() string {
	return "Fetch a URL and extract its main content as clean Markdown, with pagination support."
}

func (b *BrowserExtractTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (b *BrowserExtractTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to extract content from",
			},
			"page": map[string]any{
				"type":        "integer",
				"description": "Page number for pagination (default 1)",
			},
		},
		"required": []string{"url"},
	}
}

func (b *BrowserExtractTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in browserExtractInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	if strings.TrimSpace(in.URL) == "" {
		return Result{Error: "url is required"}, nil
	}

	parsedURL, err := url.Parse(in.URL)
	if err != nil {
		return Result{Error: "invalid URL: " + err.Error()}, nil
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return Result{Error: "URL scheme must be http or https"}, nil
	}

	if in.Page < 1 {
		in.Page = 1
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return Result{Error: "failed to create request: " + err.Error()}, nil
	}
	req.Header.Set("User-Agent", "IronClaw/1.0")

	resp, err := b.client.Do(req)
	if err != nil {
		return Result{Error: "failed to fetch URL: " + err.Error()}, nil
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

	markdown := htmlToMarkdown(string(body))

	totalPages := int(math.Ceil(float64(len(markdown)) / float64(extractPageSize)))
	if totalPages < 1 {
		totalPages = 1
	}

	if in.Page > totalPages {
		in.Page = totalPages
	}

	start := (in.Page - 1) * extractPageSize
	end := start + extractPageSize
	if end > len(markdown) {
		end = len(markdown)
	}
	page := markdown[start:end]

	return Result{
		Output:    page,
		IsPartial: totalPages > 1,
		Metadata: map[string]any{
			"url":         in.URL,
			"page":        in.Page,
			"total_pages": totalPages,
		},
	}, nil
}

var (
	reScript    = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>`)
	reStyle     = regexp.MustCompile(`(?si)<style[^>]*>.*?</style>`)
	reNav       = regexp.MustCompile(`(?si)<nav[^>]*>.*?</nav>`)
	reFooter    = regexp.MustCompile(`(?si)<footer[^>]*>.*?</footer>`)
	reHeader    = regexp.MustCompile(`(?si)<header[^>]*>.*?</header>`)
	reAside     = regexp.MustCompile(`(?si)<aside[^>]*>.*?</aside>`)
	reHeading   = regexp.MustCompile(`(?si)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	reParagraph = regexp.MustCompile(`(?si)<p[^>]*>(.*?)</p>`)
	reLink      = regexp.MustCompile(`(?si)<a[^>]+href="([^"]*)"[^>]*>(.*?)</a>`)
	reListItem  = regexp.MustCompile(`(?si)<li[^>]*>(.*?)</li>`)
	reCodeBlock = regexp.MustCompile(`(?si)<pre[^>]*>(.*?)</pre>`)
	reCode      = regexp.MustCompile(`(?si)<code[^>]*>(.*?)</code>`)
	reBr        = regexp.MustCompile(`(?i)<br\s*/?>`)
	reAllTags   = regexp.MustCompile(`<[^>]+>`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
)

func htmlToMarkdown(html string) string {
	// Remove boilerplate elements
	s := reScript.ReplaceAllString(html, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reNav.ReplaceAllString(s, "")
	s = reFooter.ReplaceAllString(s, "")
	s = reHeader.ReplaceAllString(s, "")
	s = reAside.ReplaceAllString(s, "")

	// Convert <pre> blocks before other transformations
	s = reCodeBlock.ReplaceAllStringFunc(s, func(match string) string {
		sub := reCodeBlock.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		code := reAllTags.ReplaceAllString(sub[1], "")
		code = decodeHTMLEntities(code)
		return "\n```\n" + strings.TrimSpace(code) + "\n```\n"
	})

	// Convert inline <code>
	s = reCode.ReplaceAllStringFunc(s, func(match string) string {
		sub := reCode.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		code := reAllTags.ReplaceAllString(sub[1], "")
		code = decodeHTMLEntities(code)
		return "`" + strings.TrimSpace(code) + "`"
	})

	// Convert links before stripping remaining tags
	s = reLink.ReplaceAllStringFunc(s, func(match string) string {
		sub := reLink.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		href := sub[1]
		text := reAllTags.ReplaceAllString(sub[2], "")
		text = strings.TrimSpace(decodeHTMLEntities(text))
		if text == "" {
			return ""
		}
		return "[" + text + "](" + href + ")"
	})

	// Convert headings
	s = reHeading.ReplaceAllStringFunc(s, func(match string) string {
		sub := reHeading.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		level := sub[1][0] - '0'
		text := reAllTags.ReplaceAllString(sub[2], "")
		text = strings.TrimSpace(decodeHTMLEntities(text))
		prefix := strings.Repeat("#", int(level))
		return "\n" + prefix + " " + text + "\n"
	})

	// Convert paragraphs
	s = reParagraph.ReplaceAllStringFunc(s, func(match string) string {
		sub := reParagraph.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		text := reAllTags.ReplaceAllString(sub[1], "")
		text = strings.TrimSpace(decodeHTMLEntities(text))
		if text == "" {
			return ""
		}
		return "\n" + text + "\n"
	})

	// Convert list items
	s = reListItem.ReplaceAllStringFunc(s, func(match string) string {
		sub := reListItem.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		text := reAllTags.ReplaceAllString(sub[1], "")
		text = strings.TrimSpace(decodeHTMLEntities(text))
		return "- " + text + "\n"
	})

	// Convert <br> to newlines
	s = reBr.ReplaceAllString(s, "\n")

	// Strip all remaining HTML tags
	s = reAllTags.ReplaceAllString(s, "")

	// Decode remaining entities
	s = decodeHTMLEntities(s)

	// Collapse excessive newlines
	s = reMultiNL.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

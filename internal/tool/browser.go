package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// BrowserTool fetches the content of a URL via HTTP GET and returns it as text.
// It is a read-only tool that requires network access.
type BrowserTool struct {
	client *http.Client
}

// browserInput represents the input parameters for the browser tool.
type browserInput struct {
	URL string `json:"url"`
}

// NewBrowserTool creates a new BrowserTool with a default 30-second timeout.
func NewBrowserTool() *BrowserTool {
	return &BrowserTool{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *BrowserTool) Name() string          { return "browser" }
func (b *BrowserTool) Description() string   { return "Fetch and return the content of a URL via HTTP GET." }
func (b *BrowserTool) RequiresApproval() bool { return false }
func (b *BrowserTool) IsReadOnly() bool       { return true }

// Capabilities declares BrowserTool as read-only with network access.
func (b *BrowserTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (b *BrowserTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch",
			},
		},
		"required": []string{"url"},
	}
}

func (b *BrowserTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in browserInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.URL == "" {
		return Result{Error: "url is required"}, nil
	}

	// Validate URL scheme before making the request.
	parsedURL, err := url.Parse(in.URL)
	if err != nil {
		return Result{Error: "invalid URL: " + err.Error()}, nil
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return Result{Error: "URL scheme must be http or https"}, nil
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

	// Read body with size limit to prevent unbounded memory usage.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxOutputSize)))
	if err != nil {
		return Result{Error: "failed to read response: " + err.Error()}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{
			Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	content := string(body)
	isPartial := len(body) >= maxOutputSize

	return Result{
		Output:    content,
		IsPartial: isPartial,
		Metadata: map[string]any{
			"status_code":  resp.StatusCode,
			"content_type": resp.Header.Get("Content-Type"),
			"url":          in.URL,
		},
	}, nil
}

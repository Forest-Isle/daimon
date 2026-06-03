package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPTool struct {
	client   *http.Client
	approval bool
}

// SetCheckRedirect injects a redirect validation function. When set, every
// HTTP redirect target is validated by the function before being followed.
// The function should return an error if the redirect URL is not allowed.
func (h *HTTPTool) SetCheckRedirect(checkFn func(string) error) {
	h.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if checkFn != nil {
			return checkFn(req.URL.String())
		}
		return nil
	}
}

type httpInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func NewHTTPTool(timeout time.Duration, requiresApproval bool) *HTTPTool {
	return &HTTPTool{
		client:   &http.Client{Timeout: timeout},
		approval: requiresApproval,
	}
}

func (h *HTTPTool) Name() string           { return "http" }
func (h *HTTPTool) Description() string    { return "Make HTTP requests to external APIs." }
func (h *HTTPTool) RequiresApproval() bool { return h.approval }

// IsReadOnly returns false because HTTP tool can make POST/PUT/DELETE requests.
func (h *HTTPTool) IsReadOnly() bool { return false }

// Capabilities returns the HTTP tool's capabilities.
func (h *HTTPTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      false, // can make POST/PUT/DELETE
		IsDestructive:   false,
		RequiresNetwork: true,
		ApprovalMode:    "auto",
	}
}

func (h *HTTPTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"method": map[string]any{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
				"description": "HTTP method",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Request URL",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "Request headers",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Request body",
			},
		},
		"required": []string{"method", "url"},
	}
}

func (h *HTTPTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in httpInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	var bodyReader io.Reader
	if in.Body != "" {
		bodyReader = strings.NewReader(in.Body)
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, bodyReader)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxOutputSize)))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	output := fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
	return Result{
		Output: output,
		Metadata: map[string]any{
			"status_code":  resp.StatusCode,
			"content_type": resp.Header.Get("Content-Type"),
		},
	}, nil
}

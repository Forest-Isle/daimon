package tool

import (
	"context"
)

// BrowserTool is a stub for future browser automation.
type BrowserTool struct{}

func NewBrowserTool() *BrowserTool { return &BrowserTool{} }

func (b *BrowserTool) Name() string        { return "browser" }
func (b *BrowserTool) Description() string  { return "Browser automation (not yet implemented)." }
func (b *BrowserTool) RequiresApproval() bool { return true }

// IsReadOnly returns true because the browser tool only reads web content.
func (b *BrowserTool) IsReadOnly() bool { return true }

// Capabilities returns the browser tool's capabilities.
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
			"action": map[string]any{
				"type":        "string",
				"description": "Browser action to perform",
			},
		},
		"required": []string{"action"},
	}
}

func (b *BrowserTool) Execute(_ context.Context, _ []byte) (Result, error) {
	return Result{Error: "browser tool is not yet implemented", Type: ResultText}, nil
}

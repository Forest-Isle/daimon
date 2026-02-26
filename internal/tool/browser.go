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
	return Result{Error: "browser tool is not yet implemented"}, nil
}

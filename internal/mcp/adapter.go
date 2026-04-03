package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ToolAdapter wraps an MCP tool as an IronClaw tool.Tool.
type ToolAdapter struct {
	client     client.MCPClient
	serverName string
	toolDef    mcp.Tool
	approval   bool
}

func NewToolAdapter(c client.MCPClient, serverName string, t mcp.Tool, approval bool) *ToolAdapter {
	return &ToolAdapter{
		client:     c,
		serverName: serverName,
		toolDef:    t,
		approval:   approval,
	}
}

func (a *ToolAdapter) Name() string {
	return fmt.Sprintf("mcp_%s_%s", a.serverName, a.toolDef.Name)
}

func (a *ToolAdapter) Description() string {
	return a.toolDef.Description
}

func (a *ToolAdapter) InputSchema() map[string]any {
	schema := map[string]any{
		"type": a.toolDef.InputSchema.Type,
	}
	if a.toolDef.InputSchema.Properties != nil {
		schema["properties"] = a.toolDef.InputSchema.Properties
	}
	if len(a.toolDef.InputSchema.Required) > 0 {
		schema["required"] = a.toolDef.InputSchema.Required
	}
	return schema
}

func (a *ToolAdapter) RequiresApproval() bool {
	return a.approval
}

func (a *ToolAdapter) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = a.toolDef.Name
	req.Params.Arguments = args

	resp, err := a.client.CallTool(ctx, req)
	if err != nil {
		return tool.Result{}, fmt.Errorf("mcp call %s: %w", a.Name(), err)
	}

	if resp.IsError {
		return tool.Result{Error: extractText(resp.Content)}, nil
	}
	return tool.Result{Output: extractText(resp.Content)}, nil
}

// extractText joins all text content blocks from an MCP response.
func extractText(contents []mcp.Content) string {
	var parts []string
	for _, c := range contents {
		if tc, ok := c.(mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

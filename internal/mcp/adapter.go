package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
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
	// Ensure we always return a valid JSON Schema object.
	// OpenRouter API requires all tools to have a complete input_schema.
	schemaType := a.toolDef.InputSchema.Type
	if schemaType == "" {
		schemaType = "object"
	}

	schema := map[string]any{
		"type": schemaType,
	}

	// Add properties if present
	if len(a.toolDef.InputSchema.Properties) > 0 {
		schema["properties"] = a.toolDef.InputSchema.Properties
	} else if schemaType == "object" {
		// For object types without properties, add empty properties to satisfy API requirements
		schema["properties"] = make(map[string]any)
	}

	// Add required if present
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

	// Redact potential credentials leaked in MCP server responses before
	// they propagate into session history or logs.
	text := Redact(extractText(resp.Content))
	if resp.IsError {
		return tool.Result{Error: text}, nil
	}
	return tool.Result{Output: text}, nil
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

package core

import (
	"context"
	"encoding/json"
)

// ToolHandler executes a single tool call and returns its result.
type ToolHandler func(ctx context.Context, call ToolCall) (ToolResult, error)

// ToolMiddleware wraps a ToolHandler with cross-cutting behaviour
// (permissions, caching, audit, hooks). Middleware is composed in the
// order it is added: the first middleware sees the request first.
type ToolMiddleware func(next ToolHandler) ToolHandler

// chainTool composes middleware around a base handler in correct order.
// The first element in mws is the outermost layer (called first).
func chainTool(base ToolHandler, mws ...ToolMiddleware) ToolHandler {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			base = mws[i](base)
		}
	}
	return base
}

// baseToolHandler resolves a tool by name and runs Execute.
func baseToolHandler(reg *ToolRegistry) ToolHandler {
	return func(ctx context.Context, call ToolCall) (ToolResult, error) {
		t, ok := reg.Lookup(call.Name)
		if !ok {
			return ToolResult{UseID: call.ID, Error: "tool not registered: " + call.Name}, nil
		}
		input := call.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		res, err := t.Execute(ctx, input)
		if res.UseID == "" {
			res.UseID = call.ID
		}
		return res, err
	}
}

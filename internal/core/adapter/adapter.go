// Package adapter bridges the new core to the legacy internal/agent and
// internal/tool packages so the entire surface of existing tools and
// providers is reusable from core without copying code.
package adapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/core"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// LegacyProvider wraps an internal/agent.Provider as a core.Provider.
// Streaming is intentionally unimplemented for now — the core loop is
// non-streaming and most providers also implement Complete.
type LegacyProvider struct {
	P agent.Provider
}

// NewLegacyProvider constructs a core.Provider that delegates to p.
func NewLegacyProvider(p agent.Provider) *LegacyProvider { return &LegacyProvider{P: p} }

// Complete maps core types onto agent types and back.
func (l *LegacyProvider) Complete(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	areq := agent.CompletionRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  toAgentMessages(req.Messages),
		Tools:     toAgentTools(req.Tools),
		MaxTokens: req.MaxTokens,
	}
	resp, err := l.P.Complete(ctx, areq)
	if err != nil {
		return nil, err
	}
	return &core.LLMResponse{
		Text:       resp.Text,
		ToolCalls:  fromAgentToolCalls(resp.ToolCalls),
		StopReason: fromAgentStop(resp.StopReason),
	}, nil
}

// Stream is not used by the core loop; return an error to surface misuse
// rather than silently fall back to non-streaming.
func (l *LegacyProvider) Stream(ctx context.Context, req core.LLMRequest) (core.Stream, error) {
	return nil, errors.New("adapter: streaming via core not yet supported — use Complete")
}

func toAgentMessages(in []core.Message) []agent.CompletionMessage {
	out := make([]agent.CompletionMessage, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case core.RoleSystem:
			// Legacy Provider takes System as a top-level field; ignore here.
			continue
		case core.RoleTool:
			out = append(out, agent.CompletionMessage{
				Role:      "tool",
				ToolUseID: m.ToolUseID,
				Content:   m.Content,
			})
		case core.RoleAssistant:
			am := agent.CompletionMessage{Role: "assistant", Content: m.Content}
			if len(m.ToolCalls) > 0 {
				blocks := make([]agent.ToolUseBlock, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					blocks = append(blocks, agent.ToolUseBlock{
						ID:    c.ID,
						Name:  c.Name,
						Input: string(c.Input),
					})
				}
				am.ToolBlocks = blocks
			}
			out = append(out, am)
		default:
			out = append(out, agent.CompletionMessage{Role: string(m.Role), Content: m.Content})
		}
	}
	return out
}

func toAgentTools(in []core.ToolSchema) []agent.ToolDefinition {
	out := make([]agent.ToolDefinition, 0, len(in))
	for _, t := range in {
		out = append(out, agent.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out
}

func fromAgentToolCalls(in []agent.ToolUseBlock) []core.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]core.ToolCall, 0, len(in))
	for _, b := range in {
		raw := json.RawMessage(b.Input)
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		out = append(out, core.ToolCall{ID: b.ID, Name: b.Name, Input: raw})
	}
	return out
}

func fromAgentStop(s agent.StopReason) core.StopReason {
	switch s {
	case agent.StopEndTurn:
		return core.StopEndTurn
	case agent.StopToolUse:
		return core.StopToolUse
	case agent.StopMaxToken:
		return core.StopMaxTurns
	}
	return core.StopEndTurn
}

// LegacyTool wraps an internal/tool.Tool as a core.Tool.
type LegacyTool struct {
	T tool.Tool
}

// NewLegacyTool wraps a single legacy tool.
func NewLegacyTool(t tool.Tool) *LegacyTool { return &LegacyTool{T: t} }

// Schema converts the legacy tool's metadata to core.ToolSchema.
func (l *LegacyTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        l.T.Name(),
		Description: l.T.Description(),
		InputSchema: l.T.InputSchema(),
	}
}

// ReadOnly delegates to the legacy capability surface.
func (l *LegacyTool) ReadOnly() bool { return tool.IsToolReadOnly(l.T) }

// Execute invokes the legacy tool, normalising the result shape.
func (l *LegacyTool) Execute(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	res, err := l.T.Execute(ctx, []byte(input))
	if err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	return core.ToolResult{
		Output:   res.Output,
		Error:    res.Error,
		Metadata: res.Metadata,
	}, nil
}

// ImportToolRegistry copies all currently-available legacy tools into a
// new core.ToolRegistry. Tools added to the legacy registry afterwards
// are NOT reflected — call ImportToolRegistry again to refresh.
func ImportToolRegistry(legacy *tool.Registry) *core.ToolRegistry {
	reg := core.NewToolRegistry()
	if legacy == nil {
		return reg
	}
	for _, t := range legacy.All() {
		reg.Register(NewLegacyTool(t))
	}
	return reg
}

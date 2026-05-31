// Package adapter bridges the new core to the legacy internal/agent and
// internal/tool packages so the entire surface of existing tools and
// providers is reusable from core without copying code.
package adapter

import (
	"context"
	"encoding/json"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/archived/core"
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

// Stream delegates to the underlying provider's Stream method, adapting
// the agent.StreamIterator to core.Stream.
func (l *LegacyProvider) Stream(ctx context.Context, req core.LLMRequest) (core.Stream, error) {
	areq := agent.CompletionRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  toAgentMessages(req.Messages),
		Tools:     toAgentTools(req.Tools),
		MaxTokens: req.MaxTokens,
	}
	inner, err := l.P.Stream(ctx, areq)
	if err != nil {
		return nil, err
	}
	return &legacyStream{inner: inner}, nil
}

// legacyStream adapts agent.StreamIterator to core.Stream.
type legacyStream struct {
	inner agent.StreamIterator

	// pending buffers tool calls from a final delta so they are emitted
	// one-by-one before the Done signal.
	pending []core.ToolCall

	// finalStop and pendingDone track whether a synthetic Done chunk must
	// be emitted after all pending tool calls have been drained.
	pendingDone bool
	finalStop   core.StopReason
}

func (s *legacyStream) Next(ctx context.Context) (core.LLMChunk, error) {
	// Drain buffered tool calls before reading more from the inner stream.
	if len(s.pending) > 0 {
		tc := s.pending[0]
		s.pending = s.pending[1:]
		return core.LLMChunk{ToolCall: &tc}, nil
	}

	// Emit the synthetic Done chunk now that all tool calls are drained.
	if s.pendingDone {
		return core.LLMChunk{Done: true, Stop: s.finalStop}, nil
	}

	delta, err := s.inner.Next()
	if err != nil {
		return core.LLMChunk{}, err
	}

	chunk := core.LLMChunk{
		Text: delta.Text,
		Done: delta.Done,
	}
	if delta.Done {
		chunk.Stop = fromAgentStop(delta.StopReason)
	}

	// Emit the first tool call (singular) inline if present.
	if delta.ToolCall != nil {
		tc := fromAgentToolCall(*delta.ToolCall)
		chunk.ToolCall = &tc
	}

	// When the final delta carries multiple tool calls, buffer all of them
	// so they are emitted one-by-one before the Done chunk. The singular
	// ToolCall above covers the first one.
	if delta.Done && len(delta.ToolCalls) > 0 {
		s.pending = make([]core.ToolCall, 0, len(delta.ToolCalls))
		for _, t := range delta.ToolCalls {
			s.pending = append(s.pending, fromAgentToolCall(t))
		}
		// The chunk above already consumed delta.ToolCall (first element).
		// If delta.ToolCalls[0] == delta.ToolCall, skip it from pending.
		if len(s.pending) > 0 && delta.ToolCall != nil && s.pending[0].ID == delta.ToolCall.ID {
			s.pending = s.pending[1:]
		}
		// Don't set Done yet — tool calls must be emitted first.
		chunk.Done = false
		if len(s.pending) == 0 {
			// Only one tool call; emit Done on the next call.
			s.pendingDone = true
			s.finalStop = fromAgentStop(delta.StopReason)
		} else {
			s.pendingDone = true
			s.finalStop = fromAgentStop(delta.StopReason)
		}
	}

	return chunk, nil
}

func (s *legacyStream) Close() error {
	s.inner.Close()
	return nil
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

func fromAgentToolCall(in agent.ToolUseBlock) core.ToolCall {
	raw := json.RawMessage(in.Input)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return core.ToolCall{ID: in.ID, Name: in.Name, Input: raw}
}

func fromAgentToolCalls(in []agent.ToolUseBlock) []core.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]core.ToolCall, 0, len(in))
	for _, b := range in {
		out = append(out, fromAgentToolCall(b))
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

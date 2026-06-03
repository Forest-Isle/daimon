package tool

import (
	"context"
	"encoding/json"

	"github.com/Forest-Isle/IronClaw/internal/memorywire"
)

// AMPMemoryTool exposes the AMP (Agent Memory Protocol) wire format
// as a tool that the LLM can call. Wraps the memorywire.Adapter so
// that standardized memory operations (remember/recall/forget/merge/expire)
// are available to the agent through the normal tool execution pipeline.
type AMPMemoryTool struct {
	adapter *memorywire.Adapter
}

func NewAMPMemoryTool(adapter *memorywire.Adapter) *AMPMemoryTool {
	return &AMPMemoryTool{adapter: adapter}
}

func (t *AMPMemoryTool) Name() string        { return "amp_memory" }
func (t *AMPMemoryTool) Description() string { return "AMP-standard memory operations: remember, recall, forget, merge, expire. Use JSON wire format." }
func (t *AMPMemoryTool) RequiresApproval() bool { return false }

func (t *AMPMemoryTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{IsReadOnly: false}
}

func (t *AMPMemoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{"remember", "recall", "forget", "merge", "expire"},
			},
			"memory": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":    map[string]any{"type": "string", "enum": []string{"semantic", "episodic", "procedural", "emotional"}},
					"content": map[string]any{"type": "string"},
					"id":      map[string]any{"type": "string"},
					"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"query": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":  map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
			},
			"target_ids": map[string]any{
				"type": "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"operation"},
	}
}

func (t *AMPMemoryTool) Execute(ctx context.Context, input []byte) (Result, error) {
	req, err := memorywire.UnmarshalRequest(input)
	if err != nil {
		return Result{Error: "invalid AMP request: " + err.Error()}, nil
	}
	resp := t.adapter.Handle(ctx, *req)
	b, _ := json.Marshal(resp)
	if resp.Status == "error" {
		return Result{Error: resp.Error}, nil
	}
	return Result{Output: string(b)}, nil
}

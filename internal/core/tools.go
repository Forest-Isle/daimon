package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ToolSchema is the metadata the provider needs to expose a tool to the LLM.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Tool is the executable side of a tool.
type Tool interface {
	Schema() ToolSchema
	Execute(ctx context.Context, input json.RawMessage) (ToolResult, error)

	// ReadOnly tools have no side effects and are eligible for speculative
	// execution / parallel batches.
	ReadOnly() bool
}

// ToolFunc adapts a Go function to the Tool interface.
type ToolFunc struct {
	S            ToolSchema
	IsReadOnly   bool
	Fn           func(ctx context.Context, input json.RawMessage) (ToolResult, error)
}

func (t *ToolFunc) Schema() ToolSchema { return t.S }
func (t *ToolFunc) ReadOnly() bool     { return t.IsReadOnly }
func (t *ToolFunc) Execute(ctx context.Context, input json.RawMessage) (ToolResult, error) {
	return t.Fn(ctx, input)
}

// ToolRegistry holds tools by name. The zero value is not usable; use
// NewToolRegistry.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register stores or replaces a tool by its Schema().Name. The last
// registration wins, mirroring the legacy registry.
func (r *ToolRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Schema().Name] = t
}

// Lookup returns the tool with the given name.
func (r *ToolRegistry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// MustGet panics if the tool is absent. Useful in tests.
func (r *ToolRegistry) MustGet(name string) Tool {
	t, ok := r.Lookup(name)
	if !ok {
		panic(fmt.Sprintf("core: tool %q not registered", name))
	}
	return t
}

// Schemas returns the schemas of all registered tools, suitable for
// embedding in an LLMRequest. The slice is stable for a given registry
// state but unordered.
func (r *ToolRegistry) Schemas() []ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	return out
}

// Len returns the number of registered tools.
func (r *ToolRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

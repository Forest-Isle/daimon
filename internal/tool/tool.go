package tool

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Result is the output of a tool execution.
type Result struct {
	Output string
	Error  string
}

// Tool is the interface all tools must implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input []byte) (Result, error)
	RequiresApproval() bool
}

// Registry holds all registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return t, nil
}

func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// UnregisterByPrefix removes all tools whose name starts with prefix and returns the removed names.
func (r *Registry) UnregisterByPrefix(prefix string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []string
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
			removed = append(removed, name)
		}
	}
	return removed
}

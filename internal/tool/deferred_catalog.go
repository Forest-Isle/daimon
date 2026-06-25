package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DeferredToolSpec describes a tool that can be discovered before it is made
// available to the model as a callable tool.
type DeferredToolSpec struct {
	Name        string
	Description string
	Source      string
	Load        func(context.Context) (Tool, error)
}

// DeferredToolMatch is returned by catalog searches.
type DeferredToolMatch struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Source      string         `json:"source,omitempty"`
	Resolved    bool           `json:"resolved"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// DeferredCatalog stores discoverable tools outside the active tool registry.
// Registry contains tools the model can call now; DeferredCatalog contains tools
// the model can find and explicitly resolve through tool_search.
type DeferredCatalog struct {
	mu       sync.RWMutex
	entries  map[string]DeferredToolSpec
	resolved map[string]Tool
}

func NewDeferredCatalog() *DeferredCatalog {
	return &DeferredCatalog{
		entries:  make(map[string]DeferredToolSpec),
		resolved: make(map[string]Tool),
	}
}

func (c *DeferredCatalog) Add(spec DeferredToolSpec) error {
	if c == nil {
		return fmt.Errorf("deferred catalog is nil")
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("deferred tool name is required")
	}
	if spec.Load == nil {
		return fmt.Errorf("deferred tool %s has no loader", name)
	}
	spec.Name = name

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = spec
	delete(c.resolved, name)
	return nil
}

// RemoveByPrefix removes all deferred tools whose name starts with prefix and returns the removed names.
func (c *DeferredCatalog) RemoveByPrefix(prefix string) []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var removed []string
	for name := range c.entries {
		if strings.HasPrefix(name, prefix) {
			delete(c.entries, name)
			delete(c.resolved, name)
			removed = append(removed, name)
		}
	}
	return removed
}

func (c *DeferredCatalog) Search(query string, limit int) []DeferredToolMatch {
	if c == nil {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	query = strings.TrimSpace(strings.ToLower(query))
	terms := strings.Fields(query)

	type scored struct {
		spec     DeferredToolSpec
		resolved bool
		score    int
	}

	c.mu.RLock()
	items := make([]scored, 0, len(c.entries))
	for name, spec := range c.entries {
		score := deferredToolScore(spec, terms)
		if query != "" && score == 0 {
			continue
		}
		_, resolved := c.resolved[name]
		items = append(items, scored{spec: spec, resolved: resolved, score: score})
	}
	c.mu.RUnlock()

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].spec.Name < items[j].spec.Name
	})
	if len(items) > limit {
		items = items[:limit]
	}

	matches := make([]DeferredToolMatch, 0, len(items))
	for _, item := range items {
		matches = append(matches, DeferredToolMatch{
			Name:        item.spec.Name,
			Description: item.spec.Description,
			Source:      item.spec.Source,
			Resolved:    item.resolved,
		})
	}
	return matches
}

func deferredToolScore(spec DeferredToolSpec, terms []string) int {
	if len(terms) == 0 {
		return 1
	}
	name := strings.ToLower(spec.Name)
	description := strings.ToLower(spec.Description)
	source := strings.ToLower(spec.Source)

	score := 0
	for _, term := range terms {
		switch {
		case term == name:
			score += 100
		case strings.Contains(name, term):
			score += 30
		case strings.Contains(description, term):
			score += 10
		case strings.Contains(source, term):
			score += 5
		default:
			return 0
		}
	}
	return score
}

func (c *DeferredCatalog) Resolve(ctx context.Context, name string) (Tool, error) {
	if c == nil {
		return nil, fmt.Errorf("deferred catalog is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("deferred tool name is required")
	}

	c.mu.RLock()
	if t, ok := c.resolved[name]; ok {
		c.mu.RUnlock()
		return t, nil
	}
	spec, ok := c.entries[name]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("deferred tool not found: %s", name)
	}

	t, err := spec.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load deferred tool %s: %w", name, err)
	}
	if t == nil {
		return nil, fmt.Errorf("load deferred tool %s returned nil", name)
	}
	if t.Name() != name {
		return nil, fmt.Errorf("deferred tool %s loaded as %s", name, t.Name())
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.resolved[name]; ok {
		return cached, nil
	}
	c.resolved[name] = t
	return t, nil
}

func (c *DeferredCatalog) ResolveInto(ctx context.Context, registry *Registry, name string) (Tool, error) {
	t, err := c.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if registry != nil {
		registry.Register(t)
	}
	return t, nil
}

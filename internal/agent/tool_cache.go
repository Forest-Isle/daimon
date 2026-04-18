package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ToolResultCache is a per-task cache for read-only tool results.
// Write operations invalidate cached entries whose paths overlap.
type ToolResultCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	result tool.Result
	paths  []string
}

func NewToolResultCache() *ToolResultCache {
	return &ToolResultCache{
		entries: make(map[string]*cacheEntry),
	}
}

func cacheKey(toolName, input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%s:%x", toolName, h)
}

// Get returns the cached result for the given tool+input, if present.
func (c *ToolResultCache) Get(toolName, input string) (tool.Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[cacheKey(toolName, input)]
	if !ok {
		return tool.Result{}, false
	}
	return e.result, true
}

// Put stores a tool result, extracting filesystem paths from the input JSON
// so that future writes to those paths can invalidate this entry.
func (c *ToolResultCache) Put(toolName, input string, result tool.Result) {
	paths := extractPaths([]byte(input))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(toolName, input)] = &cacheEntry{
		result: result,
		paths:  paths,
	}
}

// InvalidatePath evicts every entry whose extracted paths overlap with path.
func (c *ToolResultCache) InvalidatePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		for _, p := range e.paths {
			if p == path {
				delete(c.entries, k)
				break
			}
		}
	}
}

// Clear evicts all entries.
func (c *ToolResultCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// extractPaths parses JSON input and collects values of path-like keys.
func extractPaths(input []byte) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return nil
	}
	pathKeys := []string{"path", "file_path", "directory"}
	var paths []string
	for _, key := range pathKeys {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			paths = append(paths, s)
		}
	}
	return paths
}

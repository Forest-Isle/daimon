package feature

import (
	"context"
	"fmt"
	"sync"
)

// Feature represents a toggleable feature flag.
type Feature struct {
	Name        string
	Description string
	Enabled     bool
	Reason      string // why it was disabled (empty if enabled)
}

// Registry manages feature flags at runtime.
type Registry struct {
	mu       sync.RWMutex
	features map[string]*Feature
}

// NewRegistry creates an empty feature registry.
func NewRegistry() *Registry {
	return &Registry{
		features: make(map[string]*Feature),
	}
}

// Register adds a feature to the registry.
func (r *Registry) Register(name, description string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[name] = &Feature{
		Name:        name,
		Description: description,
		Enabled:     enabled,
	}
}

// Enable turns on a feature by name.
func (r *Registry) Enable(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.features[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	f.Enabled = true
	f.Reason = ""
	return nil
}

// Disable turns off a feature by name with an optional reason.
func (r *Registry) Disable(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.features[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	f.Enabled = false
	f.Reason = "manually disabled"
	return nil
}

// List returns a snapshot of all registered features.
func (r *Registry) List() []Feature {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Feature, 0, len(r.features))
	for _, f := range r.features {
		out = append(out, *f)
	}
	return out
}

// IsEnabled checks if a feature is enabled.
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.features[name]
	return ok && f.Enabled
}

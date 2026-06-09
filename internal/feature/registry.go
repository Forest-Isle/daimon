package feature

import (
	"context"
	"sort"
)

// Registry holds feature definitions and their resolved states.
// Call Register during init, then Resolve once. After Resolve, the registry is
// read-only (IsEnabled, List, EnabledNames are safe for concurrent use).
type Registry struct {
	features map[string]Feature
	states   map[string]state
	order    []string // registration order
}

type state struct {
	enabled bool
	reason  string
}

// NewRegistry creates an empty feature registry.
func NewRegistry() *Registry {
	return &Registry{
		features: make(map[string]Feature),
		states:   make(map[string]state),
	}
}

// Register stores a feature definition. Must be called before Resolve.
func (r *Registry) Register(f Feature) {
	r.features[f.Name] = f
	r.order = append(r.order, f.Name)
}

// Resolve evaluates every feature against overrides and auto-detection.
// overrides comes from config YAML (feature.enabled fields).
func (r *Registry) Resolve(ctx context.Context, overrides map[string]bool) {
	for _, name := range r.order {
		f := r.features[name]
		s := state{}

		// 1. Auto-detect: unavailable → off regardless
		if f.AutoDetect != nil && !f.AutoDetect(ctx) {
			s.enabled = false
			s.reason = "auto-detect: not available"
			r.states[name] = s
			continue
		}

		// 2. Override > default
		if v, ok := overrides[name]; ok {
			s.enabled = v
			if v {
				s.reason = "enabled by config"
			} else {
				s.reason = "disabled by config"
			}
		} else {
			s.enabled = f.Default
			if f.Default {
				s.reason = "enabled"
			} else {
				s.reason = "disabled"
			}
		}

		r.states[name] = s
	}

	// Sole hard dependency: team requires multi_agent
	if s, ok := r.states["team"]; ok && s.enabled {
		if ms, ok := r.states["multi_agent"]; ok && !ms.enabled {
			s.enabled = false
			s.reason = "dependency multi_agent is disabled"
			r.states["team"] = s
		}
	}
}

// IsEnabled reports whether a feature is enabled. Safe for concurrent use
// after Resolve has returned.
func (r *Registry) IsEnabled(name string) bool {
	if s, ok := r.states[name]; ok {
		return s.enabled
	}
	return false
}

// List returns all features in registration order.
func (r *Registry) List() []Info {
	result := make([]Info, 0, len(r.order))
	for _, name := range r.order {
		f := r.features[name]
		s := r.states[name]
		result = append(result, Info{
			Name:        f.Name,
			Description: f.Description,
			Enabled:     s.enabled,
			Reason:      s.reason,
		})
	}
	return result
}

// EnabledNames returns names of all enabled features, sorted.
func (r *Registry) EnabledNames() []string {
	var names []string
	for name, s := range r.states {
		if s.enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

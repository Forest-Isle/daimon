package feature

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry holds feature definitions and their resolved states.
// Call Register during init, then Resolve once. Read and runtime override
// methods are safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[f.Name] = f
	r.order = append(r.order, f.Name)
}

// Resolve evaluates every feature against overrides and auto-detection.
// overrides comes from config YAML (feature.enabled fields).
func (r *Registry) Resolve(ctx context.Context, overrides map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
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

	r.applyDependencies()
}

// IsEnabled reports whether a feature is enabled. Safe for concurrent use
// after Resolve has returned.
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.states[name]; ok {
		return s.enabled
	}
	return false
}

// Has reports whether a feature has been registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.features[name]
	return ok
}

// Set updates a resolved feature state. It is intended for user-facing runtime
// overrides; callers remain responsible for starting/stopping subsystems whose
// wiring only happens during Gateway initialization.
func (r *Registry) Set(name string, enabled bool, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.features[name]; !ok {
		return fmt.Errorf("unknown feature: %s", name)
	}
	if reason == "" {
		if enabled {
			reason = "enabled"
		} else {
			reason = "disabled"
		}
	}
	r.states[name] = state{enabled: enabled, reason: reason}
	r.applyDependencies()
	return nil
}

// List returns all features in registration order.
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, s := range r.states {
		if s.enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Registry) applyDependencies() {
	// Sole hard dependency: team requires multi_agent
	if s, ok := r.states["team"]; ok && s.enabled {
		if ms, ok := r.states["multi_agent"]; ok && !ms.enabled {
			s.enabled = false
			s.reason = "dependency multi_agent is disabled"
			r.states["team"] = s
		}
	}
}

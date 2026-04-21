package feature

import (
	"context"
	"fmt"
	"log"
	"sync"
)

type featureState struct {
	feature  Feature
	enabled  bool
	reason   string
	override *bool // nil = no override
}

// Registry holds feature definitions and their runtime states.
type Registry struct {
	mu     sync.RWMutex
	states map[string]*featureState
	order  []string // populated by ResolveAndInit (topological order)
}

func NewRegistry() *Registry {
	return &Registry{
		states: make(map[string]*featureState),
	}
}

// Register stores a feature definition. Must be called before ResolveAndInit.
func (r *Registry) Register(f Feature) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[f.Name] = &featureState{
		feature: f,
		enabled: false,
		reason:  "not initialized",
	}
}

// ApplyOverrides applies config-file overrides before ResolveAndInit.
func (r *Registry) ApplyOverrides(overrides map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, enabled := range overrides {
		if st, ok := r.states[name]; ok {
			v := enabled
			st.override = &v
		}
	}
}

// ResolveAndInit performs topological sort and initializes features in dependency order.
func (r *Registry) ResolveAndInit(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sorted, err := r.topoSort()
	if err != nil {
		return err
	}
	r.order = sorted

	for _, name := range sorted {
		st := r.states[name]
		r.resolveFeature(ctx, st)
	}
	return nil
}

// resolveFeature decides whether a single feature should be enabled, then calls OnEnable.
// Must be called with r.mu held.
func (r *Registry) resolveFeature(ctx context.Context, st *featureState) {
	if st.feature.AutoDetect != nil {
		result := st.feature.AutoDetect(ctx)
		if !result.Available {
			st.enabled = false
			st.reason = "auto-detect: " + result.Reason
			return
		}
	}

	wanted := st.feature.Default
	if st.override != nil {
		wanted = *st.override
	}
	if !wanted {
		st.enabled = false
		st.reason = "disabled by configuration"
		return
	}

	for _, dep := range st.feature.Dependencies {
		if depSt, ok := r.states[dep]; ok && !depSt.enabled {
			st.enabled = false
			st.reason = fmt.Sprintf("dependency %q is disabled", dep)
			return
		}
	}

	if st.feature.OnEnable != nil {
		if err := st.feature.OnEnable(ctx); err != nil {
			st.enabled = false
			st.reason = "OnEnable failed: " + err.Error()
			log.Printf("[feature] %s: OnEnable failed: %v", st.feature.Name, err)
			return
		}
	}

	st.enabled = true
	st.reason = "enabled"
}

// topoSort returns feature names in topological order using Kahn's algorithm.
// Must be called with r.mu held.
func (r *Registry) topoSort() ([]string, error) {
	inDegree := make(map[string]int, len(r.states))
	dependents := make(map[string][]string, len(r.states))

	for name := range r.states {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
	}

	for name, st := range r.states {
		for _, dep := range st.feature.Dependencies {
			if _, ok := r.states[dep]; !ok {
				return nil, fmt.Errorf("feature %q has unknown dependency %q", name, dep)
			}
			dependents[dep] = append(dependents[dep], name)
			inDegree[name]++
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, node)
		for _, dep := range dependents[node] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(r.states) {
		return nil, fmt.Errorf("circular dependency detected among features")
	}
	return sorted, nil
}

// SetOnEnable sets or replaces the OnEnable hook for a feature.
// Used for late-binding when the hook needs access to objects created after registration.
func (r *Registry) SetOnEnable(name string, fn func(ctx context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	st.feature.OnEnable = fn
	return nil
}

// SetOnDisable sets or replaces the OnDisable hook for a feature.
func (r *Registry) SetOnDisable(name string, fn func(ctx context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	st.feature.OnDisable = fn
	return nil
}

// IsEnabled reports whether a feature is currently enabled. Thread-safe.
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if st, ok := r.states[name]; ok {
		return st.enabled
	}
	return false
}

// Enable activates a previously disabled feature at runtime.
func (r *Registry) Enable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	if st.enabled {
		return nil
	}

	if st.feature.AutoDetect != nil {
		result := st.feature.AutoDetect(ctx)
		if !result.Available {
			return fmt.Errorf("feature %q not available: %s", name, result.Reason)
		}
	}

	for _, dep := range st.feature.Dependencies {
		if depSt, ok := r.states[dep]; ok && !depSt.enabled {
			return fmt.Errorf("dependency %q is not enabled", dep)
		}
	}

	if st.feature.OnEnable != nil {
		if err := st.feature.OnEnable(ctx); err != nil {
			return fmt.Errorf("OnEnable for %q failed: %w", name, err)
		}
	}

	st.enabled = true
	st.reason = "enabled at runtime"
	return nil
}

// Disable deactivates an enabled feature at runtime.
// Fails if another enabled feature depends on this one.
func (r *Registry) Disable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	if !st.enabled {
		return nil
	}

	for otherName, otherSt := range r.states {
		if !otherSt.enabled {
			continue
		}
		for _, dep := range otherSt.feature.Dependencies {
			if dep == name {
				return fmt.Errorf("cannot disable %q: feature %q depends on it", name, otherName)
			}
		}
	}

	if st.feature.OnDisable != nil {
		if err := st.feature.OnDisable(ctx); err != nil {
			return fmt.Errorf("OnDisable for %q failed: %w", name, err)
		}
	}

	st.enabled = false
	st.reason = "disabled at runtime"
	return nil
}

// List returns info for all features in initialization order.
func (r *Registry) List() []FeatureInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]FeatureInfo, 0, len(r.order))
	for _, name := range r.order {
		st := r.states[name]
		result = append(result, FeatureInfo{
			Name:          st.feature.Name,
			Description:   st.feature.Description,
			Enabled:       st.enabled,
			Reason:        st.reason,
			Phase:         st.feature.Phase,
			Dependencies:  st.feature.Dependencies,
			HotReloadable: st.feature.HotReloadable,
		})
	}
	return result
}

// SetOnEnable replaces the OnEnable hook for a registered feature.
// Used for late-binding hooks that need access to subsystems not available at
// registration time.
func (r *Registry) SetOnEnable(name string, fn func(ctx context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	st.feature.OnEnable = fn
	return nil
}

// SetOnDisable replaces the OnDisable hook for a registered feature.
func (r *Registry) SetOnDisable(name string, fn func(ctx context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	st.feature.OnDisable = fn
	return nil
}

// EnabledNames returns the names of all currently enabled features.
func (r *Registry) EnabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for _, name := range r.order {
		if r.states[name].enabled {
			names = append(names, name)
		}
	}
	return names
}

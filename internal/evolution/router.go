package evolution

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// ModelRoute maps a task complexity level to a specific LLM model.
type ModelRoute struct {
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"` // 0 = use default
}

// RouterConfig configures model routing by task complexity.
type RouterConfig struct {
	Enabled  bool       `yaml:"enabled"`
	Simple   ModelRoute `yaml:"simple"`
	Moderate ModelRoute `yaml:"moderate"`
	Complex  ModelRoute `yaml:"complex"`
}

// DefaultRouterConfig returns a disabled router config. When enabled, the
// caller must fill in actual model names.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{Enabled: false}
}

// routeStats tracks per-route success/failure counts for observability.
type routeStats struct {
	uses      int
	successes int
}

// ModelRouter selects the optimal LLM model for a given task complexity.
// It maintains usage statistics for each route to inform future optimizations.
type ModelRouter struct {
	cfg   RouterConfig
	stats map[string]*routeStats // keyed by complexity
	mu    sync.Mutex
}

// NewModelRouter creates a model router with the given config.
func NewModelRouter(cfg RouterConfig) *ModelRouter {
	return &ModelRouter{
		cfg: cfg,
		stats: map[string]*routeStats{
			"simple":   {},
			"moderate": {},
			"complex":  {},
		},
	}
}

// RouteResult carries the selected model and optional max_tokens override.
type RouteResult struct {
	Model     string
	MaxTokens int  // 0 means use default
	Routed    bool // true if a non-default model was selected
}

// SelectModel picks the model for the given complexity. Returns an empty
// RouteResult (Routed=false) if routing is disabled or no override exists.
func (mr *ModelRouter) SelectModel(complexity string) RouteResult {
	if !mr.cfg.Enabled {
		return RouteResult{}
	}

	var route ModelRoute
	switch strings.ToLower(complexity) {
	case "simple":
		route = mr.cfg.Simple
	case "moderate":
		route = mr.cfg.Moderate
	case "complex":
		route = mr.cfg.Complex
	default:
		return RouteResult{}
	}

	if route.Model == "" {
		return RouteResult{}
	}

	mr.mu.Lock()
	if s, ok := mr.stats[complexity]; ok {
		s.uses++
	}
	mr.mu.Unlock()

	slog.Debug("model_router: routed",
		"complexity", complexity,
		"model", route.Model,
	)

	return RouteResult{
		Model:     route.Model,
		MaxTokens: route.MaxTokens,
		Routed:    true,
	}
}

// RecordOutcome records whether a routed task succeeded.
func (mr *ModelRouter) RecordOutcome(complexity string, succeeded bool) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if s, ok := mr.stats[complexity]; ok {
		if succeeded {
			s.successes++
		}
	}
}

// Stats returns a formatted summary of routing statistics.
func (mr *ModelRouter) Stats() string {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	var b strings.Builder
	for _, level := range []string{"simple", "moderate", "complex"} {
		s := mr.stats[level]
		if s.uses == 0 {
			continue
		}
		sr := float64(s.successes) / float64(s.uses) * 100
		fmt.Fprintf(&b, "  %s: %d uses, %.0f%% success\n", level, s.uses, sr)
	}
	if b.Len() == 0 {
		return "  (no routing data yet)\n"
	}
	return b.String()
}

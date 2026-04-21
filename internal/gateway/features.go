package gateway

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/feature"
	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

func registerFeatures(cfg *config.Config) *feature.Registry {
	r := feature.NewRegistry()

	// Tier 1: Default ON, no external dependencies
	r.Register(feature.Feature{
		Name:        "memory",
		Description: "Memory system with file storage and fact extraction",
		Default:     true,
		Phase:       feature.PhaseConstruct,
	})
	r.Register(feature.Feature{
		Name:        "skills",
		Description: "SKILL.md loading and read_skill tool",
		Default:     true,
		Phase:       feature.PhaseConstruct,
	})
	r.Register(feature.Feature{
		Name:        "multi_agent",
		Description: "Sub-agent spawning and orchestration",
		Default:     true,
		Phase:       feature.PhaseConstruct,
	})
	r.Register(feature.Feature{
		Name:         "team",
		Description:  "Team coordinator for /team command",
		Default:      true,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"multi_agent"},
	})
	r.Register(feature.Feature{
		Name:        "speculative",
		Description: "Read-only tool pre-execution during streaming",
		Default:     true,
		Phase:       feature.PhaseConstruct,
	})
	r.Register(feature.Feature{
		Name:        "scheduler",
		Description: "Scheduled task execution",
		Default:     true,
		Phase:       feature.PhaseStart,
	})

	// Tier 2: AutoDetect driven
	r.Register(feature.Feature{
		Name:         "knowledge",
		Description:  "Document ingestion and hybrid retrieval",
		Default:      true,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"memory"},
		AutoDetect: func(ctx context.Context) feature.DetectResult {
			// Knowledge works without OpenAI key (BM25-only fallback via noopKBEmbedder)
			return feature.DetectResult{Available: true}
		},
	})
	r.Register(feature.Feature{
		Name:         "knowledge_graph",
		Description:  "Entity/relation extraction and graph traversal",
		Default:      true,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"knowledge"},
	})
	r.Register(feature.Feature{
		Name:         "reranker",
		Description:  "LLM-based search result reranking",
		Default:      true,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"knowledge"},
	})
	r.Register(feature.Feature{
		Name:        "sandbox",
		Description: "Docker container isolation for bash execution",
		Default:     true,
		Phase:       feature.PhaseConstruct,
		AutoDetect: func(ctx context.Context) feature.DetectResult {
			if !sandbox.ProbeDocker(ctx) {
				return feature.DetectResult{Available: false, Reason: "Docker not available"}
			}
			return feature.DetectResult{Available: true}
		},
	})

	// Tier 3: Opt-in (default false)
	r.Register(feature.Feature{
		Name:          "evolution",
		Description:   "Self-evolution engine (preference learning, skill synthesis)",
		Default:       false,
		Phase:         feature.PhaseStart,
		HotReloadable: true,
	})
	r.Register(feature.Feature{
		Name:         "rl",
		Description:  "Reinforcement learning system",
		Default:      false,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"evolution"},
	})
	r.Register(feature.Feature{
		Name:         "model_routing",
		Description:  "Dynamic model selection by task complexity",
		Default:      false,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"evolution"},
	})
	r.Register(feature.Feature{
		Name:          "dashboard",
		Description:   "Web dashboard for real-time agent monitoring",
		Default:       false,
		Phase:         feature.PhaseConstruct,
		HotReloadable: true,
	})
	r.Register(feature.Feature{
		Name:        "server",
		Description: "Standalone HTTP admin server",
		Default:     false,
		Phase:       feature.PhaseConstruct,
	})

	return r
}

// bindFeatureLifecycleHooks wires OnEnable/OnDisable hooks for hot-reloadable
// features. Called after all subsystems are initialized so that hooks can
// reference Gateway fields (dashboard server, evolution engine, etc.).
func (gw *Gateway) bindFeatureLifecycleHooks() {
	_ = gw.features.SetOnEnable("dashboard", func(ctx context.Context) error {
		return gw.startDashboard()
	})
	_ = gw.features.SetOnDisable("dashboard", func(ctx context.Context) error {
		return gw.stopDashboard()
	})

	_ = gw.features.SetOnEnable("evolution", func(ctx context.Context) error {
		if gw.evoEngine != nil {
			gw.evoEngine.Start()
		}
		return nil
	})
	_ = gw.features.SetOnDisable("evolution", func(ctx context.Context) error {
		if gw.evoEngine != nil {
			gw.evoEngine.Stop()
		}
		return nil
	})
}

func configToOverrides(cfg *config.Config) map[string]bool {
	return map[string]bool{
		"memory":          cfg.Memory.Enabled,
		"skills":          cfg.Skills.Enabled,
		"multi_agent":     cfg.Agents.Enabled,
		"team":            cfg.Agent.Team.Enabled,
		"speculative":     cfg.Agent.SpeculativeExecution.Enabled,
		"scheduler":       cfg.Scheduler.Enabled,
		"knowledge":       cfg.Knowledge.Enabled,
		"knowledge_graph": cfg.Knowledge.GraphEnabled || cfg.Graph.Enabled,
		"reranker":        cfg.Knowledge.Reranker.Enabled,
		"sandbox":         cfg.Sandbox.Enabled,
		"evolution":       cfg.Evolution.Enabled,
		"rl":              cfg.Agent.RL.Enabled,
		"model_routing":   cfg.Evolution.Router.Enabled,
		"dashboard":       cfg.Dashboard.Enabled,
		"server":          cfg.Server.Enabled,
	}
}

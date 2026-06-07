package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/feature"
	"github.com/Forest-Isle/IronClaw/internal/sandbox"
	"github.com/Forest-Isle/IronClaw/internal/worktree"
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
		Name:          "scheduler",
		Description:   "Scheduled task execution",
		Default:       true,
		Phase:         feature.PhaseStart,
		HotReloadable: true,
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
		Name:         "model_routing",
		Description:  "Dynamic model selection by task complexity",
		Default:      false,
		Phase:        feature.PhaseConstruct,
		Dependencies: []string{"evolution"},
	})
	r.Register(feature.Feature{
		Name:        "server",
		Description: "Standalone HTTP admin server",
		Default:     false,
		Phase:       feature.PhaseConstruct,
	})
	r.Register(feature.Feature{
		Name:        "worktree",
		Description: "Git worktree-based code isolation for safe file modifications",
		Default:     true,
		Phase:       feature.PhaseConstruct,
		AutoDetect: func(ctx context.Context) feature.DetectResult {
			if !worktree.Available() {
				return feature.DetectResult{Available: false, Reason: "git not found in PATH"}
			}
			return feature.DetectResult{Available: true}
		},
	})
	// "graph" feature removed — graph engine eliminated in Phase 1 optimization

	// MCP servers — each configured server gets its own hot-reloadable feature
	for name, srv := range cfg.Tools.MCP.Servers {
		name := name // capture loop variable
		srv := srv
		cmdName := srv.Command
		r.Register(feature.Feature{
			Name:          "mcp_" + name,
			Description:   fmt.Sprintf("MCP server: %s (%s)", name, cmdName),
			Default:       true,
			Phase:         feature.PhaseStart,
			HotReloadable: true,
			AutoDetect: func(ctx context.Context) feature.DetectResult {
				// Don't fail auto-detect for missing command — MCP startup gives better error messages.
				return feature.DetectResult{Available: true}
			},
		})
	}

	return r
}

// bindFeatureLifecycleHooks wires OnEnable/OnDisable hooks for hot-reloadable
// features. Called after all subsystems are initialized so that hooks can
// reference Gateway fields (evolution engine, etc.).
func (gw *Gateway) bindFeatureLifecycleHooks() {
	if err := gw.features.SetOnEnable("evolution", func(ctx context.Context) error {
		if gw.evolution.engine != nil {
			gw.evolution.engine.Start()
		}
		return nil
	}); err != nil {
		slog.Warn("gateway: SetOnEnable hook failed", "feature", "evolution", "err", err)
	}
	if err := gw.features.SetOnDisable("evolution", func(ctx context.Context) error {
		if gw.evolution.engine != nil {
			gw.evolution.engine.Stop()
		}
		return nil
	}); err != nil {
		slog.Warn("gateway: SetOnDisable hook failed", "feature", "evolution", "err", err)
	}

	if err := gw.features.SetOnEnable("scheduler", func(ctx context.Context) error {
		if gw.channels.sched != nil {
			gw.channels.sched.Start(ctx)
		}
		return nil
	}); err != nil {
		slog.Warn("gateway: SetOnEnable hook failed", "feature", "scheduler", "err", err)
	}
	if err := gw.features.SetOnDisable("scheduler", func(ctx context.Context) error {
		if gw.channels.sched != nil {
			gw.channels.sched.Stop()
		}
		return nil
	}); err != nil {
		slog.Warn("gateway: SetOnDisable hook failed", "feature", "scheduler", "err", err)
	}

	// MCP servers — bind start/stop hooks for each registered mcp_* feature
	for _, srv := range gw.features.List() {
		if !strings.HasPrefix(srv.Name, "mcp_") {
			continue
		}
		serverName := strings.TrimPrefix(srv.Name, "mcp_")
		srvCfg, ok := gw.Config().Tools.MCP.Servers[serverName]
		if !ok {
			continue
		}
		sName := serverName
		sCfg := srvCfg
		if err := gw.features.SetOnEnable("mcp_"+sName, func(ctx context.Context) error {
			return gw.mcpManager.StartServer(ctx, sName, sCfg, gw.tools)
		}); err != nil {
			slog.Warn("gateway: SetOnEnable hook failed", "feature", "mcp_"+sName, "err", err)
		}
		if err := gw.features.SetOnDisable("mcp_"+sName, func(ctx context.Context) error {
			gw.mcpManager.StopServer(sName, gw.tools)
			return nil
		}); err != nil {
			slog.Warn("gateway: SetOnDisable hook failed", "feature", "mcp_"+sName, "err", err)
		}
	}
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
		"model_routing":   cfg.Evolution.Router.Enabled,
		"server":          cfg.Server.Enabled,
	}
}

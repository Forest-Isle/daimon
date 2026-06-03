package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

func (gw *Gateway) initMultiAgent() error {
	cfg := gw.Config()
	// Multi-agent system
	if gw.featureEnabled("multi_agent") {
		// Set MultiAgent deps on gw.agentDeps first, then create SubAgentManager
		// so that the deps copy includes all sub-fields.

		// Background agent manager
		bgManager := agent.NewBackgroundManager()
		gw.agentDeps.MultiAgent.BgManager = bgManager

		// Prompt cache for sub-agents
		promptCache := agent.NewPromptCache()
		gw.agentDeps.MultiAgent.PromptCache = promptCache

		// Per-agent MCP manager
		agentMCPMgr := agent.NewAgentMCPManager(nil)
		gw.agentDeps.MultiAgent.AgentMCP = agentMCPMgr

		// Agent orchestrator for parallel scheduling
		orchestrator := agent.NewAgentOrchestrator(agent.NewAgentManager(
			gw.provider, gw.sessions, gw.db, gw.memory.Store(), gw.tools, cfg.Agent, cfg.LLM,
		), 4)
		gw.agentDeps.MultiAgent.Orchestrator = orchestrator

		// Now create SubAgentManager with fully populated deps
		deps := gw.agentDeps
		deps.Core.AgentID = "subagent-manager"

		subAgentMgr := agent.NewSubAgentManager(deps)
		gw.tasks.subAgentMgr = subAgentMgr
		gw.agentDeps.MultiAgent.SubAgentMgr = subAgentMgr

		// Agent manager for loading agent specs
		agentMgr := agent.NewAgentManager(
			gw.provider, gw.sessions, gw.db, gw.memory.Store(), gw.tools, cfg.Agent, cfg.LLM,
		)

		_ = agentMgr.LoadDir(userdir.AgentsDir())
		for _, dir := range cfg.Agents.ExtraDirs {
			if err := agentMgr.LoadDir(dir); err != nil {
				slog.Warn("gateway: failed to load agents from extra dir", "dir", dir, "err", err)
			}
		}
		for _, def := range cfg.Agents.Definitions {
			if err := agentMgr.Add(defToSpec(def)); err != nil {
				slog.Warn("gateway: failed to add inline agent definition", "name", def.Name, "err", err)
			}
		}
		agentMgr.RegisterAll(gw.tools)
		gw.agentDeps.MultiAgent.AgentMgr = agentMgr

		agentMgr.SetAgentMCPManager(agentMCPMgr)

		// Team coordination manager
		if cfg.Agent.Team.Enabled {
			teamMgr := agent.NewTeamManager(subAgentMgr)
			gw.tasks.teamManager = teamMgr
			slog.Info("agent team manager initialized", "max_workers", cfg.Agent.Team.MaxWorkers)
		}

		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Speculative execution of read-only tools during streaming
	if gw.featureEnabled("speculative") {
		maxInFlight := cfg.Agent.SpeculativeExecution.MaxInFlight
		se := agent.NewSpeculativeExecutor(gw.tools, maxInFlight)
		gw.agentDeps.MultiAgent.Speculative = se
		slog.Info("speculative execution enabled", "max_in_flight", maxInFlight)
	}

	// Compression pipeline and context manager
	contextWindow := agent.ModelContextWindow(cfg.LLM.Model)

	if cfg.Agent.Compression.Strategy == "layered" {
		_ = agent.NewCompressionPipeline(
			gw.provider, cfg.LLM.Model, cfg.Agent.Compression, gw.resultStore, contextWindow,
		)
		slog.Info("layered compression pipeline enabled")

		tokenBudget := agent.NewTokenBudget(
			contextWindow,
			float64(cfg.Agent.Compression.Layers.ToolOutputReducePct)/100.0,
			float64(cfg.Agent.Compression.Layers.SummarizePct)/100.0,
			float64(cfg.Agent.Compression.Layers.EmergencyPct)/100.0,
			cfg.Agent.Compression.TokenEstimateRatio,
		)
		_ = tokenBudget
		slog.Info("token budget monitor enabled",
			"model", cfg.LLM.Model,
			"model_limit", contextWindow,
			"light_pct", cfg.Agent.Compression.Layers.ToolOutputReducePct,
			"medium_pct", cfg.Agent.Compression.Layers.SummarizePct,
			"heavy_pct", cfg.Agent.Compression.Layers.EmergencyPct,
		)
	}

	// ContextManager is always created — it handles both layered (via pipeline)
	// and non-layered (via CompactHistory fallback) compression, and enables
	// reactive 413 retry regardless of compression strategy.
	contextMgr := agent.NewPipelineContextManager(
		gw.provider,
		cfg.LLM.Model,
		&cfg.Agent.Compression,
		contextWindow,
		gw.resultStore,
	)
	gw.contextMgr = contextMgr
	gw.agentDeps.Memory.ContextMgr = contextMgr
	slog.Info("context manager initialized", "strategy", cfg.Agent.Compression.Strategy)

	return nil
}

package gateway

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

func (gw *Gateway) initMultiAgent() error {
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
			gw.provider, gw.sessions, gw.db, gw.memory.Store(), gw.tools, gw.cfg.Agent, gw.cfg.LLM,
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
			gw.provider, gw.sessions, gw.db, gw.memory.Store(), gw.tools, gw.cfg.Agent, gw.cfg.LLM,
		)

		_ = agentMgr.LoadDir(userdir.AgentsDir())
		for _, dir := range gw.cfg.Agents.ExtraDirs {
			if err := agentMgr.LoadDir(dir); err != nil {
				slog.Warn("gateway: failed to load agents from extra dir", "dir", dir, "err", err)
			}
		}
		for _, def := range gw.cfg.Agents.Definitions {
			if err := agentMgr.Add(defToSpec(def)); err != nil {
				slog.Warn("gateway: failed to add inline agent definition", "name", def.Name, "err", err)
			}
		}
		agentMgr.RegisterAll(gw.tools)
		gw.agentDeps.MultiAgent.AgentMgr = agentMgr

		// Sidechain store for sub-agent execution history persistence
		if home, err := os.UserHomeDir(); err == nil {
			sidechainDir := filepath.Join(home, ".IronClaw", "data", "sidechain")
			sidechainStore, err := agent.NewFileSidechainStore(sidechainDir)
			if err != nil {
				slog.Warn("gateway: sidechain store init failed, sub-agent history will not persist", "err", err)
			} else {
				agentMgr.SetSidechainStore(sidechainStore)
				slog.Info("sidechain store initialized", "dir", sidechainDir)
			}
		}

		agentMgr.SetAgentMCPManager(agentMCPMgr)

		if gw.cognitiveLoop != nil {
			gw.cognitiveLoop.SetDebateConfig(gw.cfg.Agents.Debate)
		}

		// Team coordination manager
		if gw.cfg.Agent.Team.Enabled {
			teamMgr := agent.NewTeamManager(subAgentMgr)
			gw.tasks.teamManager = teamMgr
			slog.Info("agent team manager initialized", "max_workers", gw.cfg.Agent.Team.MaxWorkers)
		}

		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Speculative execution of read-only tools during streaming
	if gw.featureEnabled("speculative") {
		maxInFlight := gw.cfg.Agent.SpeculativeExecution.MaxInFlight
		se := agent.NewSpeculativeExecutor(gw.tools, maxInFlight)
		gw.agentDeps.MultiAgent.Speculative = se
		slog.Info("speculative execution enabled", "max_in_flight", maxInFlight)
	}

	// Compression pipeline and context manager
	contextWindow := agent.ModelContextWindow(gw.cfg.LLM.Model)

	if gw.cfg.Agent.Compression.Strategy == "layered" {
		_ = agent.NewCompressionPipeline(
			gw.provider, gw.cfg.LLM.Model, gw.cfg.Agent.Compression, gw.resultStore, contextWindow,
		)
		slog.Info("layered compression pipeline enabled")

		tokenBudget := agent.NewTokenBudget(
			contextWindow,
			float64(gw.cfg.Agent.Compression.Layers.ToolEvictionPct)/100.0,
			float64(gw.cfg.Agent.Compression.Layers.SummarizePct)/100.0,
			float64(gw.cfg.Agent.Compression.Layers.SlimPromptPct)/100.0,
			gw.cfg.Agent.Compression.TokenEstimateRatio,
		)
		_ = tokenBudget
		slog.Info("token budget monitor enabled",
			"model", gw.cfg.LLM.Model,
			"model_limit", contextWindow,
			"light_pct", gw.cfg.Agent.Compression.Layers.ToolEvictionPct,
			"medium_pct", gw.cfg.Agent.Compression.Layers.SummarizePct,
			"heavy_pct", gw.cfg.Agent.Compression.Layers.SlimPromptPct,
		)
	}

	// ContextManager is always created — it handles both layered (via pipeline)
	// and non-layered (via CompactHistory fallback) compression, and enables
	// reactive 413 retry regardless of compression strategy.
	contextMgr := agent.NewPipelineContextManager(
		gw.provider,
		gw.cfg.LLM.Model,
		&gw.cfg.Agent.Compression,
		contextWindow,
		gw.resultStore,
	)
	gw.contextMgr = contextMgr
	gw.agentDeps.Memory.ContextMgr = contextMgr
	slog.Info("context manager initialized", "strategy", gw.cfg.Agent.Compression.Strategy)

	return nil
}

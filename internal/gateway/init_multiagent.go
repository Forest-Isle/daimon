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
	if gw.cfg.Agents.Enabled {
		agentMgr := agent.NewAgentManager(gw.provider, gw.sessions, gw.db, gw.memStore, gw.tools, gw.cfg.Agent, gw.cfg.LLM)

		subAgentMgr := agent.NewSubAgentManager(gw.provider, gw.sessions, gw.db, gw.memStore, gw.tools, gw.cfg.Agent, gw.cfg.LLM)
		gw.subAgentMgr = subAgentMgr
		agentMgr.SetSubAgentManager(subAgentMgr)

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
		gw.runtime.SetAgentManager(agentMgr)
		// Initialize sidechain store for sub-agent execution history persistence
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
		// Background agent manager
		bgManager := agent.NewBackgroundManager()
		agentMgr.SetBackgroundManager(bgManager)
		subAgentMgr.SetBackgroundManager(bgManager)
		gw.runtime.SetBackgroundManager(bgManager)
		slog.Info("background agent manager initialized")
		// Prompt cache for sub-agents
		promptCache := agent.NewPromptCache()
		gw.runtime.SetPromptCache(promptCache)
		slog.Info("agent prompt cache initialized")
		// Per-agent MCP manager
		agentMCPMgr := agent.NewAgentMCPManager(nil)
		agentMgr.SetAgentMCPManager(agentMCPMgr)
		subAgentMgr.SetAgentMCPManager(agentMCPMgr)
		gw.runtime.SetAgentMCPManager(agentMCPMgr)
		slog.Info("per-agent MCP manager initialized")
		// Agent orchestrator for parallel scheduling
		orchestrator := agent.NewAgentOrchestrator(agentMgr, 4)
		gw.runtime.SetOrchestrator(orchestrator)
		slog.Info("agent orchestrator initialized", "max_parallel", 4)
		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetAgentManager(agentMgr)
		}
		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetOrchestrator(orchestrator)
		}
		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetDebateConfig(gw.cfg.Agents.Debate)
		}
		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Speculative execution of read-only tools during streaming
	if gw.cfg.Agent.SpeculativeExecution.Enabled {
		maxInFlight := gw.cfg.Agent.SpeculativeExecution.MaxInFlight
		se := agent.NewSpeculativeExecutor(gw.tools, maxInFlight)
		gw.runtime.SetSpeculativeExecutor(se)
		slog.Info("speculative execution enabled", "max_in_flight", maxInFlight)
	}

	// Compression pipeline and context manager
	contextWindow := agent.ModelContextWindow(gw.cfg.LLM.Model)

	if gw.cfg.Agent.Compression.Strategy == "layered" {
		pipeline := agent.NewCompressionPipeline(
			gw.provider, gw.cfg.LLM.Model, gw.cfg.Agent.Compression, gw.resultStore, contextWindow,
		)
		gw.runtime.SetCompressionPipeline(pipeline)
		slog.Info("layered compression pipeline enabled")

		tokenBudget := agent.NewTokenBudget(
			contextWindow,
			float64(gw.cfg.Agent.Compression.Layers.ToolEvictionPct)/100.0,
			float64(gw.cfg.Agent.Compression.Layers.SummarizePct)/100.0,
			float64(gw.cfg.Agent.Compression.Layers.SlimPromptPct)/100.0,
			gw.cfg.Agent.Compression.TokenEstimateRatio,
		)
		gw.runtime.SetTokenBudget(tokenBudget)
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
	gw.runtime.SetContextManager(contextMgr)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetContextManager(contextMgr)
	}
	slog.Info("context manager initialized", "strategy", gw.cfg.Agent.Compression.Strategy)

	return nil
}

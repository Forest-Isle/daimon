package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

func (gw *Gateway) initMultiAgent() error {
	// Multi-agent system
	if gw.cfg.Agents.Enabled {
		agentMgr := agent.NewAgentManager(gw.provider, gw.sessions, gw.db, gw.memStore, gw.tools, gw.cfg.Agent, gw.cfg.LLM)
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
		// Background agent manager
		bgManager := agent.NewBackgroundManager()
		agentMgr.SetBackgroundManager(bgManager)
		gw.runtime.SetBackgroundManager(bgManager)
		slog.Info("background agent manager initialized")
		// Prompt cache for sub-agents
		promptCache := agent.NewPromptCache()
		gw.runtime.SetPromptCache(promptCache)
		slog.Info("agent prompt cache initialized")
		// Per-agent MCP manager
		agentMCPMgr := agent.NewAgentMCPManager(nil)
		agentMgr.SetAgentMCPManager(agentMCPMgr)
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
		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Compression pipeline
	if gw.cfg.Agent.Compression.Strategy == "layered" {
		pipeline := agent.NewCompressionPipeline(
			gw.provider, gw.cfg.LLM.Model, gw.cfg.Agent.Compression, gw.resultStore, 200000,
		)
		gw.runtime.SetCompressionPipeline(pipeline)
		slog.Info("layered compression pipeline enabled")
	}

	return nil
}

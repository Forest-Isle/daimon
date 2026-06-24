package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/userdir"
)

type MultiAgentSubsystem struct {
	ContextMgr  agent.ContextManager
	AgentMgr    *agent.AgentManager
	SubAgentMgr *agent.SubAgentManager
	BgManager   *agent.BackgroundManager
}

func (ma *MultiAgentSubsystem) Name() string                  { return "multi_agent" }
func (ma *MultiAgentSubsystem) Start(_ context.Context) error { return nil }
func (ma *MultiAgentSubsystem) Stop(_ context.Context) error  { return nil }

func InitMultiAgent(features *FeatureSubsystem, cfg *config.Config, builder *agent.DepsBuilder,
	provider mind.Provider, sessions *session.Manager, db *store.DB, memStore memory.Store,
	toolsReg *tool.Registry, resultStore *tool.ResultStore) *MultiAgentSubsystem {

	ma := &MultiAgentSubsystem{}

	if features.IsEnabled("multi_agent") {
		builder.MultiAgent.BgManager = agent.NewBackgroundManager()
		ma.BgManager = builder.MultiAgent.BgManager
		builder.MultiAgent.PromptCache = agent.NewPromptCache()
		agentMCPMgr := agent.NewAgentMCPManager(nil)
		builder.MultiAgent.AgentMCP = agentMCPMgr
		subDeps := builder.Build()
		subDeps.Core.AgentID = "subagent-manager"
		builder.MultiAgent.SubAgentMgr = agent.NewSubAgentManager(subDeps)
		agentMgr := agent.NewAgentManager(provider, sessions, db, memStore, toolsReg, cfg.Agent, cfg.LLM)
		if err := agentMgr.LoadDir(userdir.AgentsDir()); err != nil {
			slog.Warn("multi-agent: load agents dir failed", "dir", userdir.AgentsDir(), "err", err)
		}
		for _, dir := range cfg.Agents.ExtraDirs {
			if err := agentMgr.LoadDir(dir); err != nil {
				slog.Warn("multi-agent: load agents dir failed", "dir", dir, "err", err)
			}
		}
		for _, def := range cfg.Agents.Definitions {
			if err := agentMgr.Add(defToSpec(def)); err != nil {
				slog.Warn("multi-agent: add inline agent failed", "name", def.Name, "err", err)
			}
		}
		builder.MultiAgent.AgentMgr = agentMgr
		agentMgr.SetAgentMCPManager(agentMCPMgr)
		agentMgr.SetBackgroundManager(builder.MultiAgent.BgManager)
		agentMgr.SetSubAgentManager(builder.MultiAgent.SubAgentMgr)
		agentMgr.RegisterAll(toolsReg)
		ma.AgentMgr = agentMgr
		ma.SubAgentMgr = builder.MultiAgent.SubAgentMgr
		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	contextWindow := mind.ModelContextWindow(cfg.LLM.Model)
	if cfg.Agent.Compression.Strategy == "layered" {
		_ = agent.NewCompressionPipeline(provider, cfg.LLM.Model, cfg.Agent.Compression, resultStore, contextWindow)
		slog.Info("layered compression pipeline enabled")
	}
	contextMgr := agent.NewPipelineContextManager(provider, cfg.LLM.Model, &cfg.Agent.Compression, contextWindow, resultStore)
	ma.ContextMgr = contextMgr
	builder.Memory.ContextMgr = contextMgr
	slog.Info("context manager initialized", "strategy", cfg.Agent.Compression.Strategy)
	return ma
}

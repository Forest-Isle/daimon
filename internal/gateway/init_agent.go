package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initAgentRuntime() error {
	// LLM provider selection based on config
	var provider agent.Provider
	switch gw.cfg.LLM.Provider {
	case "openai", "openai-compatible":
		provider = agent.NewOpenAIProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
		slog.Info("LLM provider: openai-compatible", "model", gw.cfg.LLM.Model, "base_url", gw.cfg.LLM.BaseURL)
	default:
		provider = agent.NewClaudeProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
		slog.Info("LLM provider: claude", "model", gw.cfg.LLM.Model)
	}

	if gw.cfg.LLM.Retry.MaxRetries > 0 {
		provider = agent.NewRetryProvider(provider, gw.cfg.LLM.Retry)
		slog.Info("LLM retry enabled", "max_retries", gw.cfg.LLM.Retry.MaxRetries, "base_delay", gw.cfg.LLM.Retry.BaseDelay)
	}
	gw.provider = provider

	// Build interceptor chain helper
	getInterceptor := func() *tool.InterceptorChain {
		if gw.interceptorChain != nil {
			return gw.interceptorChain
		}
		return tool.NewInterceptorChain(nil)
	}

	// Build AgentDeps with whatever is available at this point.
	// Memory/MultiAgent fields will be populated later as subsystems initialize.
	deps := agent.AgentDeps{
		Core: agent.CoreDeps{
			Provider: gw.provider,
			Tools:    gw.tools,
			Sessions: gw.sessions,
			DB:       gw.db,
			Cfg:      gw.cfg.Agent,
			LLMCfg:   gw.cfg.LLM,
			AgentID:  "gateway",
			ToolsCfg: gw.cfg.Tools,
		},
		Memory: agent.MemoryDeps{
			ContextMgr: gw.contextMgr,
		}.WithDefaults(),
		Security: agent.SecurityDeps{
			Interceptor: getInterceptor(),
			HookMgr:     gw.hookMgr,
			PermEngine:  gw.permEngine,
		}.WithDefaults(),
		Observability: agent.ObservabilityDeps{
			Emitter:        gw.emitter,
			MetricsEmitter: nil,
		}.WithDefaults(),
		MultiAgent: agent.MultiAgentDeps{
			ResultStore: gw.resultStore,
			SkillMgr:    gw.skillMgr,
		}.WithDefaults(),
	}

	// Build result store if configured
	if gw.cfg.Tools.ResultPersistence.Enabled {
		gw.resultStore = tool.NewResultStore(
			gw.cfg.Tools.ResultPersistence.CacheDir,
			gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			gw.cfg.Tools.ResultPersistence.PreviewChars,
			gw.cfg.Tools.ResultPersistence.TTLHours,
		)
		deps.MultiAgent.ResultStore = gw.resultStore
		// Startup cleanup sweep
		if err := gw.resultStore.Cleanup(); err != nil {
			slog.Warn("gateway: result store startup cleanup failed", "err", err)
		}
		slog.Info("tool result persistence enabled",
			"threshold", gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			"ttl_hours", gw.cfg.Tools.ResultPersistence.TTLHours,
		)
	}

	// Apply defaults once to gw.agentDeps, then pass a pointer to the Agent.
	// Because Agent holds *AgentDeps (not a by-value copy), later wiring —
	// Memory.Store, ContextMgr, Observability.Emitter, etc. — is immediately
	// visible to the Agent.
	gw.agentDeps = deps.WithDefaults()
	gw.agent = agent.NewAgent(&gw.agentDeps, &agent.UnifiedLoop{}, agent.NewEventBus())
	gw.agent.SetApprovalFunc(gw.handleApproval)

	return nil
}

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
		if gw.sandbox.InterceptorChain() != nil {
			return gw.sandbox.InterceptorChain()
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
			Emitter:        gw.dashboard.Emitter(),
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

	gw.agentDeps = deps
	gw.agent = agent.NewAgent(deps.WithDefaults(), &agent.UnifiedLoop{}, agent.NewEventBus())
	gw.agent.SetApprovalFunc(gw.handleApproval)

	// Register plan_task tool for LLM-driven task decomposition
	planExecutor := &agent.ToolExecutor{Agent: gw.agent}
	maxParallel := gw.cfg.Agent.Cognitive.MaxParallelTools
	if maxParallel <= 0 {
		maxParallel = 5
	}
	planTaskTool := tool.NewPlanTaskTool(
		maxParallel, planExecutor,
		gw.hookMgr, gw.permEngine, getInterceptor(),
	)
	gw.tools.Register(planTaskTool)
	slog.Info("plan_task tool registered", "max_parallel", maxParallel)

	return nil
}

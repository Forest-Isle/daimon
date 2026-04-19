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

	// Agent runtime
	gw.runtime = agent.NewRuntime(provider, gw.tools, gw.sessions, gw.db, gw.cfg.Agent, gw.cfg.LLM)

	// Wire hook manager and permission engine
	gw.runtime.SetHookManager(gw.hookMgr)
	gw.runtime.SetPermissionEngine(gw.permEngine)
	gw.runtime.SetInterceptorChain(gw.interceptorChain)

	// Tool result persistence
	if gw.cfg.Tools.ResultPersistence.Enabled {
		gw.resultStore = tool.NewResultStore(
			gw.cfg.Tools.ResultPersistence.CacheDir,
			gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			gw.cfg.Tools.ResultPersistence.PreviewChars,
			gw.cfg.Tools.ResultPersistence.TTLHours,
		)
		gw.runtime.SetResultStore(gw.resultStore)
		// Startup cleanup sweep
		if err := gw.resultStore.Cleanup(); err != nil {
			slog.Warn("gateway: result store startup cleanup failed", "err", err)
		}
		slog.Info("tool result persistence enabled",
			"threshold", gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			"ttl_hours", gw.cfg.Tools.ResultPersistence.TTLHours,
		)
	}

	// Concurrent tool execution
	gw.runtime.SetConcurrentConfig(gw.cfg.Tools.ConcurrentExecution)
	if gw.cfg.Tools.ConcurrentExecution.Enabled {
		slog.Info("concurrent tool execution enabled", "max_concurrency", gw.cfg.Tools.ConcurrentExecution.MaxConcurrency)
	}

	return nil
}

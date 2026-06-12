package gateway

import (
	"context"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
	"log/slog"
)

type AgentSubsystem struct {
	Provider agent.Provider
}

func (as *AgentSubsystem) Name() string                  { return "agent" }
func (as *AgentSubsystem) Start(_ context.Context) error { return nil }
func (as *AgentSubsystem) Stop(_ context.Context) error  { return nil }

func InitAgentRuntime(builder *agent.DepsBuilder, cfg *config.Config) *AgentSubsystem {
	var p agent.Provider
	if cfg.LLM.Provider == "openai" || cfg.LLM.Provider == "openai-compatible" {
		p = agent.NewOpenAIProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)
		slog.Info("LLM provider: openai-compatible", "model", cfg.LLM.Model)
	} else {
		p = agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)
		slog.Info("LLM provider: claude", "model", cfg.LLM.Model)
	}
	if cfg.LLM.Retry.MaxRetries > 0 {
		p = agent.NewRetryProvider(p, cfg.LLM.Retry)
	}
	builder.Core.Provider = p
	builder.Core.Cfg = cfg.Agent
	builder.Core.LLMCfg = cfg.LLM
	builder.Core.AgentID = "gateway"
	builder.Core.ToolsCfg = cfg.Tools
	return &AgentSubsystem{Provider: p}
}

package gateway

import (
	"context"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
)

type AgentSubsystem struct {
	Provider agent.Provider
}

func (as *AgentSubsystem) Name() string                  { return "agent" }
func (as *AgentSubsystem) Start(_ context.Context) error { return nil }
func (as *AgentSubsystem) Stop(_ context.Context) error  { return nil }

func InitAgentRuntime(builder *agent.DepsBuilder, cfg *config.Config) *AgentSubsystem {
	p := agent.NewProviderFromConfig(cfg.LLM)
	builder.Core.Provider = p
	builder.Core.Cfg = cfg.Agent
	builder.Core.LLMCfg = cfg.LLM
	builder.Core.AgentID = "gateway"
	builder.Core.ToolsCfg = cfg.Tools
	return &AgentSubsystem{Provider: p}
}

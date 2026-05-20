// Package boot is a lightweight, self-contained alternative to the legacy
// internal/gateway. It builds a minimal core.Agent with the bare essentials —
// LLM provider + a curated set of legacy tools + permission gate — and
// exposes a single Run entry-point.
//
// boot.New does NOT touch SQLite, evolution, RL, dashboard, MCP, scheduler,
// channels, or any of the optional subsystems. It is the "what you actually
// need to run an agent" core, intended to be the new default startup path.
package boot

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/core"
	"github.com/Forest-Isle/IronClaw/internal/core/adapter"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// Options is the runtime configuration for a minimal boot agent.
// Zero values give a sensible default: Claude provider with the
// configured model, all standard tools enabled.
type Options struct {
	Cfg            *config.Config
	Sink           core.EventSink
	Gate           core.Gate
	Approver       core.Approver
	ExtraTools     []core.Tool
	ExtraMW        []core.ToolMiddleware
	System         string
	Model          string
	MaxTurns       int
	ParallelTools  int
	MemoryPath     string // if set, use FileMemory at this path (NDJSON)
}

// New constructs a fully-wired core.Agent from a config.Config. The agent
// is independent of internal/gateway — it doesn't open the database, start
// goroutines, or persist memory. It's the right entry point for unit-test
// or single-shot CLI flows.
func New(opts Options) (*core.Agent, error) {
	if opts.Cfg == nil {
		return nil, fmt.Errorf("boot.New: nil config")
	}
	cfg := opts.Cfg

	provider, err := buildProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}

	legacyTools := buildLegacyTools(cfg)
	tools := adapter.ImportToolRegistry(legacyTools)
	for _, t := range opts.ExtraTools {
		tools.Register(t)
	}

	system := opts.System
	if system == "" {
		system = cfg.Agent.SystemPrompt
	}

	model := opts.Model
	if model == "" {
		model = cfg.LLM.Model
	}

	var mem core.Memory
	if opts.MemoryPath != "" {
		fm, err := core.NewFileMemory(opts.MemoryPath)
		if err != nil {
			return nil, fmt.Errorf("memory: %w", err)
		}
		mem = fm
	}

	corecfg := core.Config{
		Model:          model,
		System:         system,
		MaxTurns:       opts.MaxTurns,
		MaxTokens:      cfg.LLM.MaxTokens,
		ParallelTools:  opts.ParallelTools,
		Sink:           opts.Sink,
		Gate:           opts.Gate,
		Approver:       opts.Approver,
		ToolMiddleware: append([]core.ToolMiddleware{core.CacheToolMiddleware(tools)}, opts.ExtraMW...),
	}

	return core.New(adapter.NewLegacyProvider(provider), tools, mem, corecfg), nil
}

// Run is a one-shot convenience: build an agent, run a prompt, return the
// final text. Useful for `ironclaw core run "<prompt>"` or quick tests.
func Run(ctx context.Context, opts Options, prompt string) (string, core.StopReason, error) {
	ag, err := New(opts)
	if err != nil {
		return "", core.StopError, err
	}
	return ag.Run(ctx, prompt)
}

// buildProvider mirrors the small selection logic in
// internal/gateway/init_agent.go but without any of the gateway state.
func buildProvider(cfg *config.Config) (agent.Provider, error) {
	switch cfg.LLM.Provider {
	case "openai", "openai-compatible":
		return agent.NewOpenAIProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL), nil
	case "claude", "":
		return agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL), nil
	default:
		return nil, fmt.Errorf("unknown llm.provider %q (want claude|openai|openai-compatible)", cfg.LLM.Provider)
	}
}

// buildLegacyTools constructs the standard set of legacy tools based on
// the config. Mirrors the simple subset used by the gateway, omitting
// codebase-index/worktree/MCP which require additional state.
func buildLegacyTools(cfg *config.Config) *tool.Registry {
	reg := tool.NewRegistry()
	policy := tool.NewPolicy(cfg.Tools.Bash.BlockedCommands)
	if cfg.Tools.Bash.Enabled {
		reg.Register(tool.NewBashTool(cfg.Tools.Bash.Timeout, cfg.Tools.Bash.RequiresApproval, policy))
	}
	if cfg.Tools.File.Enabled {
		reg.Register(tool.NewFileReadTool())
		reg.Register(tool.NewFileWriteTool(cfg.Tools.File.RequiresApproval))
		reg.Register(tool.NewFileEditTool(cfg.Tools.File.RequiresApproval))
		reg.Register(tool.NewFileListTool())
	}
	if cfg.Tools.HTTP.Enabled {
		reg.Register(tool.NewHTTPTool(cfg.Tools.HTTP.Timeout, cfg.Tools.HTTP.RequiresApproval))
	}
	if cfg.Tools.Browser.Enabled {
		reg.Register(tool.NewBrowserSearchTool(cfg.Tools.Browser.Timeout, cfg.Tools.Browser.RequiresApproval))
		reg.Register(tool.NewBrowserExtractTool(cfg.Tools.Browser.Timeout, cfg.Tools.Browser.RequiresApproval))
	}
	return reg
}

// ListToolSchemas returns the schemas of all tools that would be registered
// for the given config, suitable for display by `ironclaw core tools`.
func ListToolSchemas(cfg *config.Config) []core.ToolSchema {
	reg := adapter.ImportToolRegistry(buildLegacyTools(cfg))
	return reg.Schemas()
}

package gateway

import (
	"fmt"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// HeadlessGateway is a minimal runtime used by subprocess backends.
// It initializes only DB → Tools → LLM → Runtime, skipping channels,
// scheduler, knowledge, skills, memory, and multi-agent orchestration.
type HeadlessGateway struct {
	db         *store.DB
	sessions   *session.Manager
	tools      *tool.Registry
	permEngine   *tool.PermissionEngine
	trustTracker *tool.TrustTracker
	hookMgr      *hook.Manager
	provider   agent.Provider
	agent      *agent.Agent
	cfg        *config.Config
}

// NewHeadless creates a headless gateway from the given config.
// It is the minimal setup needed to run a single agent task.
func NewHeadless(cfg *config.Config) (*HeadlessGateway, error) {
	h := &HeadlessGateway{cfg: cfg}

	// 1. Database
	db, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, fmt.Errorf("headless: database: %w", err)
	}
	h.db = db
	h.sessions = session.NewManager(db)

	// 2. Tool registry
	h.tools = tool.NewRegistry()
	policy := tool.NewPolicy(cfg.Tools.Bash.BlockedCommands)

	if cfg.Tools.Bash.Enabled {
		h.tools.Register(tool.NewBashTool(cfg.Tools.Bash.Timeout, cfg.Tools.Bash.RequiresApproval, policy))
		h.tools.Register(tool.NewTestRunTool("."))
	}
	if cfg.Tools.File.Enabled {
		h.tools.Register(tool.NewFileReadTool())
		h.tools.Register(tool.NewFileWriteTool(cfg.Tools.File.RequiresApproval))
		h.tools.Register(tool.NewFileEditTool(cfg.Tools.File.RequiresApproval))
		h.tools.Register(tool.NewFilePatchTool("."))
		h.tools.Register(tool.NewFileListTool())
		h.tools.Register(tool.NewGrepCodeTool("."))
		h.tools.Register(tool.NewFindSymbolTool("."))
		h.tools.Register(tool.NewListImportsTool("."))
	}
	if cfg.Tools.HTTP.Enabled {
		h.tools.Register(tool.NewHTTPTool(cfg.Tools.HTTP.Timeout, cfg.Tools.HTTP.RequiresApproval))
	}
	if cfg.Tools.Browser.Enabled {
		h.tools.Register(tool.NewBrowserTool(cfg.Tools.Browser.Timeout, cfg.Tools.Browser.RequiresApproval))
	}

	// 3. Hook & permission engine
	hookCfg := cfg.Hooks
	preToolUseCfg := make([]hook.HandlerConfig, len(hookCfg.PreToolUse))
	for i, h := range hookCfg.PreToolUse {
		preToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	postToolUseCfg := make([]hook.HandlerConfig, len(hookCfg.PostToolUse))
	for i, h := range hookCfg.PostToolUse {
		postToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	onUserMsgCfg := make([]hook.HandlerConfig, len(hookCfg.OnUserMessage))
	for i, h := range hookCfg.OnUserMessage {
		onUserMsgCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	preCompactCfg := make([]hook.HandlerConfig, len(hookCfg.PreCompact))
	for i, h := range hookCfg.PreCompact {
		preCompactCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	h.hookMgr = hook.BuildManager(preToolUseCfg, postToolUseCfg, onUserMsgCfg, preCompactCfg, &hook.BuildManagerOpts{DB: db.DB})

	permRules := make([]tool.PermissionRule, len(cfg.Permissions.Rules))
	for i, r := range cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	h.permEngine = tool.NewPermissionEngine(permRules, cfg.Permissions.Default, policy)
	h.trustTracker = tool.NewTrustTracker()

	// 4. LLM provider
	var provider agent.Provider = agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)
	if cfg.LLM.Retry.MaxRetries > 0 {
		provider = agent.NewRetryProvider(provider, cfg.LLM.Retry)
	}
	h.provider = provider

	// 5. Agent runtime
	deps := agent.AgentDeps{
		Core: agent.CoreDeps{
			Provider: provider,
			Tools:    h.tools,
			Sessions: h.sessions,
			DB:       h.db,
			Cfg:      cfg.Agent,
			LLMCfg:   cfg.LLM,
			AgentID:  "headless",
			ToolsCfg: cfg.Tools,
		},
		Security: agent.SecurityDeps{
			Interceptor:  tool.NewInterceptorChain(nil),
			HookMgr:      h.hookMgr,
			PermEngine:   h.permEngine,
			TrustTracker: h.trustTracker,
		}.WithDefaults(),
		Observability: agent.ObservabilityDeps{}.WithDefaults(),
		MultiAgent:    agent.MultiAgentDeps{}.WithDefaults(),
	}.WithDefaults()

	if cfg.Tools.ResultPersistence.Enabled {
		rs := tool.NewResultStore(
			cfg.Tools.ResultPersistence.CacheDir,
			cfg.Tools.ResultPersistence.ThresholdBytes,
			cfg.Tools.ResultPersistence.PreviewChars,
			cfg.Tools.ResultPersistence.TTLHours,
		)
		deps.MultiAgent.ResultStore = rs
	}

	h.agent = agent.NewAgent(deps.WithDefaults(), &agent.SimpleLoop{}, agent.NewEventBus())

	slog.Info("headless gateway initialized")
	return h, nil
}

// Agent returns the agent runtime.
func (h *HeadlessGateway) Agent() *agent.Agent { return h.agent }

// Provider returns the LLM provider.
func (h *HeadlessGateway) Provider() agent.Provider { return h.provider }

// Tools returns the tool registry.
func (h *HeadlessGateway) Tools() *tool.Registry { return h.tools }

// Sessions returns the session manager.
func (h *HeadlessGateway) Sessions() *session.Manager { return h.sessions }

// DB returns the database handle.
func (h *HeadlessGateway) DB() *store.DB { return h.db }

// Config returns the loaded config.
func (h *HeadlessGateway) Config() *config.Config { return h.cfg }

// Close releases all resources held by the headless gateway.
func (h *HeadlessGateway) Close() error {
	if h.db != nil {
		return h.db.Close()
	}
	return nil
}

// FilterTools rebuilds the tool registry to contain only the named tools.
// Pass an empty or nil slice to keep all tools.
func (h *HeadlessGateway) FilterTools(allowed []string) {
	if len(allowed) == 0 {
		return
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowSet[name] = struct{}{}
	}
	filtered := tool.NewRegistry()
	for _, t := range h.tools.All() {
		if _, ok := allowSet[t.Name()]; ok {
			filtered.Register(t)
		}
	}
	h.tools = filtered
}

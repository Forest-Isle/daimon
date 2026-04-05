package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func (gw *Gateway) initToolsAndHooks() error {
	// Tool registry
	gw.tools = tool.NewRegistry()
	policy := tool.NewPolicy(gw.cfg.Tools.Bash.BlockedCommands)

	if gw.cfg.Tools.Bash.Enabled {
		gw.tools.Register(tool.NewBashTool(gw.cfg.Tools.Bash.Timeout, gw.cfg.Tools.Bash.RequiresApproval, policy))
	}
	if gw.cfg.Tools.File.Enabled {
		gw.tools.Register(tool.NewFileTool(gw.cfg.Tools.File.RequiresApproval))
	}
	if gw.cfg.Tools.HTTP.Enabled {
		gw.tools.Register(tool.NewHTTPTool(gw.cfg.Tools.HTTP.Timeout, gw.cfg.Tools.HTTP.RequiresApproval))
	}

	// Hook event system
	hookCfg := gw.cfg.Hooks
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
	gw.hookMgr = hook.BuildManager(preToolUseCfg, postToolUseCfg, onUserMsgCfg, preCompactCfg, &hook.BuildManagerOpts{DB: gw.db.DB})
	slog.Info("hook system initialized")

	// Permission engine
	permRules := make([]tool.PermissionRule, len(gw.cfg.Permissions.Rules))
	for i, r := range gw.cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	gw.permEngine = tool.NewPermissionEngine(permRules, gw.cfg.Permissions.Default, policy)

	return nil
}

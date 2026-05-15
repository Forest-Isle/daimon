package gateway

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/sandbox"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/Forest-Isle/IronClaw/internal/worktree"
)

func (gw *Gateway) initToolsAndHooks() error {
	// Tool registry
	gw.tools = tool.NewRegistry()
	policy := tool.NewPolicy(gw.cfg.Tools.Bash.BlockedCommands)

	if gw.cfg.Tools.Bash.Enabled {
		gw.tools.Register(tool.NewBashTool(gw.cfg.Tools.Bash.Timeout, gw.cfg.Tools.Bash.RequiresApproval, policy))
		gw.tools.Register(tool.NewTestRunTool("."))
	}
	if gw.cfg.Tools.File.Enabled {
		gw.tools.Register(tool.NewFileReadTool())
		gw.tools.Register(tool.NewFileWriteTool(gw.cfg.Tools.File.RequiresApproval))
		gw.tools.Register(tool.NewFileEditTool(gw.cfg.Tools.File.RequiresApproval))
		gw.tools.Register(tool.NewFilePatchTool("."))
		gw.tools.Register(tool.NewFileListTool())
		gw.tools.Register(tool.NewGrepCodeTool("."))
		gw.tools.Register(tool.NewFindSymbolTool("."))
		gw.tools.Register(tool.NewListImportsTool("."))
	}
	if gw.cfg.Tools.HTTP.Enabled {
		gw.tools.Register(tool.NewHTTPTool(gw.cfg.Tools.HTTP.Timeout, gw.cfg.Tools.HTTP.RequiresApproval))
	}
	if gw.cfg.Tools.Browser.Enabled {
		gw.tools.Register(tool.NewBrowserTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
		gw.tools.Register(tool.NewBrowserSearchTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
		gw.tools.Register(tool.NewBrowserExtractTool(gw.cfg.Tools.Browser.Timeout, gw.cfg.Tools.Browser.RequiresApproval))
	}

	// Worktree tools for isolated code changes
	if gw.featureEnabled("worktree") {
		worktree.RegisterTools(gw.tools, ".")
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

	// User-configurable hook scripts from ~/.IronClaw/hooks/
	if home, err := os.UserHomeDir(); err == nil {
		hooksDir := filepath.Join(home, ".IronClaw", "hooks")
		gw.userHookMgr = hook.NewUserHookManager(hooksDir, 30*time.Second)
		slog.Info("user hook system initialized", "dir", hooksDir,
			"hooks", len(gw.userHookMgr.ListHooks()))
	}

	// Permission engine
	permRules := make([]tool.PermissionRule, len(gw.cfg.Permissions.Rules))
	for i, r := range gw.cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	gw.permEngine = tool.NewPermissionEngine(permRules, gw.cfg.Permissions.Default, policy)

	// Sandbox components
	var fileGuard *sandbox.FileGuard
	var networkPolicy *sandbox.NetworkPolicy
	sandboxEnabled := gw.featureEnabled("sandbox")

	if sandboxEnabled {
		if len(gw.cfg.Sandbox.AllowedDirectories) > 0 {
			var err error
			fileGuard, err = sandbox.NewFileGuard(gw.cfg.Sandbox.AllowedDirectories, gw.cfg.Sandbox.ReadonlyDirectories)
			if err != nil {
				slog.Warn("sandbox: FileGuard init failed, disabled", "err", err)
			}
		}
		networkPolicy = sandbox.NewNetworkPolicy(
			gw.cfg.Sandbox.Network.Mode,
			gw.cfg.Sandbox.Network.Whitelist,
			gw.cfg.Sandbox.Network.Blacklist,
		)
		if gw.cfg.Sandbox.Bash.Backend == "docker" {
			sandbox.CleanupOrphans(context.Background())
			available := sandbox.ProbeDocker(context.Background())
			if !available {
				slog.Warn("sandbox: Docker not available, bash will run on host")
			}
			gw.dockerSessionMgr = sandbox.NewDockerSessionManager(sandbox.DockerSessionConfig{
				Image:        gw.cfg.Sandbox.Bash.Docker.Image,
				NetworkMode:  gw.cfg.Sandbox.Bash.Docker.Network,
				MemoryLimit:  gw.cfg.Sandbox.Bash.Docker.MemoryLimit,
				CPULimit:     gw.cfg.Sandbox.Bash.Docker.CPULimit,
				AllowedDirs:  gw.cfg.Sandbox.AllowedDirectories,
				ReadonlyDirs: gw.cfg.Sandbox.ReadonlyDirectories,
				IdleTimeout:  gw.cfg.Sandbox.Bash.Docker.IdleTimeout,
			}, available)
		}
	}

	// Build interceptor chain: permission → hook → sandbox → verify → audit
	auditInterceptor, err := tool.NewAuditInterceptor("")
	if err != nil {
		slog.Warn("audit interceptor init failed, continuing without audit", "err", err)
	}
	verifyInterceptor := tool.NewVerifyInterceptor(".")

	// Progressive trust tracker (resets per session)
	gw.trustTracker = tool.NewTrustTracker()

	interceptors := []tool.ToolInterceptor{
		tool.NewPermissionInterceptor(gw.permEngine, nil, nil),
		tool.NewHookInterceptor(gw.hookMgr),
		newUserHookInterceptor(gw.userHookMgr),
		tool.NewSandboxInterceptor(gw.dockerSessionMgr, fileGuard, networkPolicy, sandboxEnabled),
	}
	if gw.cfg.Tools.Verify.Enabled {
		interceptors = append(interceptors, verifyInterceptor)
	}
	if auditInterceptor != nil {
		interceptors = append(interceptors, auditInterceptor)
	}
	gw.interceptorChain = tool.NewInterceptorChain(interceptors)

	slog.Info("sandbox system initialized", "enabled", sandboxEnabled)

	return nil
}

// userHookInterceptor runs user-configurable hook scripts around tool execution.
type userHookInterceptor struct {
	mgr *hook.UserHookManager
}

func newUserHookInterceptor(mgr *hook.UserHookManager) *userHookInterceptor {
	return &userHookInterceptor{mgr: mgr}
}

func (u *userHookInterceptor) Name() string { return "user_hooks" }

func (u *userHookInterceptor) Intercept(
	ctx context.Context, call *tool.ToolCall, next tool.InterceptorFunc,
) (*tool.ToolResult, error) {
	if u.mgr == nil {
		return next(ctx, call)
	}

	// Fire pre_tool_use hooks before execution.
	u.mgr.RunHooks(ctx, hook.HookPreToolUse, map[string]any{
		"tool_name":  call.ToolName,
		"tool_input": call.Input,
	})

	// Execute the actual tool.
	result, err := next(ctx, call)

	// Fire post_tool_use hooks after execution.
	output := ""
	toolErr := ""
	if result != nil {
		output = result.Output
		toolErr = result.Error
	}
	u.mgr.RunHooks(ctx, hook.HookPostToolUse, map[string]any{
		"tool_name":   call.ToolName,
		"tool_input":  call.Input,
		"tool_output": output,
		"tool_error":  toolErr,
	})

	return result, err
}

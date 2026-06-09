package gateway

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/sandbox"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/Forest-Isle/IronClaw/internal/worktree"
)

func (gw *Gateway) initToolsAndHooks() error {
	// Tool registry
	gw.tools = tool.NewRegistry()
	cfg := gw.Config()
	policy := tool.NewPolicy(cfg.Tools.Bash.BlockedCommands)

	if cfg.Tools.Bash.Enabled {
		gw.tools.Register(tool.NewBashTool(cfg.Tools.Bash.Timeout, cfg.Tools.Bash.RequiresApproval, policy))
		gw.tools.Register(tool.NewTestRunTool("."))
	}
	if cfg.Tools.File.Enabled {
		gw.tools.Register(tool.NewFileReadTool())
		gw.tools.Register(tool.NewFileWriteTool(cfg.Tools.File.RequiresApproval))
		gw.tools.Register(tool.NewFileEditTool(cfg.Tools.File.RequiresApproval))
		gw.tools.Register(tool.NewFilePatchTool("."))
		gw.tools.Register(tool.NewFileListTool())
		gw.tools.Register(tool.NewGrepCodeTool("."))
		gw.tools.Register(tool.NewFindSymbolTool("."))
		gw.tools.Register(tool.NewListImportsTool("."))
	}
	if cfg.Tools.HTTP.Enabled {
		httpTool := tool.NewHTTPTool(cfg.Tools.HTTP.Timeout, cfg.Tools.HTTP.RequiresApproval)
		gw.tools.Register(httpTool)
		gw.sandbox.httpTool = httpTool // stored for redirect-check injection after network policy is created
	}
	if gw.codebaseIndex == nil {
		gw.codebaseIndex = newCodebaseIndexFromConfig(gw)
		if gw.codebaseIndex != nil && gw.codebaseIndex.IsAvailable() {
			if err := gw.codebaseIndex.IndexDirectoryContext(gw.initCtx, "."); err != nil {
				slog.Warn("codebase index: initial indexing failed", "err", err)
			} else {
				slog.Info("codebase index initialized")
			}
		}
	}
	if gw.codebaseIndex != nil && gw.codebaseIndex.IsAvailable() {
		gw.tools.Register(tool.NewSemanticSearchTool(
			gw.codebaseIndex.IsAvailable,
			func(query string, topK int) ([]tool.CodeSearchResult, error) {
				results, err := gw.codebaseIndex.Search(gw.initCtx, query, topK)
				if err != nil {
					return nil, err
				}
				out := make([]tool.CodeSearchResult, 0, len(results))
				for _, chunk := range results {
					out = append(out, tool.CodeSearchResult{
						FilePath:  chunk.FilePath,
						StartLine: chunk.StartLine,
						EndLine:   chunk.EndLine,
						Content:   chunk.Content,
						Score:     chunk.Score,
					})
				}
				return out, nil
			},
		))
	}

	// Worktree tools for isolated code changes
	if gw.featureEnabled("worktree") {
		worktree.RegisterTools(gw.tools, ".")
	}
	// Hook event system
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
	permRules := make([]tool.PermissionRule, len(cfg.Permissions.Rules))
	for i, r := range cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	gw.permEngine = tool.NewPermissionEngine(permRules, cfg.Permissions.Default, policy)
	// Sandbox components
	var fileGuard *sandbox.FileGuard
	var networkPolicy *sandbox.NetworkPolicy
	sandboxEnabled := gw.featureEnabled("sandbox")

	if sandboxEnabled {
		if len(cfg.Sandbox.AllowedDirectories) > 0 {
			var err error
			fileGuard, err = sandbox.NewFileGuard(cfg.Sandbox.AllowedDirectories, cfg.Sandbox.ReadonlyDirectories)
			if err != nil {
				slog.Warn("sandbox: FileGuard init failed, disabled", "err", err)
			}
		}
		networkPolicy = sandbox.NewNetworkPolicy(
			cfg.Sandbox.Network.Mode,
			cfg.Sandbox.Network.Whitelist,
			cfg.Sandbox.Network.Blacklist,
		)

	// Inject redirect validation into HTTP tool so that HTTP redirects
	// are re-checked against the network policy (SSRF protection).
	if gw.sandbox.httpTool != nil && networkPolicy != nil {
		gw.sandbox.httpTool.SetCheckRedirect(networkPolicy.CheckURL)
	}
		if cfg.Sandbox.Bash.Backend == "docker" {
			sandbox.CleanupOrphans(gw.initCtx)
			available := sandbox.ProbeDocker(gw.initCtx)
			if !available {
				slog.Warn("sandbox: Docker not available, bash will run on host")
			}
			gw.sandbox.dockerSessionMgr = sandbox.NewDockerSessionManager(sandbox.DockerSessionConfig{
				Image:        cfg.Sandbox.Bash.Docker.Image,
				NetworkMode:  cfg.Sandbox.Bash.Docker.Network,
				MemoryLimit:  cfg.Sandbox.Bash.Docker.MemoryLimit,
				CPULimit:     cfg.Sandbox.Bash.Docker.CPULimit,
				AllowedDirs:  cfg.Sandbox.AllowedDirectories,
				ReadonlyDirs: cfg.Sandbox.ReadonlyDirectories,
				IdleTimeout:  cfg.Sandbox.Bash.Docker.IdleTimeout,
			}, available)
		}
	}

	// Build interceptor chain: permission -> hook -> sandbox -> verify -> audit
	auditInterceptor, err := tool.NewAuditInterceptor("")
	if err != nil {
		slog.Warn("audit interceptor init failed, continuing without audit", "err", err)
	}
	verifyInterceptor := tool.NewVerifyInterceptor(".")

	// Progressive trust tracker removed (Phase 4 — trust accumulation over-designed)

	interceptors := []tool.ToolInterceptor{
		tool.NewPermissionInterceptor(gw.permEngine,
			tool.WithNotifier(NewGatewayToolNotifier()),
			tool.WithApprover(NewGatewayToolApprover(gw.sessions, gw.channels))),
		tool.NewHookInterceptor(gw.hookMgr),
		newUserHookInterceptor(gw.userHookMgr),
		tool.NewSandboxInterceptor(gw.sandbox.dockerSessionMgr, fileGuard, networkPolicy, sandboxEnabled),
	}
	if cfg.Tools.Verify.Enabled {
		interceptors = append(interceptors, verifyInterceptor)
	}
	if auditInterceptor != nil {
		interceptors = append(interceptors, auditInterceptor)
	}
	gw.sandbox.interceptorChain = tool.NewInterceptorChain(interceptors)

	slog.Info("sandbox system initialized", "enabled", sandboxEnabled)

	return nil
}

type memoryEmbeddingAdapter struct {
	provider memory.EmbeddingProvider
}

func (a memoryEmbeddingAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	embedding, err := a.provider.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(embedding) == 0 {
		return nil, nil
	}
	out := make([]float64, len(embedding))
	for i := range embedding {
		out[i] = float64(embedding[i])
	}
	return out, nil
}

func newCodebaseIndexFromConfig(gw *Gateway) *agent.CodebaseIndex {
	cfg := gw.Config()
	if cfg.Memory.OpenAIAPIKey == "" {
		return agent.NewCodebaseIndex(nil, agent.IndexConfig{
			ChunkSize:      50,
			Overlap:        10,
			EmbeddingModel: cfg.Memory.EmbeddingModel,
		})
	}
	provider := memory.NewCachedEmbedder(memory.NewOpenAIEmbeddingWithURL(
		cfg.Memory.OpenAIAPIKey,
		cfg.Memory.EmbeddingModel,
		cfg.Memory.EmbeddingBaseURL,
	))
	return agent.NewCodebaseIndex(memoryEmbeddingAdapter{provider: provider}, agent.IndexConfig{
		ChunkSize:      50,
		Overlap:        10,
		EmbeddingModel: cfg.Memory.EmbeddingModel,
	})
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

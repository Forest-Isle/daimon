package gateway

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/hook"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/world"
)

type ToolSubsystem struct {
	Registry         *tool.Registry
	DeferredCatalog  *tool.DeferredCatalog
	InterceptorChain *tool.InterceptorChain
	HookMgr          *hook.Manager
	PermEngine       *tool.PermissionEngine
	UserHookMgr      *hook.UserHookManager
	ResultStore      *tool.ResultStore
	CodebaseIndex    *agent.CodebaseIndex
	WorldStore       *world.Store
	WorldIdentity    world.Identity
	ActionStore      *action.Store
}

func (ts *ToolSubsystem) Name() string                  { return "tool" }
func (ts *ToolSubsystem) Start(_ context.Context) error { return nil }
func (ts *ToolSubsystem) Stop(_ context.Context) error  { return nil }

func InitTools(ctx context.Context, cfg *config.Config, features *FeatureSubsystem, sessions *session.Manager, channels *ChannelSubsystem, db *store.DB, bus agent.EventBus) *ToolSubsystem {
	ts := &ToolSubsystem{}
	ts.Registry = tool.NewRegistry()
	ts.DeferredCatalog = tool.NewDeferredCatalog()
	policy := tool.NewPolicy(cfg.Tools.Bash.BlockedCommands)

	ts.Registry.Register(tool.NewToolSearchTool(ts.DeferredCatalog, ts.Registry))

	ts.WorldIdentity = world.Identity{Dir: filepath.Join(appdir.BaseDir(), "world", "identity")}
	if err := ts.WorldIdentity.EnsureDir(); err != nil {
		slog.Warn("world: ensure identity dir failed", "err", err)
	}
	ts.WorldStore = world.NewStore(db.DB)
	ts.Registry.Register(tool.NewWorldReadTool(ts.WorldStore, ts.WorldIdentity))
	ts.Registry.Register(tool.NewCommitmentTool(ts.WorldStore))
	ts.Registry.Register(tool.NewWorldEditTool(ts.WorldIdentity))

	ts.ActionStore = action.NewStore(db.DB)

	if cfg.Tools.Bash.Enabled {
		// Route bash through host/sandbox per call: non-local triggers are forced
		// into the seatbelt sandbox; the configured default applies to local ones.
		shellBackend := tool.NewChannelRoutingBackend(
			tool.NewHostShellBackend(),
			tool.NewSeatbeltShellBackend(),
			cfg.Tools.Exec.Backend == "seatbelt",
		)
		ts.Registry.Register(tool.NewBashToolWithBackend(cfg.Tools.Bash.Timeout, cfg.Tools.Bash.RequiresApproval, policy, shellBackend))
		ts.Registry.Register(tool.NewTestRunTool("."))
	}
	if cfg.Tools.File.Enabled {
		ts.Registry.Register(tool.NewFileReadTool())
		ts.Registry.Register(tool.NewFileWriteTool(cfg.Tools.File.RequiresApproval))
		ts.Registry.Register(tool.NewFileEditTool(cfg.Tools.File.RequiresApproval))
		ts.Registry.Register(tool.NewFilePatchTool("."))
		ts.Registry.Register(tool.NewFileListTool())
		ts.Registry.Register(tool.NewGrepCodeTool("."))
		ts.Registry.Register(tool.NewFindSymbolTool("."))
		ts.Registry.Register(tool.NewListImportsTool("."))
	}
	if cfg.Tools.HTTP.Enabled {
		ts.Registry.Register(tool.NewHTTPTool(cfg.Tools.HTTP.Timeout, cfg.Tools.HTTP.RequiresApproval))
	}

	ts.CodebaseIndex = newCodebaseIndexFromCfg(cfg)
	if ts.CodebaseIndex != nil && ts.CodebaseIndex.IsAvailable() {
		if err := ts.CodebaseIndex.IndexDirectoryContext(ctx, "."); err != nil {
			slog.Warn("codebase index: initial indexing failed", "err", err)
		} else {
			slog.Info("codebase index initialized")
		}
		ts.Registry.Register(tool.NewSemanticSearchTool(
			ts.CodebaseIndex.IsAvailable,
			func(query string, topK int) ([]tool.CodeSearchResult, error) {
				results, err := ts.CodebaseIndex.Search(ctx, query, topK)
				if err != nil {
					return nil, err
				}
				out := make([]tool.CodeSearchResult, 0, len(results))
				for _, chunk := range results {
					out = append(out, tool.CodeSearchResult{
						FilePath: chunk.FilePath, StartLine: chunk.StartLine,
						EndLine: chunk.EndLine, Content: chunk.Content, Score: chunk.Score,
					})
				}
				return out, nil
			},
		))
	}

	preToolUseCfg := make([]hook.HandlerConfig, len(cfg.Hooks.PreToolUse))
	for i, h := range cfg.Hooks.PreToolUse {
		preToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	postToolUseCfg := make([]hook.HandlerConfig, len(cfg.Hooks.PostToolUse))
	for i, h := range cfg.Hooks.PostToolUse {
		postToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	onUserMsgCfg := make([]hook.HandlerConfig, len(cfg.Hooks.OnUserMessage))
	for i, h := range cfg.Hooks.OnUserMessage {
		onUserMsgCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	preCompactCfg := make([]hook.HandlerConfig, len(cfg.Hooks.PreCompact))
	for i, h := range cfg.Hooks.PreCompact {
		preCompactCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	ts.HookMgr = hook.BuildManager(preToolUseCfg, postToolUseCfg, onUserMsgCfg, preCompactCfg, &hook.BuildManagerOpts{DB: db.DB})
	slog.Info("hook system initialized")

	hooksDir := filepath.Join(appdir.BaseDir(), "hooks")
	ts.UserHookMgr = hook.NewUserHookManager(hooksDir, 30*time.Second)
	slog.Info("user hook system initialized", "dir", hooksDir, "hooks", len(ts.UserHookMgr.ListHooks()))

	permRules := make([]tool.PermissionRule, len(cfg.Permissions.Rules))
	for i, r := range cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action}
	}
	ts.PermEngine = tool.NewPermissionEngineWithProfiles(permRules, cfg.Permissions.Default, policy,
		permissionProfilesFromConfig(cfg))

	audit, _ := tool.NewAuditInterceptor("")
	verify := tool.NewVerifyInterceptor(".")
	interceptors := []tool.ToolInterceptor{
		tool.NewPermissionInterceptor(ts.PermEngine,
			tool.WithNotifier(NewGatewayToolNotifier()),
			tool.WithApprover(NewGatewayToolApprover(sessions, channels)),
			tool.WithPermissionAuditSink(db),
			tool.WithPermissionDecisionReporter(NewGatewayPermissionDecisionReporter(bus))),
		tool.NewHookInterceptor(ts.HookMgr),
		newUserHookInterceptor(ts.UserHookMgr),
		tool.NewReadBeforeEditInterceptor(nil),
	}
	if cfg.Tools.Verify.Enabled {
		interceptors = append(interceptors, verify)
	}
	if audit != nil {
		interceptors = append(interceptors, audit)
	}
	// Action interceptor records governed (non-read-only) executions in the
	// trust ledger and stamps the reversibility class. It sits inside the
	// permission gate so it only sees allowed calls and the raw execution result.
	interceptors = append(interceptors, action.NewInterceptor(ts.ActionStore, nil))
	// Activity reporter sits innermost so it wraps the real tool execution
	// tightest — it reports only tools that passed permission and hook gates,
	// avoiding a flicker for denied/blocked calls.
	interceptors = append(interceptors,
		tool.NewActivityInterceptor(NewGatewayToolActivityReporter(sessions, channels)))
	ts.InterceptorChain = tool.NewInterceptorChain(interceptors)

	if cfg.Tools.ResultPersistence.Enabled {
		ts.ResultStore = tool.NewResultStore(
			cfg.Tools.ResultPersistence.CacheDir, cfg.Tools.ResultPersistence.ThresholdBytes,
			cfg.Tools.ResultPersistence.PreviewChars, cfg.Tools.ResultPersistence.TTLHours,
		)
		_ = ts.ResultStore.Cleanup()
	}

	return ts
}

func newCodebaseIndexFromCfg(cfg *config.Config) *agent.CodebaseIndex {
	if cfg.Memory.OpenAIAPIKey == "" {
		return agent.NewCodebaseIndex(nil, agent.IndexConfig{ChunkSize: 50, Overlap: 10, EmbeddingModel: cfg.Memory.EmbeddingModel})
	}
	p := memory.NewCachedEmbedder(memory.NewOpenAIEmbeddingWithURL(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel, cfg.Memory.EmbeddingBaseURL))
	return agent.NewCodebaseIndex(memoryEmbeddingAdapter{provider: p}, agent.IndexConfig{ChunkSize: 50, Overlap: 10, EmbeddingModel: cfg.Memory.EmbeddingModel})
}

func permissionProfilesFromConfig(cfg *config.Config) map[tool.ToolChannelClass]tool.PermissionProfile {
	defaultAction := tool.ParsePermissionAction(cfg.Permissions.Default)
	profiles := tool.DefaultPermissionProfiles(defaultAction)
	for name, override := range cfg.Permissions.Profiles {
		class := tool.ToolChannelClass(name)
		profile, ok := profiles[class]
		if !ok {
			continue
		}
		if override.Default != "" {
			profile.DefaultAction = tool.ParsePermissionAction(override.Default)
		}
		if override.RequireApprovalForWrite != nil {
			profile.RequireApprovalForWrite = *override.RequireApprovalForWrite
		}
		if override.RequireApprovalForDestructive != nil {
			profile.RequireApprovalForDestructive = *override.RequireApprovalForDestructive
		}
		if override.RequireApprovalForNetwork != nil {
			profile.RequireApprovalForNetwork = *override.RequireApprovalForNetwork
		}
		profiles[class] = profile
	}
	return profiles
}

type memoryEmbeddingAdapter struct{ provider memory.EmbeddingProvider }

func (a memoryEmbeddingAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	e, err := a.provider.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(e) == 0 {
		return nil, nil
	}
	out := make([]float64, len(e))
	for i := range e {
		out[i] = float64(e[i])
	}
	return out, nil
}

type userHookInterceptor struct{ mgr *hook.UserHookManager }

func newUserHookInterceptor(mgr *hook.UserHookManager) *userHookInterceptor {
	return &userHookInterceptor{mgr: mgr}
}
func (u *userHookInterceptor) Name() string { return "user_hooks" }
func (u *userHookInterceptor) Intercept(ctx context.Context, call *tool.ToolCall, next tool.InterceptorFunc) (*tool.ToolResult, error) {
	if u.mgr == nil {
		return next(ctx, call)
	}
	u.mgr.RunHooks(ctx, hook.HookPreToolUse, map[string]any{"tool_name": call.ToolName, "tool_input": call.Input})
	result, err := next(ctx, call)
	output, toolErr := "", ""
	if result != nil {
		output = result.Output
		toolErr = result.Error
	}
	u.mgr.RunHooks(ctx, hook.HookPostToolUse, map[string]any{"tool_name": call.ToolName, "tool_input": call.Input, "tool_output": output, "tool_error": toolErr})
	return result, err
}

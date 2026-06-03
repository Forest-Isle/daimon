package gateway

import (
	"context"
	"fmt"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"net/http"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/eval"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/feature"
	"github.com/Forest-Isle/IronClaw/internal/health"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/mcp"
	"github.com/Forest-Isle/IronClaw/internal/ratelimit"
	"github.com/Forest-Isle/IronClaw/internal/sandbox"
	"github.com/Forest-Isle/IronClaw/internal/scheduler"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

// Gateway is the central coordinator that wires all modules together.
type Gateway struct {
	cfg              *config.Config
	cfgMu            sync.RWMutex // protects cfg reads/writes (hot-reload swaps the pointer)
	cfgPath          string
	configWatcher    *config.ConfigWatcher
	db               *store.DB
	sessions         *session.Manager
	provider         agent.Provider // stored for completerAdapter use
	agent            *agent.Agent
	cognitiveLoop    *agent.CognitiveLoop
	tools            *tool.Registry
	hookMgr          *hook.Manager
	permEngine       *tool.PermissionEngine
	skillMgr         *skill.Manager
	resultStore      *tool.ResultStore
	mcpManager       *mcp.Manager
	userHookMgr      *hook.UserHookManager // user-configurable hook scripts
	planMode         *agent.PlanMode       // plan->approve->execute flow
	contextMgr       *agent.PipelineContextManager
	features         *feature.Registry
	featureStatePath string // path to ~/.IronClaw/feature_state.json
	healthRegistry   *health.Registry
	healthSrv        *http.Server
	currentMode      atomic.Value // stores string: "simple" | "cognitive"
	codebaseIndex    *agent.CodebaseIndex
	stopCh           chan struct{} // closed in Stop() to signal background goroutines
	stopOnce         sync.Once     // ensures stopCh is closed exactly once
	initCtx          context.Context
	initCancel       context.CancelFunc

	// Subsystems — each manages a group of related fields
	memory        *MemorySubsystem
	channels      *ChannelSubsystem
	dashboard     *DashboardSubsystem
	sandbox       *SandboxSubsystem
	evolution     *EvolutionSubsystem
	tasks         *TaskSubsystem
	observability *ObservabilitySubsystem

	agentDeps agent.AgentDeps // shared dependency bundle
	cmdTable  commandTable    // slash command routing table

	subsystems Subsystems // ordered list for lifecycle management
}

// GatewayOptions configures optional behaviour for Gateway.New.
type GatewayOptions struct {
	// SkipPersistedFeatureState prevents loading ~/.IronClaw/feature_state.json
	// during feature registry initialization. Set to true in eval mode so that
	// eval's forced config overrides cannot be silently reverted by a user's
	// runtime `/feature disable` state from a previous interactive session.
	SkipPersistedFeatureState bool
	// ConfigPath is the path to the YAML config file for hot-reload watching.
	// When set, the gateway will start a ConfigWatcher that reloads the config
	// on file changes and calls OnReload callbacks.
	ConfigPath string
}

func New(cfg *config.Config, opts ...GatewayOptions) (*Gateway, error) {
	gw := &Gateway{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
	var rb rollbackStack
	var err error
	defer func() {
		if err != nil {
			rb.run()
		}
	}()

	// Initialize subsystems with their zero values — fields will be populated
	// by the init* methods below.
	gw.memory = &MemorySubsystem{}
	gw.channels = &ChannelSubsystem{channels: make(map[string]channel.Channel)}
	gw.dashboard = &DashboardSubsystem{}
	gw.sandbox = &SandboxSubsystem{}
	gw.evolution = &EvolutionSubsystem{}
	gw.tasks = &TaskSubsystem{}
	gw.observability = &ObservabilitySubsystem{
		obsShutdown: func(context.Context) {},
	}
	gw.currentMode.Store(cfg.Agent.Mode)
	gw.initCtx, gw.initCancel = context.WithTimeout(context.Background(), 30*time.Second)

	if err = gw.initDatabase(); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	rb.push(func() { gw.db.Close() })

	// Health check registry — always available regardless of dashboard
	gw.healthRegistry = health.NewRegistry()
	gw.healthRegistry.Register("database", health.CheckerFunc(func(ctx context.Context) error {
		return gw.db.PingContext(ctx)
	}))

	obsShutdown, err := initObservability(gw.initCtx, *cfg)
	if err != nil {
		slog.Warn("observability init failed, continuing without telemetry", "err", err)
		obsShutdown = func(context.Context) {}
	}
	gw.observability.obsShutdown = obsShutdown

	opt := GatewayOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Config hot-reload watcher — start after config is loaded, register callbacks later
	if opt.ConfigPath != "" {
		gw.cfgPath = opt.ConfigPath
		gw.configWatcher, err = config.NewConfigWatcher(opt.ConfigPath)
		if err != nil {
			slog.Warn("config: hot-reload unavailable, using static config", "err", err)
			gw.configWatcher = nil
		}
	}

	// Feature registry — register all features, apply config overrides, resolve dependencies
	gw.features = registerFeatures(cfg)
	gw.features.ApplyOverrides(configToOverrides(cfg))
	// Apply any persisted runtime overrides (highest priority — user's explicit choices survive restarts)
	if !opt.SkipPersistedFeatureState {
		if home, err := os.UserHomeDir(); err == nil {
			gw.featureStatePath = feature.DefaultStatePath(filepath.Join(home, ".IronClaw"))
			if persisted, err := feature.LoadOverrides(gw.featureStatePath); err != nil {
				slog.Warn("gateway: failed to load persisted feature state — file may be corrupted, using config defaults", "path", gw.featureStatePath, "err", err)
			} else if len(persisted) > 0 {
				gw.features.ApplyOverrides(persisted)
				slog.Info("gateway: applied persisted feature overrides", "count", len(persisted))
			}
		}
	}
	if err = gw.features.ResolveAndInit(gw.initCtx); err != nil {
		return nil, fmt.Errorf("feature registry: %w", err)
	}

	if err = gw.initToolsAndHooks(); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}

	// Register Docker health check if Docker sandbox is available
	if gw.sandbox.DockerSessionManager() != nil {
		gw.healthRegistry.Register("docker", health.CheckerFunc(func(ctx context.Context) error {
			if !sandbox.ProbeDocker(ctx) {
				return fmt.Errorf("docker daemon not reachable")
			}
			return nil
		}))
	}

	if err = gw.initAgentRuntime(); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if err = gw.initMemorySystem(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	// Update agentDeps with real memory store (initAgentRuntime used defaults)
	if gw.memory.Store() != nil {
		gw.agentDeps.Memory.Store = gw.memory.Store()
		gw.agentDeps.Memory.LifecycleMgr = gw.memory.LifecycleManager()
		gw.agentDeps.Memory.FactExtractor = gw.memory.FactExtractor()
		gw.agentDeps.Memory.BaseDir = gw.memory.MemoryDir()
	}

	// Evolution engine must exist before cognitive agent registers hooks.
	gw.evolution.engine = evolution.NewEngine(cfg.Evolution)
	if err = gw.initCognitiveAgent(); err != nil {
		return nil, fmt.Errorf("cognitive: %w", err)
	}
	if err = gw.initKnowledgeSystem(); err != nil {
		return nil, fmt.Errorf("knowledge: %w", err)
	}
	if gw.memory.Store() != nil {
		procedural := memory.NewProceduralStore(gw.memory.Store(), gw.memory.Embedder())
		gw.memory.cortex = memory.NewUnifiedRetriever(gw.memory.Store(), gw.memory.KBSearcher(), gw.memory.GraphStore(), procedural)
		if gw.cognitiveLoop != nil {
			gw.cognitiveLoop.SetCortexRetriever(gw.memory.Cortex())
		}
	}

	if err = gw.initSkillManager(); err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	if err = gw.initMultiAgent(); err != nil {
		return nil, fmt.Errorf("multi-agent: %w", err)
	}

	// Task ledger
	gw.tasks.taskLedger = taskledger.NewSQLiteTaskLedger(gw.db)
	gw.agentDeps.MultiAgent.TaskLedger = gw.tasks.TaskLedger()

	// Team coordinator
	if gw.features.IsEnabled("team") {
		maxWorkers := cfg.Agent.Team.MaxWorkers
		if maxWorkers <= 0 {
			maxWorkers = 3
		}
		tc := taskledger.NewTeamCoordinator(gw.tasks.TaskLedger(), maxWorkers)
		tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
			if gw.tasks.SubAgentManager() == nil {
				return gw.executeTeamTask(ctx, task)
			}
			taskIDShort := task.ID
			if len(taskIDShort) > 8 {
				taskIDShort = taskIDShort[:8]
			}
			spec := &agent.AgentSpec{
				Name:          fmt.Sprintf("team_%s", taskIDShort),
				Description:   "Team task worker",
				SystemPrompt:  "You are an agent executing a specific task. Be concise and focused.",
				MaxIterations: 10,
			}
			cfg := gw.Config()
			if cfg.Agent.Team.Model != "" {
				spec.Model = cfg.Agent.Team.Model
			}
			_ = spec.Validate()
			result, err := gw.tasks.SubAgentManager().Spawn(ctx, agent.SpawnRequest{
				Spec: spec,
				Task: task.Description,
			})
			if err != nil {
				return "", err
			}
			if result.Status == agent.StatusError {
				return "", fmt.Errorf("task failed: %s", result.Error)
			}
			return result.Summary, nil
		})
		gw.tasks.teamCoordinator = tc
	}

	// Scheduler
	gw.channels.sched = scheduler.New(gw.db, cfg.Scheduler.PollInterval)
	gw.mcpManager = mcp.NewManager()

	// Approval wiring
	gw.agent.SetApprovalFunc(gw.handleApproval)
	if gw.cognitiveLoop != nil {
		gw.cognitiveLoop.SetApprovalFunc(gw.handleApproval)
	}

	// Scheduler handler
	sched := gw.channels.Scheduler()
	sched.SetHandler(func(ctx context.Context, task scheduler.Task) {
		gw.handleInbound(ctx, channel.InboundMessage{
			Channel: task.Channel, ChannelID: task.ChannelID,
			UserID: "scheduler", UserName: "scheduler", Text: task.Prompt,
		})
	})

	// initCogMetrics runs unconditionally so eval runs (dashboard disabled)
	// still populate cogCollector.
	gw.initCogMetrics()

	// Rate limiter must be initialized before dashboard (which may wrap it).
	gw.initRateLimiter()

	if err = gw.initDashboard(); err != nil {
		return nil, fmt.Errorf("dashboard: %w", err)
	}
	rb.push(func() { _ = gw.dashboard.Stop(gw.initCtx) })

	// Populate command table
	gw.cmdTable = commandTable{
		"/tasks":   {gw.handleTasks, true},
		"/team":    {gw.handleTeam, false},
		"/mode":    {gw.handleMode, false},
		"/feature": {gw.handleFeature, false},
		"/config":  {gw.handleConfig, true},
		"/compact": {gw.handleCompact, true},
		"/model":   {gw.handleModel, false},
		"/new":     {gw.handleReset, true},
		"/start":   {gw.handleReset, true},
	}

	// Build subsystems list in dependency order
	gw.subsystems = Subsystems{
		gw.observability, // shutdown last, start first
		gw.memory,
		gw.sandbox,
		gw.evolution,
		gw.tasks,
		gw.channels,
		gw.dashboard,
	}

	gw.bindFeatureLifecycleHooks()

	// Register config hot-reload callbacks now that all subsystems exist.
	if gw.configWatcher != nil {
		gw.configWatcher.OnReload(func(newCfg *config.Config) {
			// Update the gateway's config pointer so all subsystems see the new values.
			gw.cfgMu.Lock()
			gw.cfg = newCfg
			gw.cfgMu.Unlock()
			// Update LLM model on agent
			if gw.agent != nil {
				gw.agent.SetModel(newCfg.LLM.Model)
			}
			// Update rate limiter
			gw.initRateLimiter()
			// Publish config changed event
			if gw.agent != nil {
				gw.agent.EventBus().Publish(agent.ConfigChanged{
					Path: gw.cfgPath,
				})
			}
		})
	}

	rb.cleanups = nil // success — Stop() handles cleanup
	return gw, nil
}

// Config returns the current configuration snapshot under a read lock.
// Callers must NOT modify the returned pointer — it is shared across all readers.
func (gw *Gateway) Config() *config.Config {
	gw.cfgMu.RLock()
	defer gw.cfgMu.RUnlock()
	return gw.cfg
}

// AddChannel registers a channel adapter. Call before Start().
func (gw *Gateway) AddChannel(ch channel.Channel) {
	gw.channels.channels[ch.Name()] = ch
}

// Start initializes all channels and begins processing.
func (gw *Gateway) Start(ctx context.Context) error {
	// Start health check HTTP server — always available independent of dashboard
	gw.startHealthServer()

	// Start MCP servers asynchronously — npx/uvx process startup can take
	// several seconds and should not block the TUI from appearing.
	if len(gw.Config().Tools.MCP.Servers) > 0 {
		go func() {
			var wg sync.WaitGroup
			cfg := gw.Config()
			for name, srv := range cfg.Tools.MCP.Servers {
				name, srv := name, srv
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := gw.mcpManager.StartServer(ctx, name, srv, gw.tools); err != nil {
						slog.Error("mcp server failed to start", "server", name, "err", err)
						if gw.features != nil {
							_ = gw.features.Disable(ctx, "mcp_"+name)
						}
					}
				}()
			}
			wg.Wait()
		}()
	}

	// Start MCP hot-reload watcher (polls ~/.IronClaw/mcp/ for new/removed configs)
	go gw.watchMCPDir(ctx)

	// Start result store cleanup goroutine
	if gw.resultStore != nil {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := gw.resultStore.Cleanup(); err != nil {
						slog.Warn("gateway: result store cleanup failed", "err", err)
					}
				}
			}
		}()
	}

	// Start channels
	for name, ch := range gw.channels.Channels() {
		if err := ch.Start(ctx, gw.handleInbound); err != nil {
			return err
		}
		slog.Info("channel started", "name", name)
	}

	// Start scheduler
	if gw.features.IsEnabled("scheduler") {
		gw.channels.Scheduler().Start(ctx)
		slog.Info("scheduler started")
	}

	// Start HTTP admin server if enabled (standalone only — dashboard has its own server)
	if gw.features.IsEnabled("server") && !gw.featureEnabled("dashboard") {
		go startHTTPServer(gw.Config().Server.Addr, gw.db)
	}

	// Start stale task detector
	if gw.tasks.TaskLedger() != nil {
		if err := gw.tasks.Start(ctx); err != nil {
			slog.Warn("gateway: stale detector start failed", "err", err)
		}
	}

	// Start evolution engine only when feature is enabled
	if gw.evolution.Engine() != nil && gw.featureEnabled("evolution") {
		gw.evolution.Engine().Start()
	}

	slog.Info("gateway started")
	return nil
}

// SetDashboardEmitter replaces the current DashboardEmitter on the agent deps
// and context manager. Prefer AddDashboardEmitter when multiple consumers must
// coexist (e.g. web dashboard + TUI).
func (gw *Gateway) SetDashboardEmitter(e agent.DashboardEmitter) {
	gw.dashboard.emitter = e
	gw.agentDeps.Observability.Emitter = e
	if gw.contextMgr != nil {
		gw.contextMgr.SetDashboardEmitter(e)
	}
}

// AddDashboardEmitter merges e with the existing emitter so both the web
// dashboard and channel-specific consumers (e.g. TUI status bar) receive events.
func (gw *Gateway) AddDashboardEmitter(e agent.DashboardEmitter) {
	merged := agent.NewMultiEmitter(gw.dashboard.Emitter(), e)
	gw.SetDashboardEmitter(merged)
}

// SetMetricsEmitter sets a MetricsEmitter on the agent deps for TUI status reporting.
func (gw *Gateway) SetMetricsEmitter(e agent.MetricsEmitter) {
	gw.agentDeps.Observability.MetricsEmitter = e
}

// Stop gracefully shuts down all components.
func (gw *Gateway) Stop(ctx context.Context) error {
	// Stop subsystems in reverse order
	gw.subsystems.StopAll(ctx)

	_ = gw.mcpManager.Close()

	// Persist evolution state and stop engine (only when feature was enabled)
	if gw.evolution.Engine() != nil && gw.featureEnabled("evolution") {
		prefPath := ""
		if p, err := gw.resolveEvolutionPreferencePath(gw.Config().Evolution.PreferenceFile); err == nil {
			prefPath = p
		}
		gw.evolution.Engine().SaveState(prefPath)
		gw.evolution.Engine().Stop()
	}

	if gw.healthSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gw.healthSrv.Shutdown(shutCtx)
	}

	gw.stopOnce.Do(func() { close(gw.stopCh) })
	if gw.initCancel != nil {
		gw.initCancel()
	}

	if gw.configWatcher != nil {
		gw.configWatcher.Stop()
	}

	_ = gw.db.Close()
	slog.Info("gateway stopped")
	return nil
}

// CurrentMode returns the active agent mode ("simple" or "cognitive").
func (gw *Gateway) CurrentMode() string {
	return gw.currentMode.Load().(string)
}

// SetMode atomically switches the active agent mode and strategy.
// Returns an error if mode is not "simple" or "cognitive".
func (gw *Gateway) SetMode(mode string) error {
	if mode != "simple" && mode != "cognitive" {
		return fmt.Errorf("unknown mode %q: valid modes are simple, cognitive", mode)
	}
	switch mode {
	case "cognitive":
		if gw.agent != nil && gw.cognitiveLoop != nil {
			gw.agent.SetStrategy(gw.cognitiveLoop)
		}
	default:
		if gw.agent != nil {
			gw.agent.SetStrategy(&agent.SimpleLoop{})
		}
	}
	gw.currentMode.Store(mode)
	slog.Info("gateway: mode switched", "mode", mode)
	return nil
}

// CogMetricsCollector returns the cognitive-metrics collector, or nil when
// evolution is not enabled. Used by the eval harness to populate CogHealth.
func (gw *Gateway) CogMetricsCollector() *cogmetrics.Collector {
	return gw.evolution.Collector()
}

// NewEvalRunner creates an eval.AgentRunner backed by the gateway's cognitive agent.
// It also wires the runner's compression emitter into the context manager so that
// context compression events are captured even when the dashboard is disabled.
func (gw *Gateway) NewEvalRunner() *eval.CognitiveAgentRunner {
	if gw.agent == nil { // defensive: should not happen after init
		return nil
	}
	r := eval.NewCognitiveAgentRunner(gw.agent)
	r.SetCogCollector(gw.evolution.Collector())
	r.SetMemoryStore(gw.memory.Store())
	// Route context compression events through the eval hook so they appear
	// in EvalResult.CompressionEvents even when the dashboard is disabled.
	if gw.contextMgr != nil {
		gw.contextMgr.SetDashboardEmitter(
			agent.NewMultiEmitter(gw.dashboard.Emitter(), r.CompressionEmitter()),
		)
	}
	return r
}

// EvolutionEngine returns the gateway's evolution engine, or nil if evolution
// is not configured. Used by the eval longitudinal command to trigger insights
// cycles between benchmark iterations.
func (gw *Gateway) EvolutionEngine() *evolution.Engine {
	return gw.evolution.Engine()
}

// LLMProvider returns the gateway's LLM provider for external use (e.g. eval judging).
func (gw *Gateway) LLMProvider() agent.Provider {
	return gw.provider
}

// Features returns the feature registry, allowing callers to inspect which
// features are enabled. Returns nil if the registry was not initialized.
func (gw *Gateway) Features() *feature.Registry {
	return gw.features
}

// handleInbound routes incoming messages to the agent runtime.
func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
	if msg.Text == "" {
		return
	}

	// Rate limiting for agent message handling
	if limiter := gw.observability.RateLimiter(); limiter != nil {
		allowed, waitTime, err := limiter.Allow(ctx, msg.UserID)
		if err != nil {
			slog.Warn("gateway: rate limiter error, allowing message", "err", err, "user", msg.UserID)
		} else if !allowed {
			slog.Warn("gateway: rate limited", "user", msg.UserID, "wait", waitTime)
			ch, ok := gw.channels.Channels()[msg.Channel]
			if ok {
				if err := ch.Send(ctx, channel.OutboundMessage{
					Channel:   msg.Channel,
					ChannelID: msg.ChannelID,
					Text:      "You are sending messages too quickly. Please wait a moment before trying again.",
				}); err != nil {
					slog.Warn("failed to send message", "err", err)
				}
			}
			return
		}
	}

	ch, ok := gw.channels.Channels()[msg.Channel]
	if !ok {
		slog.Error("unknown channel", "channel", msg.Channel)
		return
	}

	// Attach stream callback for channels that support real-time tool output.
	if streamWriter, ok := ch.(channel.ToolStreamWriter); ok {
		target := channel.MessageTarget{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
		}
		ctx = tool.WithStreamCallback(ctx, func(chunk string) {
			if err := streamWriter.WriteToolStream(ctx, target, "bash", chunk); err != nil {
				slog.Warn("gateway: tool stream write failed", "err", err)
			}
		})
	}

	// Command dispatch via command table
	if gw.cmdTable != nil {
		if resp, handled := gw.cmdTable.dispatch(ctx, ch, msg); handled {
			if resp != "" {
				if err := ch.Send(ctx, channel.OutboundMessage{
					Channel:   msg.Channel,
					ChannelID: msg.ChannelID,
					Text:      resp,
				}); err != nil {
					slog.Warn("failed to send command response", "err", err)
				}
			}
			return
		}
	}

	slog.Info("message received", "channel", msg.Channel, "user", msg.UserName, "text_len", len(msg.Text))

	if err := gw.agent.HandleMessage(ctx, ch, msg); err != nil {
		slog.Error("agent error", "err", err)
		if err := ch.Send(ctx, channel.OutboundMessage{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			Text:      "Error: " + err.Error(),
		}); err != nil {
			slog.Warn("failed to send message", "err", err)
		}
	}
}

// handleApproval sends an approval request via the channel and waits for response.
// Channels that implement channel.ApprovalSender get interactive approval;
// all others auto-approve.
func (gw *Gateway) handleApproval(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error) {
	if sender, ok := ch.(channel.ApprovalSender); ok {
		return sender.SendApprovalRequest(ctx, target, toolName, input)
	}
	// Channel does not support interactive approval — auto-approve.
	return true, nil
}

// sendMemoryNotification sends a lightweight memory operation summary via the channel.
// Channels that implement channel.NotificationSender get the notification;
// all others silently skip it.
func (gw *Gateway) sendMemoryNotification(ctx context.Context, ch channel.Channel, target channel.MessageTarget, summary string) {
	if sender, ok := ch.(channel.NotificationSender); ok {
		if err := sender.SendNotification(ctx, target, summary); err != nil {
			slog.Warn("gateway: memory notification failed", "err", err)
		}
	}
}

// completerAdapter bridges agent.Provider to memory.Completer.
type completerAdapter struct {
	provider agent.Provider
	model    string
}

func (a *completerAdapter) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	req := agent.CompletionRequest{
		Model:     a.model,
		System:    systemPrompt,
		Messages:  []agent.CompletionMessage{{Role: "user", Content: userMessage}},
		MaxTokens: 512,
	}
	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// defaultSkillsDir returns the path to ~/.IronClaw/skills/.
func defaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".IronClaw", "skills")
}

// noopKBEmbedder is a no-op EmbeddingProvider used when no OpenAI key is configured.
// It causes the knowledge base to fall back to BM25/LIKE text search only.
type noopKBEmbedder struct{}

func (n *noopKBEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

func (n *noopKBEmbedder) Dimensions() int {
	return 0
}

// watchMCPDir periodically scans ~/.IronClaw/mcp/ and syncs MCP servers.
// New yaml files trigger server startup; removed files trigger shutdown.
func (gw *Gateway) watchMCPDir(ctx context.Context) {
	pollInterval := gw.Config().Tools.MCP.PollInterval
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			desired := userdir.ScanMCPDir()
			if desired == nil {
				desired = make(map[string]config.MCPServerConfig)
			}
			// Merge project-level MCP config (project config always takes priority).
			cfg := gw.Config()
		for name, srv := range cfg.Tools.MCP.Servers {
				desired[name] = srv
			}
			gw.mcpManager.SyncServers(ctx, desired, gw.tools)
		}
	}
}

// defToSpec converts a config.AgentDefinition to an agent.AgentSpec.
func defToSpec(def config.AgentDefinition) *agent.AgentSpec {
	return &agent.AgentSpec{
		Name:          def.Name,
		Description:   def.Description,
		SystemPrompt:  def.SystemPrompt,
		Model:         def.Model,
		MaxTokens:     def.MaxTokens,
		MaxIterations: def.MaxIterations,
		Tools:         def.Tools,
		Tags:          def.Tags,
		Mode:          def.Mode,
	}
}

// handleTasksCommand lists running and pending tasks from the task ledger.
func (gw *Gateway) handleTasksCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	target := channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID}

	if gw.tasks.TaskLedger() == nil {
		target.Text = "Task ledger not available."
		if err := ch.Send(ctx, target); err != nil {
			slog.Warn("failed to send message", "err", err)
		}
		return
	}

	running := taskledger.TaskStateRunning
	runningTasks, err := gw.tasks.TaskLedger().List(ctx, taskledger.TaskFilter{State: &running})
	if err != nil {
		target.Text = "Error: failed to list tasks: " + err.Error()
		if err := ch.Send(ctx, target); err != nil {
			slog.Warn("failed to send message", "err", err)
		}
		return
	}

	pending := taskledger.TaskStatePending
	pendingTasks, err := gw.tasks.TaskLedger().List(ctx, taskledger.TaskFilter{State: &pending})
	if err != nil {
		target.Text = "Error: failed to list tasks: " + err.Error()
		if err := ch.Send(ctx, target); err != nil {
			slog.Warn("failed to send message", "err", err)
		}
		return
	}

	var b strings.Builder
	b.WriteString("**Task Ledger**\n\n")

	if len(runningTasks) == 0 && len(pendingTasks) == 0 {
		b.WriteString("No active tasks.")
	} else {
		if len(runningTasks) > 0 {
			fmt.Fprintf(&b, "Running (%d):\n", len(runningTasks))
			for _, t := range runningTasks {
				age := time.Since(t.CreatedAt).Truncate(time.Second)
				fmt.Fprintf(&b, "  ▶ [%s] %s (%s ago)\n", t.Kind, t.Title, age)
			}
		}
		if len(pendingTasks) > 0 {
			if len(runningTasks) > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "Pending (%d):\n", len(pendingTasks))
			for _, t := range pendingTasks {
				fmt.Fprintf(&b, "  ○ [%s] %s\n", t.Kind, t.Title)
			}
		}
	}

	target.Text = b.String()
	if err := ch.Send(ctx, target); err != nil {
		slog.Warn("failed to send message", "err", err)
	}
}

// handleTeamCommand breaks a goal into parallel tasks using the LLM and executes them.
func (gw *Gateway) handleTeamCommand(ctx context.Context, goal string) string {
	if gw.tasks.TeamCoordinator() == nil {
		return "Team mode is not enabled. Set agent.team.enabled: true in config."
	}

	cfg := gw.Config()
	prompt := fmt.Sprintf(taskledger.TeamPlanPrompt, goal)
	req := agent.CompletionRequest{
		Model:     gw.agent.Model(),
		System:    "You are a task planning assistant. Output only valid JSON.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: cfg.LLM.MaxTokens,
	}
	resp, err := gw.provider.Complete(ctx, req)
	if err != nil {
		return fmt.Sprintf("Failed to generate plan: %v", err)
	}

	rootID := fmt.Sprintf("team_%d", time.Now().UnixNano())
	rootTask := taskledger.Task{
		ID:    rootID,
		Kind:  taskledger.TaskKindTeamTask,
		State: taskledger.TaskStateRunning,
		Title: util.TruncateStr(goal, 100),
	}
	if err := gw.tasks.TaskLedger().Register(ctx, rootTask); err != nil {
		return fmt.Sprintf("Failed to register root task: %v", err)
	}

	tasks, err := taskledger.ParseTaskPlan(resp.Text, rootID)
	if err != nil {
		return fmt.Sprintf("Failed to parse plan: %v", err)
	}

	for _, t := range tasks {
		if err := gw.tasks.TeamCoordinator().AddTask(ctx, t); err != nil {
			return fmt.Sprintf("Failed to add task %s: %v", t.ID, err)
		}
	}

	result, err := gw.tasks.TeamCoordinator().RunWithExecutor(ctx)
	if err != nil {
		return fmt.Sprintf("Team execution failed: %v", err)
	}

	now := time.Now().UTC()
	rootTask.State = taskledger.TaskStateCompleted
	rootTask.CompletedAt = &now
	rootTask.Result = result.Summary
	if err := gw.tasks.TaskLedger().Update(ctx, rootTask); err != nil {
		slog.Warn("gateway: failed to update root task", "err", err)
	}

	return fmt.Sprintf("Team completed: %d tasks done, %d failed", result.TasksCompleted, result.TasksFailed)
}

// handleModeCommand processes the /mode command argument.
// arg="" means query-only; arg="simple"|"cognitive" switches mode.
func (gw *Gateway) handleModeCommand(arg string) string {
	current := gw.CurrentMode()
	if arg == "" {
		return fmt.Sprintf("Mode: %s", current)
	}
	if arg != "simple" && arg != "cognitive" {
		return fmt.Sprintf("Error: unknown mode %q. Valid modes: simple, cognitive", arg)
	}
	if arg == current {
		return fmt.Sprintf("Already in %s mode", current)
	}
	if err := gw.SetMode(arg); err != nil {
		slog.Warn("gateway: set mode failed", "mode", arg, "err", err)
	}
	return fmt.Sprintf("Mode switched to %s (was: %s)", arg, current)
}

// executeTeamTask runs a single team task by creating a temporary session
// and routing through the main agent runtime.
func (gw *Gateway) executeTeamTask(ctx context.Context, task taskledger.Task) (string, error) {
	cfg := gw.Config()
	req := agent.CompletionRequest{
		Model:     gw.agent.Model(),
		System:    "You are an agent executing a specific task. Be concise and focused.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: task.Description}},
		MaxTokens: cfg.LLM.MaxTokens,
	}
	resp, err := gw.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// initRateLimiter creates the rate limiter based on config.
func (gw *Gateway) initRateLimiter() {
	cfg := gw.Config()
	if !cfg.RateLimit.Enabled {
		gw.observability.rateLimiter = ratelimit.NoopLimiter{}
		slog.Info("rate limiting disabled")
		return
	}

	rate := cfg.RateLimit.RequestsPerSec
	if rate <= 0 {
		rate = 10
	}
	burst := cfg.RateLimit.Burst
	if burst <= 0 {
		burst = 20
	}

	gw.observability.rateLimiter = ratelimit.NewPerKeyLimiter(rate, burst, 5*time.Minute)
	slog.Info("rate limiting enabled", "requests_per_sec", rate, "burst", burst)
}

// SetRateLimiter replaces the rate limiter. Used for testing.
func (gw *Gateway) SetRateLimiter(l ratelimit.Limiter) {
	gw.observability.rateLimiter = l
}

// RateLimiter returns the current rate limiter.
func (gw *Gateway) RateLimiter() ratelimit.Limiter {
	return gw.observability.RateLimiter()
}

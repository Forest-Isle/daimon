package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"net/http"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/memorywire"
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
	tools            *tool.Registry
	hookMgr          *hook.Manager
	permEngine       *tool.PermissionEngine
	trustTracker     *tool.TrustTracker
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
	currentMode      atomic.Value // stores string: "simple" | "unified"
	codebaseIndex    *agent.CodebaseIndex
	stopCh           chan struct{} // closed in Stop() to signal background goroutines
	stopOnce         sync.Once     // ensures stopCh is closed exactly once
	initCtx          context.Context
	initCancel       context.CancelFunc

	// Subsystems — each manages a group of related fields
	memory        *MemorySubsystem
	channels      *ChannelSubsystem
	sandbox       *SandboxSubsystem
	evolution     *EvolutionSubsystem
	tasks         *TaskSubsystem
	observability *ObservabilitySubsystem

	// emitter is the active ObservabilityEmitter shared by the agent runtime,
	// context manager, and channel consumers (e.g. TUI status bar). Nil means
	// no emitter is configured; agent deps substitute a discard emitter.
	emitter agent.ObservabilityEmitter

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

	// Health check registry — always available
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
	if err = gw.initPlanAndEvolution(); err != nil {
		return nil, fmt.Errorf("plan/evolution: %w", err)
	}
	if err = gw.initKnowledgeSystem(); err != nil {
		return nil, fmt.Errorf("knowledge: %w", err)
	}
	if gw.memory.Store() != nil {
		procedural := memory.NewProceduralStore(gw.memory.Store(), gw.memory.Embedder())
		gw.memory.cortex = memory.NewUnifiedRetriever(gw.memory.Store(), gw.memory.KBSearcher(), gw.memory.GraphStore(), procedural, gw.memory.Embedder())
		// Register core_memory tool so the LLM can actively manage its own
		// persistent memory (Mem0/Letta pattern — agentic memory writes).
		gw.tools.Register(tool.NewCoreMemoryTool(gw.memory.Store(), gw.memory.LifecycleManager()))

		// Initialize AMP (Agent Memory Protocol) adapter for standards-compliant
		// memory operations (Memorywire: remember/recall/forget/merge/expire).
		gw.memory.ampAdapter = memorywire.NewAdapter(gw.memory.Store(), gw.memory.Embedder())

		// Register AMP memory tool so the LLM can use standardized AMP operations.
		gw.tools.Register(tool.NewAMPMemoryTool(gw.memory.ampAdapter))
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

	// Scheduler handler
	sched := gw.channels.Scheduler()
	sched.SetHandler(func(ctx context.Context, task scheduler.Task) {
		gw.handleInbound(ctx, channel.InboundMessage{
			Channel: task.Channel, ChannelID: task.ChannelID,
			UserID: "scheduler", UserName: "scheduler", Text: task.Prompt,
		})
	})

	// initCogMetrics runs unconditionally so eval runs still populate
	// cogCollector.
	gw.initCogMetrics()

	gw.initRateLimiter()

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
	// Start health check HTTP server — always available
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

	// Start HTTP admin server if enabled (standalone)
	if gw.features.IsEnabled("server") {
		go startHTTPServer(gw.Config().Server.Addr, gw.db, gw.memory.Store())
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

		// Wire SkillOpt text-space optimizer into the evolution engine.
		// Uses the gateway's LLM provider for candidate generation.
		if gw.provider != nil {
			skillCompleter := newSkillOptCompleter(gw.provider)
			skillOpt := evolution.NewLLMSkillOpt(0.3, skillCompleter)
			gw.evolution.Engine().SetSkillOpt(skillOpt)
			slog.Info("gateway: SkillOpt optimizer wired")
		}
	}

	slog.Info("gateway started")
	return nil
}

// SetObservabilityEmitter replaces the current ObservabilityEmitter on the agent deps
// and context manager. Prefer AddObservabilityEmitter when multiple consumers must
// coexist (e.g. TUI status bar alongside another consumer).
func (gw *Gateway) SetObservabilityEmitter(e agent.ObservabilityEmitter) {
	gw.emitter = e
	gw.agentDeps.Observability.Emitter = e
	if gw.contextMgr != nil {
		gw.contextMgr.SetObservabilityEmitter(e)
	}
}

// AddObservabilityEmitter merges e with the existing emitter so multiple
// channel-specific consumers (e.g. TUI status bar) receive events.
func (gw *Gateway) AddObservabilityEmitter(e agent.ObservabilityEmitter) {
	merged := agent.NewMultiEmitter(gw.emitter, e)
	gw.SetObservabilityEmitter(merged)
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

// CurrentMode returns the active agent mode ("simple" or "unified").
func (gw *Gateway) CurrentMode() string {
	return gw.currentMode.Load().(string)
}

// SetMode atomically switches the active agent mode and strategy.
// Valid modes: "simple", "unified" (also accepts "cognitive" for backward compat).
func (gw *Gateway) SetMode(mode string) error {
	if mode != "simple" && mode != "unified" && mode != "cognitive" {
		return fmt.Errorf("unknown mode %q: valid modes are simple, unified", mode)
	}
	switch mode {
	case "unified", "cognitive": // "cognitive" kept for backward compat
		if gw.agent != nil {
			gw.agent.SetStrategy(&agent.UnifiedLoop{})
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

// skillOptCompleter bridges agent.Provider to evolution.Completer for SkillOpt.
type skillOptCompleter struct {
	provider agent.Provider
}

func newSkillOptCompleter(provider agent.Provider) *skillOptCompleter {
	return &skillOptCompleter{provider: provider}
}

func (a *skillOptCompleter) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	req := agent.CompletionRequest{
		System:    systemPrompt,
		Messages:  []agent.CompletionMessage{{Role: "user", Content: userMessage}},
		MaxTokens: 256,
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

// handleTeamCommand breaks a goal into parallel tasks using the LLM and executes them.

// handleModeCommand processes the /mode command argument.
// arg="" means query-only; arg="simple"|"cognitive" switches mode.

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

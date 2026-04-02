package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/agent"
	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/hook"
	"github.com/punkopunko/ironclaw/internal/knowledge"
	"github.com/punkopunko/ironclaw/internal/knowledge/graph"
	"github.com/punkopunko/ironclaw/internal/mcp"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/rl"
	"github.com/punkopunko/ironclaw/internal/scheduler"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/skill"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
	"github.com/punkopunko/ironclaw/internal/userdir"
)

// Gateway is the central coordinator that wires all modules together.
type Gateway struct {
	cfg            *config.Config
	db             *store.DB
	sessions       *session.Manager
	runtime        *agent.Runtime
	cognitiveAgent *agent.CognitiveAgent
	tools          *tool.Registry
	channels       map[string]channel.Channel
	sched          *scheduler.Scheduler
	mcpManager     *mcp.Manager
	rlTrainer      *rl.Trainer
	resultStore    *tool.ResultStore
	mu             sync.Mutex
}

func New(cfg *config.Config) (*Gateway, error) {
	// Open database
	db, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, err
	}

	// Session manager
	sessions := session.NewManager(db)

	// Tool registry
	tools := tool.NewRegistry()
	policy := tool.NewPolicy(cfg.Tools.Bash.BlockedCommands)

	if cfg.Tools.Bash.Enabled {
		tools.Register(tool.NewBashTool(cfg.Tools.Bash.Timeout, cfg.Tools.Bash.RequiresApproval, policy))
	}
	if cfg.Tools.File.Enabled {
		tools.Register(tool.NewFileTool(cfg.Tools.File.RequiresApproval))
	}
	if cfg.Tools.HTTP.Enabled {
		tools.Register(tool.NewHTTPTool(cfg.Tools.HTTP.Timeout, cfg.Tools.HTTP.RequiresApproval))
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
	hookMgr := hook.BuildManager(preToolUseCfg, postToolUseCfg, onUserMsgCfg, preCompactCfg)
	slog.Info("hook system initialized")

	// Permission engine
	permRules := make([]tool.PermissionRule, len(cfg.Permissions.Rules))
	for i, r := range cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	permEngine := tool.NewPermissionEngine(permRules, cfg.Permissions.Default, policy)

	// LLM provider
	provider := agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)

	// Agent runtime
	runtime := agent.NewRuntime(provider, tools, sessions, db, cfg.Agent, cfg.LLM)

	// Wire hook manager and permission engine
	runtime.SetHookManager(hookMgr)
	runtime.SetPermissionEngine(permEngine)

	// Tool result persistence
	var resultStore *tool.ResultStore
	if cfg.Tools.ResultPersistence.Enabled {
		resultStore = tool.NewResultStore(
			cfg.Tools.ResultPersistence.CacheDir,
			cfg.Tools.ResultPersistence.ThresholdBytes,
			cfg.Tools.ResultPersistence.PreviewChars,
			cfg.Tools.ResultPersistence.TTLHours,
		)
		runtime.SetResultStore(resultStore)
		// Startup cleanup sweep
		if err := resultStore.Cleanup(); err != nil {
			slog.Warn("gateway: result store startup cleanup failed", "err", err)
		}
		slog.Info("tool result persistence enabled",
			"threshold", cfg.Tools.ResultPersistence.ThresholdBytes,
			"ttl_hours", cfg.Tools.ResultPersistence.TTLHours,
		)
	}

	// Concurrent tool execution
	runtime.SetConcurrentConfig(cfg.Tools.ConcurrentExecution)
	if cfg.Tools.ConcurrentExecution.Enabled {
		slog.Info("concurrent tool execution enabled", "max_concurrency", cfg.Tools.ConcurrentExecution.MaxConcurrency)
	}

	// Memory store
	var memStore memory.Store
	var factExtractor *memory.LLMFactExtractor
	var lifecycleMgr *memory.LifecycleManager

	if cfg.Memory.Enabled {
		var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
		if cfg.Memory.OpenAIAPIKey != "" {
			baseEmbedder := memory.NewOpenAIEmbedding(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel)
			embedder = memory.NewCachedEmbedder(baseEmbedder)
			slog.Info("memory: cached embedder enabled")
		}
		memCfg := memory.MemoryConfig{
			FactExtraction:           cfg.Memory.FactExtraction,
			SimilarityThreshold:      cfg.Memory.SimilarityThreshold,
			ConsolidationInterval:    cfg.Memory.ConsolidationInterval,
			BM25Weight:               cfg.Memory.BM25Weight,
			VectorWeight:             cfg.Memory.VectorWeight,
			EnableVSS:                cfg.Memory.EnableVSS,
			VectorDimension:          cfg.Memory.VectorDimension,
			EnableSearchCache:        cfg.Memory.EnableSearchCache,
			SearchCacheSize:          cfg.Memory.SearchCacheSize,
			SearchCacheTTL:           cfg.Memory.SearchCacheTTL,
			ReflectionCountThreshold: cfg.Memory.ReflectionCountThreshold,
			ReflectionDriftThreshold: cfg.Memory.ReflectionDriftThreshold,
			ReflectionL2Trigger:      cfg.Memory.ReflectionL2Trigger,
		}

		// File-based storage
		storageDir := cfg.Memory.StorageDir
		if storageDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			storageDir = filepath.Join(home, ".IronClaw", "memory")
		}

		// Create file store
		fileStore, err := memory.NewFileMemoryStore(storageDir, db.DB, embedder, memCfg)
		if err != nil {
			return nil, fmt.Errorf("create file memory store: %w", err)
		}
		memStore = fileStore

		slog.Info("memory: file-based storage enabled", "dir", storageDir)

		runtime.SetMemoryStore(memStore)

		// Set memory base dir on runtime for user profile injection
		runtime.SetMemoryBaseDir(storageDir)

		// Initialize incremental compressor
		compressor := memory.NewIncrementalCompressor(storageDir, &completerAdapter{provider: provider, model: cfg.LLM.Model})
		runtime.SetCompressor(compressor)
		slog.Info("memory: incremental compressor enabled")

		// Initialize forgetting curve manager
		forgettingCurve := memory.NewForgettingCurveManager(db)

		if cfg.Memory.FactExtraction {
			completer := &completerAdapter{provider: provider, model: cfg.LLM.Model}
			factExtractor = memory.NewLLMFactExtractor(completer, memCfg)

			// Create reflection tracker for automatic L1/L2 reflections
			var reflector *memory.ReflectionTracker
			reflector = memory.NewReflectionTracker(memStore, completer, embedder, memCfg, db.DB)
			slog.Info("memory: reflection tracker enabled")

			lifecycleMgr = memory.NewLifecycleManager(memStore, embedder, completer, memCfg, reflector)

			// Start compactor background task
			compactor := memory.NewCompactor(memStore, completer, db.DB, storageDir, memCfg)
			compactor.Start(context.Background())
			slog.Info("memory: compactor enabled")
			// Note: compactor.Stop() should be called on shutdown

			// Create profiler (triggered by reflection completion, not a background task)
			profiler := memory.NewProfiler(memStore, completer, db.DB, storageDir, memCfg)
			_ = profiler // Profiler is triggered by ReflectionTracker callbacks
			// TODO: Add profiler callback to reflector once ReflectionTracker supports it
			slog.Info("memory: profiler created")
		}

		// Register memory_manage tool
		memTool := tool.NewMemoryManageTool(memStore, db.DB, storageDir)
		tools.Register(memTool)
		slog.Info("memory: memory_manage tool registered")

		// Schedule daily retention policy enforcement alongside fade task
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				if err := forgettingCurve.FadeWeakMemoriesFromFiles(context.Background(), storageDir); err != nil {
					slog.Warn("memory: fade weak memory files failed", "err", err)
				}
				if err := forgettingCurve.FadeByRetentionPolicy(context.Background(), storageDir, memCfg); err != nil {
					slog.Warn("memory: retention policy enforcement failed", "err", err)
				}
			}
		}()
		slog.Info("memory: forgetting curve and retention policy enabled (file-based)")
	}

	// Optionally build cognitive agent
	var cognitiveAgent *agent.CognitiveAgent
	if cfg.Agent.Mode == "cognitive" {
		cognitiveAgent = agent.NewCognitiveAgent(provider, tools, sessions, db, cfg.Agent, cfg.LLM)
		if memStore != nil {
			cognitiveAgent.SetMemoryStore(memStore)
		}
		if factExtractor != nil {
			cognitiveAgent.SetFactExtractor(factExtractor)
		}
		if lifecycleMgr != nil {
			cognitiveAgent.SetLifecycleManager(lifecycleMgr)
		}
	}

	// RL System (requires cognitive agent)
	var rlTrainer *rl.Trainer
	if cfg.Agent.RL.Enabled && cognitiveAgent != nil {
		rlStorage := rl.NewStorage(db)
		rlPolicy := rl.NewPolicy(rlStorage, cfg.Agent.RL)
		if err := rlPolicy.LoadCheckpoint(context.Background()); err != nil {
			slog.Warn("gateway: failed to load RL checkpoint", "err", err)
		}
		rlTrainer = rl.NewTrainer(rlPolicy, cfg.Agent.RL)
		cognitiveAgent.SetRLPolicy(rlPolicy)
		cognitiveAgent.SetRLTrainer(rlTrainer)
		slog.Info("RL system initialized")
	}

	// Knowledge base (Phase 2)
	if cfg.Knowledge.Enabled {
		kbCfg := knowledge.Config{
			ChunkSize:         cfg.Knowledge.ChunkSize,
			ChunkOverlap:      cfg.Knowledge.ChunkOverlap,
			BM25Weight:        cfg.Knowledge.BM25Weight,
			VectorWeight:      cfg.Knowledge.VectorWeight,
			IngestDirs:        cfg.Knowledge.IngestDirs,
			EnableSearchCache: cfg.Knowledge.EnableSearchCache,
			SearchCacheSize:   cfg.Knowledge.SearchCacheSize,
			SearchCacheTTL:    cfg.Knowledge.SearchCacheTTL,
		}
		var kbEmbedder knowledge.EmbeddingProvider
		if cfg.Memory.OpenAIAPIKey != "" {
			kbEmbedder = memory.NewOpenAIEmbedding(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel)
		} else {
			kbEmbedder = &noopKBEmbedder{}
		}
		kb := knowledge.New(db, kbEmbedder, kbCfg)

		// Build reranker + hybrid retriever (used as the searcher for perceiver)
		var reranker knowledge.Reranker = &knowledge.NoopReranker{}
		if cfg.Knowledge.Reranker.Enabled && cfg.Knowledge.Reranker.Provider == "llm" {
			llmCompleter := &completerAdapter{provider: provider, model: cfg.LLM.Model}
			reranker = knowledge.NewLLMReranker(llmCompleter)
		}
		retriever := knowledge.NewHybridRetriever(kb, reranker)

		// Ingest configured directories at startup
		for _, dir := range cfg.Knowledge.IngestDirs {
			if err := kb.GetPipeline().IngestDir(context.Background(), dir); err != nil {
				slog.Warn("gateway: failed to ingest dir", "dir", dir, "err", err)
			}
		}

		if cognitiveAgent != nil {
			cognitiveAgent.SetKnowledgeSearcher(retriever)
		}

		// Knowledge graph (Phase 3)
		if cfg.Knowledge.GraphEnabled || cfg.Graph.Enabled {
			kg := graph.NewSQLiteGraph(db)
			llmCompleter := &completerAdapter{provider: provider, model: cfg.LLM.Model}
			extractor := graph.NewLLMEntityExtractor(kg, llmCompleter)

			// Extract entities from already-ingested chunks in background
			go func() {
				sources, err := kb.Sources(context.Background())
				if err != nil {
					slog.Warn("gateway: failed to list KB sources for graph extraction", "err", err)
					return
				}
				for _, src := range sources {
					results, err := kb.Search(context.Background(), knowledge.KnowledgeQuery{
						Text:       "",
						SourceType: src.SourceType,
						Limit:      50,
					})
					if err != nil {
						continue
					}
					for _, r := range results {
						extractor.Extract(context.Background(), r.Chunk.Content, "kb_chunk", r.Chunk.ID) //nolint:errcheck
					}
				}
				slog.Info("gateway: initial graph entity extraction complete")
			}()

			if cognitiveAgent != nil {
				cognitiveAgent.SetKnowledgeGraph(kg)
				cognitiveAgent.SetEntityExtractor(extractor)
			}

			// Wire GraphSync to lifecycle manager for memory→graph synchronization
			if lifecycleMgr != nil {
				graphSync := graph.NewGraphSync(kg, extractor)
				lifecycleMgr.SetGraphSync(graphSync)
				slog.Info("knowledge graph: memory lifecycle sync enabled")
			}

			// Start graph decay background task
			graphDecay := graph.NewGraphDecayTask(kg, 24*time.Hour)
			go graphDecay.Start(context.Background())
			slog.Info("knowledge graph: decay task started")

			slog.Info("knowledge graph initialized")
		}

		slog.Info("knowledge base initialized", "ingest_dirs", cfg.Knowledge.IngestDirs)
	}

	// Skill manager — load builtin skills, then ~/.IronClaw/skills/ and any extra dirs
	var skillMgr *skill.Manager
	if cfg.Skills.Enabled {
		skillMgr = skill.New()
		if err := skillMgr.LoadBuiltin(); err != nil {
			slog.Warn("gateway: failed to load builtin skills", "err", err)
		}
		userSkillsDir := defaultSkillsDir()
		if err := skillMgr.LoadDir(userSkillsDir); err != nil {
			slog.Warn("gateway: failed to load user skills", "dir", userSkillsDir, "err", err)
		}
		for _, dir := range cfg.Skills.ExtraDirs {
			if err := skillMgr.LoadDir(dir); err != nil {
				slog.Warn("gateway: failed to load extra skills dir", "dir", dir, "err", err)
			}
		}
		runtime.SetSkillManager(skillMgr)
		if cognitiveAgent != nil {
			cognitiveAgent.SetSkillManager(skillMgr)
		}
		// Register the read_skill tool for progressive disclosure —
		// agent sees metadata in prompt, loads full content via this tool.
		tools.Register(tool.NewSkillTool(skillMgr))
		slog.Info("skill manager initialized", "skills", len(skillMgr.All()))
	}

	// Multi-agent system
	if cfg.Agents.Enabled {
		agentMgr := agent.NewAgentManager(provider, sessions, db, memStore, tools, cfg.Agent, cfg.LLM)
		agentMgr.LoadDir(userdir.AgentsDir())
		for _, dir := range cfg.Agents.ExtraDirs {
			if err := agentMgr.LoadDir(dir); err != nil {
				slog.Warn("gateway: failed to load agents from extra dir", "dir", dir, "err", err)
			}
		}
		for _, def := range cfg.Agents.Definitions {
			if err := agentMgr.Add(defToSpec(def)); err != nil {
				slog.Warn("gateway: failed to add inline agent definition", "name", def.Name, "err", err)
			}
		}
		agentMgr.RegisterAll(tools)
		runtime.SetAgentManager(agentMgr)
		if cognitiveAgent != nil {
			cognitiveAgent.SetAgentManager(agentMgr)
		}
		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Compression pipeline
	if cfg.Agent.Compression.Strategy == "layered" {
		pipeline := agent.NewCompressionPipeline(
			provider, cfg.LLM.Model, cfg.Agent.Compression, resultStore, 200000,
		)
		runtime.SetCompressionPipeline(pipeline)
		slog.Info("layered compression pipeline enabled")
	}

	// Scheduler
	sched := scheduler.New(db, cfg.Scheduler.PollInterval)

	gw := &Gateway{
		cfg:            cfg,
		db:             db,
		sessions:       sessions,
		runtime:        runtime,
		cognitiveAgent: cognitiveAgent,
		tools:          tools,
		channels:       make(map[string]channel.Channel),
		sched:          sched,
		mcpManager:     mcp.NewManager(),
		rlTrainer:      rlTrainer,
		resultStore:    resultStore,
	}

	// Set up approval function
	runtime.SetApprovalFunc(gw.handleApproval)
	if cognitiveAgent != nil {
		cognitiveAgent.SetApprovalFunc(gw.handleApproval)
	}

	// Set up scheduler handler — routes scheduled tasks through the normal message pipeline
	sched.SetHandler(func(ctx context.Context, task scheduler.Task) {
		gw.handleInbound(ctx, channel.InboundMessage{
			Channel:   task.Channel,
			ChannelID: task.ChannelID,
			UserID:    "scheduler",
			UserName:  "scheduler",
			Text:      task.Prompt,
		})
	})

	return gw, nil
}

// AddChannel registers a channel adapter. Call before Start().
func (gw *Gateway) AddChannel(ch channel.Channel) {
	gw.channels[ch.Name()] = ch
}

// Start initializes all channels and begins processing.
func (gw *Gateway) Start(ctx context.Context) error {
	// Start MCP servers (non-fatal — partial failures are logged)
	if len(gw.cfg.Tools.MCP.Servers) > 0 {
		if err := gw.mcpManager.StartServers(ctx, gw.cfg.Tools.MCP.Servers, gw.tools); err != nil {
			slog.Error("some MCP servers failed to start", "err", err)
		}
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
	for name, ch := range gw.channels {
		if err := ch.Start(ctx, gw.handleInbound); err != nil {
			return err
		}
		slog.Info("channel started", "name", name)
	}

	// Start scheduler
	if gw.cfg.Scheduler.Enabled {
		gw.sched.Start(ctx)
		slog.Info("scheduler started")
	}

	// Start HTTP admin server if enabled
	if gw.cfg.Server.Enabled {
		go startHTTPServer(gw.cfg.Server.Addr, gw.db)
	}

	// Start RL trainer
	if gw.rlTrainer != nil {
		gw.rlTrainer.Start(ctx)
		slog.Info("RL trainer started")
	}

	slog.Info("gateway started")
	return nil
}

// Stop gracefully shuts down all components.
func (gw *Gateway) Stop(ctx context.Context) error {
	for name, ch := range gw.channels {
		if err := ch.Stop(ctx); err != nil {
			slog.Error("failed to stop channel", "name", name, "err", err)
		}
	}

	if gw.cfg.Scheduler.Enabled {
		gw.sched.Stop()
	}

	gw.mcpManager.Close()

	if gw.rlTrainer != nil {
		gw.rlTrainer.Stop()
	}

	gw.db.Close()
	slog.Info("gateway stopped")
	return nil
}

// handleInbound routes incoming messages to the agent runtime.
func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
	if msg.Text == "" {
		return
	}

	ch, ok := gw.channels[msg.Channel]
	if !ok {
		slog.Error("unknown channel", "channel", msg.Channel)
		return
	}

	// Handle /new and /start commands — reset session to start fresh conversation
	if msg.Text == "/new" || msg.Text == "/start" {
		if err := gw.sessions.Reset(ctx, msg.Channel, msg.ChannelID); err != nil {
			slog.Error("session reset failed", "err", err)
			ch.Send(ctx, channel.OutboundMessage{
				Channel:   msg.Channel,
				ChannelID: msg.ChannelID,
				Text:      "⚠️ Failed to reset session: " + err.Error(),
			})
			return
		}
		ch.Send(ctx, channel.OutboundMessage{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			Text:      "🔄 New conversation started.",
		})
		return
	}

	slog.Info("message received", "channel", msg.Channel, "user", msg.UserName, "text_len", len(msg.Text))

	if gw.cognitiveAgent != nil {
		if err := gw.cognitiveAgent.HandleMessage(ctx, ch, msg); err != nil {
			slog.Error("cognitive agent error", "err", err)
			ch.Send(ctx, channel.OutboundMessage{
				Channel:   msg.Channel,
				ChannelID: msg.ChannelID,
				Text:      "⚠️ Error: " + err.Error(),
			})
		}
		return
	}

	if err := gw.runtime.HandleMessage(ctx, ch, msg); err != nil {
		slog.Error("agent error", "err", err)
		ch.Send(ctx, channel.OutboundMessage{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			Text:      "⚠️ Error: " + err.Error(),
		})
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
	const pollInterval = 30 * time.Second
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
			for name, srv := range gw.cfg.Tools.MCP.Servers {
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

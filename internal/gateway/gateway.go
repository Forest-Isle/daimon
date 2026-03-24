package gateway

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/agent"
	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/channel/telegram"
	"github.com/punkopunko/ironclaw/internal/config"
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
	mu             sync.Mutex

	// Approval tracking for Telegram inline keyboard
	pendingApprovals sync.Map // key: "toolName" → chan bool
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

	// LLM provider
	provider := agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)

	// Agent runtime
	runtime := agent.NewRuntime(provider, tools, sessions, db, cfg.Agent, cfg.LLM)

	// Memory store
	var memStore memory.Store
	var factExtractor *memory.LLMFactExtractor
	var lifecycleMgr *memory.LifecycleManager

	if cfg.Memory.Enabled {
		var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
		if cfg.Memory.OpenAIAPIKey != "" {
			baseEmbedder := memory.NewOpenAIEmbedding(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel)
			// Wrap with cache if enabled
			if cfg.Memory.EnableSearchCache {
				embedder = memory.NewCachedEmbedder(
					baseEmbedder,
					cfg.Memory.EmbeddingModel,
					1000,           // Cache 1000 embeddings
					10*time.Minute, // 10 minute TTL
				)
				slog.Info("memory: embedding cache enabled")
			} else {
				embedder = baseEmbedder
			}
		}
		memCfg := memory.MemoryConfig{
			FactExtraction:        cfg.Memory.FactExtraction,
			SimilarityThreshold:   cfg.Memory.SimilarityThreshold,
			ConsolidationInterval: cfg.Memory.ConsolidationInterval,
			BM25Weight:            cfg.Memory.BM25Weight,
			VectorWeight:          cfg.Memory.VectorWeight,
			EnableVSS:             cfg.Memory.EnableVSS,
			VectorDimension:       cfg.Memory.VectorDimension,
			EnableSearchCache:     cfg.Memory.EnableSearchCache,
			SearchCacheSize:       cfg.Memory.SearchCacheSize,
			SearchCacheTTL:        cfg.Memory.SearchCacheTTL,
		}

		// Determine storage type (default: file)
		storageType := cfg.Memory.StorageType
		if storageType == "" {
			storageType = "file"
		}

		if storageType == "file" {
			// File-based storage
			storageDir := cfg.Memory.StorageDir
			if storageDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, err
				}
				storageDir = filepath.Join(home, ".IronClaw", "memory")
			}

			// Create embeddings DB
			embeddingsDB := memory.NewEmbeddingsDB(db, memCfg)

			// Create file store
			fileStore := memory.NewFileStore(storageDir, embeddingsDB, embedder, memCfg)
			memStore = fileStore

			// Start background processor for transaction log
			go fileStore.StartBackgroundProcessor(context.Background())

			slog.Info("memory: file-based storage enabled", "dir", storageDir)
		} else {
			// SQLite storage (legacy)
			sqliteStore := memory.NewSQLiteStore(db, embedder, memCfg)
			memStore = sqliteStore
			slog.Info("memory: SQLite storage enabled")
		}

		runtime.SetMemoryStore(memStore)

		if cfg.Memory.FactExtraction {
			completer := &completerAdapter{provider: provider, model: cfg.LLM.Model}
			factExtractor = memory.NewLLMFactExtractor(completer, memCfg)
			lifecycleMgr = memory.NewLifecycleManager(memStore, embedder, completer, memCfg)
		}
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

	// Initialize Telegram channel
	tg, err := telegram.New(gw.cfg.Telegram.Token, gw.cfg.Telegram.AllowedUserIDs)
	if err != nil {
		return err
	}
	gw.channels["telegram"] = tg

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
	// Handle approval callbacks
	if msg.CallbackData != "" {
		gw.handleCallback(msg)
		return
	}

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

// handleApproval sends an approval request via Telegram and waits for response.
func (gw *Gateway) handleApproval(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error) {
	tgAdapter, ok := ch.(*telegram.Adapter)
	if !ok {
		// Non-Telegram channels auto-approve for now
		return true, nil
	}

	chatID := parseChatID(target.ChannelID)
	if chatID == 0 {
		return false, nil
	}

	_, err := tgAdapter.SendApprovalRequest(chatID, toolName, input)
	if err != nil {
		return false, err
	}

	// Wait for callback
	resultCh := make(chan bool, 1)
	gw.pendingApprovals.Store(toolName, resultCh)
	defer gw.pendingApprovals.Delete(toolName)

	select {
	case approved := <-resultCh:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// handleCallback processes inline keyboard callbacks.
func (gw *Gateway) handleCallback(msg channel.InboundMessage) {
	parts := strings.SplitN(msg.CallbackData, ":", 2)
	if len(parts) != 2 {
		return
	}

	action, key := parts[0], parts[1]

	// Handle reflection replan decisions
	if action == "reflect_continue" || action == "reflect_adjust" || action == "reflect_abort" {
		if gw.cognitiveAgent != nil {
			var decision agent.ReplanDecision
			switch action {
			case "reflect_continue":
				decision = agent.ReplanContinue
			case "reflect_adjust":
				decision = agent.ReplanAdjust
			case "reflect_abort":
				decision = agent.ReplanAbort
			}
			gw.cognitiveAgent.ResolveReplanDecision(key, decision)
		}
		return
	}

	// Handle tool approval
	approved := action == "approve"
	if v, ok := gw.pendingApprovals.Load(key); ok {
		ch := v.(chan bool)
		ch <- approved
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

func parseChatID(s string) int64 {
	var id int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			id = id*10 + int64(c-'0')
		} else if c == '-' && id == 0 {
			// negative chat IDs for groups
			continue
		}
	}
	return id
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

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/mcp"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/rl"
	"github.com/Forest-Isle/IronClaw/internal/scheduler"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

// Gateway is the central coordinator that wires all modules together.
type Gateway struct {
	cfg            *config.Config
	db             *store.DB
	sessions       *session.Manager
	provider       agent.Provider        // stored for completerAdapter use
	runtime        *agent.Runtime
	cognitiveAgent *agent.CognitiveAgent
	tools          *tool.Registry
	hookMgr        *hook.Manager
	permEngine     *tool.PermissionEngine
	memStore       memory.Store
	factExtractor  *memory.LLMFactExtractor
	lifecycleMgr   *memory.LifecycleManager
	skillMgr       *skill.Manager
	channels       map[string]channel.Channel
	sched          *scheduler.Scheduler
	mcpManager     *mcp.Manager
	rlTrainer      *rl.Trainer
	resultStore    *tool.ResultStore
	consolidator   *memory.Consolidator
	compactor      *memory.Compactor
	graphDecay     *graph.GraphDecayTask
	evoEngine      *evolution.Engine
	stopCh         chan struct{} // closed in Stop() to signal background goroutines
	stopOnce       sync.Once    // ensures stopCh is closed exactly once
}

func New(cfg *config.Config) (*Gateway, error) {
	gw := &Gateway{
		cfg:      cfg,
		channels: make(map[string]channel.Channel),
		stopCh:   make(chan struct{}),
	}

	if err := gw.initDatabase(); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := gw.initToolsAndHooks(); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
	if err := gw.initAgentRuntime(); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if err := gw.initMemorySystem(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	if err := gw.initCognitiveAgent(); err != nil {
		return nil, fmt.Errorf("cognitive: %w", err)
	}
	if err := gw.initKnowledgeSystem(); err != nil {
		return nil, fmt.Errorf("knowledge: %w", err)
	}
	if err := gw.initSkillManager(); err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	if err := gw.initMultiAgent(); err != nil {
		return nil, fmt.Errorf("multi-agent: %w", err)
	}

	// Initialize evolution engine (self-improvement loops)
	gw.evoEngine = evolution.NewEngine(cfg.Evolution)

	// Scheduler
	gw.sched = scheduler.New(gw.db, cfg.Scheduler.PollInterval)
	gw.mcpManager = mcp.NewManager()

	// Approval wiring
	gw.runtime.SetApprovalFunc(gw.handleApproval)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetApprovalFunc(gw.handleApproval)
	}

	// Scheduler handler
	gw.sched.SetHandler(func(ctx context.Context, task scheduler.Task) {
		gw.handleInbound(ctx, channel.InboundMessage{
			Channel: task.Channel, ChannelID: task.ChannelID,
			UserID: "scheduler", UserName: "scheduler", Text: task.Prompt,
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

	// Start evolution engine
	if gw.evoEngine != nil {
		gw.evoEngine.Start()
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

	_ = gw.mcpManager.Close()

	if gw.rlTrainer != nil {
		gw.rlTrainer.Stop()
	}

	// Stop evolution engine
	if gw.evoEngine != nil {
		gw.evoEngine.Stop()
	}

	// Stop memory background tasks
	gw.stopOnce.Do(func() { close(gw.stopCh) })
	if gw.consolidator != nil {
		gw.consolidator.Stop()
	}
	if gw.compactor != nil {
		gw.compactor.Stop()
	}
	if gw.graphDecay != nil {
		gw.graphDecay.Stop()
	}

	_ = gw.db.Close()
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
			_ = ch.Send(ctx, channel.OutboundMessage{
				Channel:   msg.Channel,
				ChannelID: msg.ChannelID,
				Text:      "⚠️ Failed to reset session: " + err.Error(),
			})
			return
		}
		_ = ch.Send(ctx, channel.OutboundMessage{
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
			_ = ch.Send(ctx, channel.OutboundMessage{
				Channel:   msg.Channel,
				ChannelID: msg.ChannelID,
				Text:      "⚠️ Error: " + err.Error(),
			})
		}
		return
	}

	if err := gw.runtime.HandleMessage(ctx, ch, msg); err != nil {
		slog.Error("agent error", "err", err)
		_ = ch.Send(ctx, channel.OutboundMessage{
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

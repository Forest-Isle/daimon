package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/punkopunko/ironclaw/internal/agent"
	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/channel/telegram"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/scheduler"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// Gateway is the central coordinator that wires all modules together.
type Gateway struct {
	cfg      *config.Config
	db       *store.DB
	sessions *session.Manager
	runtime  *agent.Runtime
	tools    *tool.Registry
	channels map[string]channel.Channel
	sched    *scheduler.Scheduler
	mu       sync.Mutex

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
	provider := agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model)

	// Agent runtime
	runtime := agent.NewRuntime(provider, tools, sessions, db, cfg.Agent, cfg.LLM)

	// Scheduler
	sched := scheduler.New(db)

	gw := &Gateway{
		cfg:      cfg,
		db:       db,
		sessions: sessions,
		runtime:  runtime,
		tools:    tools,
		channels: make(map[string]channel.Channel),
		sched:    sched,
	}

	// Set up approval function
	runtime.SetApprovalFunc(gw.handleApproval)

	return gw, nil
}

// Start initializes all channels and begins processing.
func (gw *Gateway) Start(ctx context.Context) error {
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

	slog.Info("message received", "channel", msg.Channel, "user", msg.UserName, "text_len", len(msg.Text))

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

	action, toolName := parts[0], parts[1]
	approved := action == "approve"

	if v, ok := gw.pendingApprovals.Load(toolName); ok {
		ch := v.(chan bool)
		ch <- approved
	}
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

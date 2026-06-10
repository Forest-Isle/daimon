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
	"github.com/Forest-Isle/IronClaw/internal/feature"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

type Gateway struct {
	db        *store.DB
	stopCh    chan struct{}
	stopOnce  sync.Once
	initCtx   context.Context
	initCancel context.CancelFunc

	agent       *agent.Agent
	sessions    *session.Manager
	features    *feature.Registry
	contextMgr  agent.ContextManager

	config     *ConfigSubsystem
	database   *DatabaseSubsystem
	toolSub    *ToolSubsystem
	memory     *MemorySubsystem
	skills     *SkillSubsystem
	channels   *ChannelSubsystem
	multiAgent *MultiAgentSubsystem
	mcpSub     *MCPSubsystem
	health     *HealthSubsystem
	commands   *CommandSubsystem
	scheduler  *SchedulerSubsystem

	subsystems Subsystems
}

type GatewayOptions struct {
	ConfigPath string
}

func New(cfg *config.Config, opts ...GatewayOptions) (*Gateway, error) {
	opt := GatewayOptions{}
	if len(opts) > 0 { opt = opts[0] }

	gw := &Gateway{stopCh: make(chan struct{})}
	gw.initCtx, gw.initCancel = context.WithTimeout(context.Background(), 30*time.Second)

	gw.config = InitConfig(cfg, opt.ConfigPath)
	featSub := InitFeatures(cfg)
	gw.features = featSub.Registry
	dbSub, err := InitDatabase(cfg.Store.Path)
	if err != nil { return nil, fmt.Errorf("database: %w", err) }
	gw.database = dbSub
	gw.db = dbSub.DB
	gw.sessions = dbSub.Sessions
	gw.channels = &ChannelSubsystem{channels: make(map[string]channel.Channel)}

	gw.toolSub = InitTools(gw.initCtx, cfg, featSub, gw.sessions, gw.channels, gw.db)

	builder := agent.NewDepsBuilder()
	builder.Core.Tools = gw.toolSub.Registry
	builder.Core.Sessions = gw.sessions
	builder.Core.DB = gw.db
	builder.Security = agent.SecurityDeps{
		Interceptor: gw.toolSub.InterceptorChain,
		HookMgr:     gw.toolSub.HookMgr,
		PermEngine:  gw.toolSub.PermEngine,
	}

	agentSub := InitAgentRuntime(builder, cfg)

	gw.memory = InitMemorySystem(featSub, cfg, builder, agentSub.Provider, gw.db, gw.toolSub.Registry)
	gw.memory.BuildCortex()
	if gw.memory.Store() != nil {
		gw.toolSub.Registry.Register(tool.NewMemoryTool(gw.memory.Store(), gw.memory.LifecycleManager()))
	}

	gw.skills = InitSkills(featSub, cfg, gw.toolSub.Registry, builder)

	gw.multiAgent = InitMultiAgent(featSub, cfg, builder, agentSub.Provider,
		gw.sessions, gw.db, gw.memory.Store(), gw.toolSub.Registry, gw.toolSub.ResultStore)
	gw.contextMgr = gw.multiAgent.ContextMgr

	gw.mcpSub = InitMCP()

	deps := builder.Build()
	gw.agent = agent.NewAgent(&deps, &agent.LinearLoop{}, agent.NewEventBus())
	gw.agent.SetApprovalFunc(gw.handleApproval)

	gw.health = InitHealth(cfg, gw.db)
	gw.commands = InitCommands(gw)

	// SchedulerChannel notifier is wired post-construction in main.go
	// after the Telegram channel is created.
	gw.scheduler = InitScheduler(gw.db, nil)
	gw.AddChannel(gw.scheduler.Channel)

	gw.config.OnReload(func(newCfg *config.Config) {
		if gw.agent != nil {
			gw.agent.SetModel(newCfg.LLM.Model)
			gw.agent.EventBus().Publish(agent.ConfigChanged{Path: opt.ConfigPath})
		}
	})

	gw.subsystems = Subsystems{gw.memory, gw.channels, gw.mcpSub, gw.health, gw.config, gw.scheduler}
	return gw, nil
}

func (gw *Gateway) Config() *config.Config    { return gw.config.Config() }
func (gw *Gateway) Features() *feature.Registry { return gw.features }

func (gw *Gateway) AddChannel(ch channel.Channel) {
	gw.channels.channels[ch.Name()] = ch
}

// SetSchedulerNotifier wires the scheduler's reply forwarding to a real channel.
func (gw *Gateway) SetSchedulerNotifier(ch channel.Channel) {
	if gw.scheduler != nil {
		gw.scheduler.Channel.SetNotifier(ch)
	}
}

func (gw *Gateway) Start(ctx context.Context) error {
	gw.health.StartServer(gw.config.Config())

	if len(gw.config.Config().Tools.MCP.Servers) > 0 {
		go gw.mcpSub.StartServers(ctx, gw.config.Config(), gw.toolSub.Registry)
	}
	go gw.mcpSub.WatchDir(ctx, gw.config.Config())

	if gw.toolSub.ResultStore != nil {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done(): return
				case <-ticker.C:
					if err := gw.toolSub.ResultStore.Cleanup(); err != nil {
						slog.Warn("gateway: result store cleanup failed", "err", err)
					}
				}
			}
		}()
	}

	for name, ch := range gw.channels.Channels() {
		if err := ch.Start(ctx, gw.handleInbound); err != nil { return err }
		slog.Info("channel started", "name", name)
	}

	if gw.features.IsEnabled("server") {
		go startHTTPServer(gw.config.Config().Server.Addr, gw.db)
	}

	slog.Info("gateway started")
	return nil
}

func (gw *Gateway) Stop(ctx context.Context) error {
	gw.subsystems.StopAll(ctx)
	if gw.mcpSub.Manager != nil { _ = gw.mcpSub.Manager.Close() }
	gw.stopOnce.Do(func() { close(gw.stopCh) })
	if gw.initCancel != nil { gw.initCancel() }
	_ = gw.db.Close()
	slog.Info("gateway stopped")
	return nil
}

func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
	if msg.Text == "" { return }
	ch, ok := gw.channels.Channels()[msg.Channel]
	if !ok { slog.Error("unknown channel", "channel", msg.Channel); return }

	if sw, ok := ch.(channel.ToolStreamWriter); ok {
		target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
		ctx = tool.WithStreamCallback(ctx, func(chunk string) {
			if err := sw.WriteToolStream(ctx, target, "bash", chunk); err != nil {
				slog.Warn("gateway: tool stream write failed", "err", err)
			}
		})
	}

	if gw.commands != nil {
		if resp, handled := gw.commands.Dispatch(ctx, ch, msg); handled {
			if resp != "" {
				_ = ch.Send(ctx, channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID, Text: resp})
			}
			return
		}
	}

	slog.Info("message received", "channel", msg.Channel, "user", msg.UserName, "text_len", len(msg.Text))
	if err := gw.agent.HandleMessage(ctx, ch, msg); err != nil {
		slog.Error("agent error", "err", err)
		_ = ch.Send(ctx, channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID, Text: "Error: " + err.Error()})
	}
}

func (gw *Gateway) handleApproval(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName, input string) (bool, error) {
	if sender, ok := ch.(channel.ApprovalSender); ok {
		return sender.SendApprovalRequest(ctx, target, toolName, input)
	}
	return true, nil
}

type completerAdapter struct {
	provider agent.Provider
	model    string
}

func (a *completerAdapter) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	req := agent.CompletionRequest{
		Model: a.model, System: systemPrompt,
		Messages: []agent.CompletionMessage{{Role: "user", Content: userMessage}},
		MaxTokens: 512,
	}
	resp, err := a.provider.Complete(ctx, req)
	if err != nil { return "", err }
	return resp.Text, nil
}

func defaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil { return "" }
	return filepath.Join(home, ".ironclaw", "skills")
}

func defToSpec(def config.AgentDefinition) *agent.AgentSpec {
	return &agent.AgentSpec{
		Name: def.Name, Description: def.Description, SystemPrompt: def.SystemPrompt,
		Model: def.Model, MaxTokens: def.MaxTokens, MaxIterations: def.MaxIterations,
		Tools: def.Tools, Tags: def.Tags, Mode: def.Mode,
	}
}

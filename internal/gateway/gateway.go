package gateway

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/episode"
	"github.com/Forest-Isle/daimon/internal/feature"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/taskruntime"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/workflow"
)

type Gateway struct {
	db         *store.DB
	stopCh     chan struct{}
	stopOnce   sync.Once
	initCtx    context.Context
	initCancel context.CancelFunc

	agent      *agent.Agent
	sessions   *session.Manager
	features   *feature.Registry
	contextMgr agent.ContextManager

	EpisodeRunner  *episode.Runner
	EpisodeEnabled bool

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
	taskLedger *taskruntime.Ledger
	telemetry  *TelemetrySubsystem

	subsystems Subsystems
}

type GatewayOptions struct {
	ConfigPath string
}

func New(cfg *config.Config, opts ...GatewayOptions) (*Gateway, error) {
	opt := GatewayOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

	gw := &Gateway{stopCh: make(chan struct{})}
	gw.initCtx, gw.initCancel = context.WithTimeout(context.Background(), 30*time.Second)
	eventBus := agent.NewEventBus()

	gw.config = InitConfig(cfg, opt.ConfigPath)
	gw.telemetry = InitTelemetry(cfg, eventBus)
	featSub := InitFeatures(cfg)
	gw.features = featSub.Registry
	dbSub, err := InitDatabase(cfg.Store.Path)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	gw.database = dbSub
	gw.db = dbSub.DB
	gw.sessions = dbSub.Sessions
	gw.taskLedger = taskruntime.NewLedger(gw.db.DB)
	gw.channels = &ChannelSubsystem{channels: make(map[string]channel.Channel)}

	gw.toolSub = InitTools(gw.initCtx, cfg, featSub, gw.sessions, gw.channels, gw.db, eventBus)

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
	gw.EpisodeRunner = episode.NewRunner(agentSub.Provider, gw.toolSub.Registry, gw.toolSub.WorldStore, &gw.toolSub.WorldIdentity)
	gw.EpisodeEnabled = cfg.Agent.EpisodeEnabled

	gw.memory = InitMemorySystem(featSub, cfg, builder, agentSub.Provider, gw.db, gw.toolSub.Registry)
	gw.memory.BuildCortex()
	builder.Memory.Cortex = gw.memory.Cortex()
	if gw.memory.Store() != nil {
		gw.toolSub.Registry.Register(tool.NewMemoryTool(gw.memory.Store(), gw.memory.LifecycleManager()))
	}

	gw.skills = InitSkills(featSub, cfg, gw.toolSub.Registry, builder)

	gw.multiAgent = InitMultiAgent(featSub, cfg, builder, agentSub.Provider,
		gw.sessions, gw.db, gw.memory.Store(), gw.toolSub.Registry, gw.toolSub.ResultStore)
	gw.contextMgr = gw.multiAgent.ContextMgr
	if gw.multiAgent.AgentMgr != nil && gw.multiAgent.SubAgentMgr != nil {
		gw.toolSub.Registry.Register(agent.NewWorkflowTool(
			gw.multiAgent.AgentMgr,
			gw.multiAgent.SubAgentMgr,
			workflow.NewSQLiteCache(gw.db.DB),
			eventBus,
		))
	}

	gw.mcpSub = InitMCP()

	deps := builder.Build()
	gw.agent = agent.NewAgent(&deps, &agent.LinearLoop{}, eventBus)
	gw.agent.SetApprovalFunc(gw.handleApproval)

	gw.health = InitHealth(cfg, gw.db)
	gw.commands = InitCommands(gw)

	// SchedulerChannel notifier is wired post-construction in main.go
	// after the Telegram channel is created.
	gw.scheduler = InitScheduler(gw.db, nil, gw.taskLedger)
	gw.AddChannel(gw.scheduler.Channel)

	gw.config.OnReload(func(newCfg *config.Config) {
		if gw.agent != nil {
			gw.agent.SetModel(newCfg.LLM.Model)
			gw.agent.EventBus().Publish(agent.ConfigChanged{Path: opt.ConfigPath})
		}
		gw.EpisodeEnabled = newCfg.Agent.EpisodeEnabled
	})

	gw.subsystems = Subsystems{gw.memory, gw.channels, gw.mcpSub, gw.health, gw.config, gw.scheduler, gw.telemetry}
	return gw, nil
}

func (gw *Gateway) Config() *config.Config      { return gw.config.Config() }
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
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := gw.toolSub.ResultStore.Cleanup(); err != nil {
						slog.Warn("gateway: result store cleanup failed", "err", err)
					}
				}
			}
		}()
	}

	for name, ch := range gw.channels.Channels() {
		if err := ch.Start(ctx, gw.handleInbound); err != nil {
			return err
		}
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
	if gw.mcpSub.Manager != nil {
		_ = gw.mcpSub.Manager.Close()
	}
	gw.stopOnce.Do(func() { close(gw.stopCh) })
	if gw.initCancel != nil {
		gw.initCancel()
	}
	_ = gw.db.Close()
	slog.Info("gateway stopped")
	return nil
}

func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
	if msg.Text == "" {
		return
	}
	ch, ok := gw.channels.Channels()[msg.Channel]
	if !ok {
		slog.Error("unknown channel", "channel", msg.Channel)
		return
	}

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
	if msg.Channel == "scheduler" {
		gw.publishTaskTransition(msg.ChannelID, "scheduled", "", "running", "scheduler message handling started")
	}
	if gw.EpisodeEnabled && gw.EpisodeRunner != nil {
		outcome, runErr := gw.EpisodeRunner.Run(ctx, msgToEpisodeState(msg))
		if runErr == nil && outcome.Status != "failed" {
			if outcome.Summary != "" {
				if err := ch.Send(ctx, channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID, Text: outcome.Summary}); err != nil {
					slog.Warn("gateway: episode response send failed", "err", err)
				}
			}
			gw.finishInbound(ctx, msg, nil)
			return
		}
		if runErr != nil {
			slog.Warn("gateway: episode runner failed; falling back to agent", "err", runErr)
		} else {
			slog.Warn("gateway: episode outcome failed; falling back to agent", "summary", outcome.Summary)
		}
	}
	err := gw.agent.HandleMessage(ctx, ch, msg)
	if err != nil {
		slog.Error("agent error", "err", err)
		_ = ch.Send(ctx, channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID, Text: "Error: " + err.Error()})
	}
	gw.finishInbound(ctx, msg, err)
}

func (gw *Gateway) finishInbound(ctx context.Context, msg channel.InboundMessage, err error) {
	result := gw.saveTaskCheckpoint(ctx, msg)
	if msg.Channel == "scheduler" && gw.scheduler != nil && gw.scheduler.Channel != nil {
		gw.scheduler.Channel.FinishRun(ctx, msg.ChannelID, err, result)
		toState := "succeeded"
		if err != nil {
			toState = "failed"
		}
		gw.publishTaskTransition(msg.ChannelID, "scheduled", "running", toState, "scheduler message handling completed")
	}
}

func msgToEpisodeState(msg channel.InboundMessage) episode.State {
	now := time.Now().UTC()
	return episode.State{
		ID:        newULID(now),
		Goal:      "Respond to the user's message",
		Trigger:   "chat: " + msg.Text,
		CreatedAt: now,
		Budget:    episode.Budget{},
	}
}

const ulidEncoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newULID(t time.Time) string {
	var entropy [10]byte
	if _, err := crand.Read(entropy[:]); err != nil {
		seed := uint64(t.UnixNano())
		for i := range entropy {
			seed = seed*6364136223846793005 + 1442695040888963407
			entropy[i] = byte(seed >> 56)
		}
	}

	ms := uint64(t.UnixMilli())
	var id [26]byte
	id[0] = ulidEncoding[(ms>>45)&0x1f]
	id[1] = ulidEncoding[(ms>>40)&0x1f]
	id[2] = ulidEncoding[(ms>>35)&0x1f]
	id[3] = ulidEncoding[(ms>>30)&0x1f]
	id[4] = ulidEncoding[(ms>>25)&0x1f]
	id[5] = ulidEncoding[(ms>>20)&0x1f]
	id[6] = ulidEncoding[(ms>>15)&0x1f]
	id[7] = ulidEncoding[(ms>>10)&0x1f]
	id[8] = ulidEncoding[(ms>>5)&0x1f]
	id[9] = ulidEncoding[ms&0x1f]

	id[10] = ulidEncoding[(entropy[0]&0xf8)>>3]
	id[11] = ulidEncoding[((entropy[0]&0x07)<<2)|((entropy[1]&0xc0)>>6)]
	id[12] = ulidEncoding[(entropy[1]&0x3e)>>1]
	id[13] = ulidEncoding[((entropy[1]&0x01)<<4)|((entropy[2]&0xf0)>>4)]
	id[14] = ulidEncoding[((entropy[2]&0x0f)<<1)|((entropy[3]&0x80)>>7)]
	id[15] = ulidEncoding[(entropy[3]&0x7c)>>2]
	id[16] = ulidEncoding[((entropy[3]&0x03)<<3)|((entropy[4]&0xe0)>>5)]
	id[17] = ulidEncoding[entropy[4]&0x1f]
	id[18] = ulidEncoding[(entropy[5]&0xf8)>>3]
	id[19] = ulidEncoding[((entropy[5]&0x07)<<2)|((entropy[6]&0xc0)>>6)]
	id[20] = ulidEncoding[(entropy[6]&0x3e)>>1]
	id[21] = ulidEncoding[((entropy[6]&0x01)<<4)|((entropy[7]&0xf0)>>4)]
	id[22] = ulidEncoding[((entropy[7]&0x0f)<<1)|((entropy[8]&0x80)>>7)]
	id[23] = ulidEncoding[(entropy[8]&0x7c)>>2]
	id[24] = ulidEncoding[((entropy[8]&0x03)<<3)|((entropy[9]&0xe0)>>5)]
	id[25] = ulidEncoding[entropy[9]&0x1f]
	return string(id[:])
}

func (gw *Gateway) publishTaskTransition(taskID, kind, from, to, reason string) {
	if gw == nil || gw.agent == nil || taskID == "" {
		return
	}
	gw.agent.EventBus().Publish(agent.TaskTransitioned{
		TaskID:    taskID,
		Kind:      kind,
		FromState: from,
		ToState:   to,
		Reason:    reason,
	})
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
		Messages:  []agent.CompletionMessage{{Role: "user", Content: userMessage}},
		MaxTokens: 512,
	}
	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func defaultSkillsDir() string {
	return filepath.Join(appdir.BaseDir(), "skills")
}

func defToSpec(def config.AgentDefinition) *agent.AgentSpec {
	return &agent.AgentSpec{
		Name: def.Name, Description: def.Description, SystemPrompt: def.SystemPrompt,
		Model: def.Model, MaxTokens: def.MaxTokens, MaxIterations: def.MaxIterations,
		Tools: def.Tools, Tags: def.Tags, Mode: def.Mode,
	}
}

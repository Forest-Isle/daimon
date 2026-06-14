package gateway

import (
	"context"
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
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/sleep"
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
	heart      *HeartSubsystem // nil unless agent.heart_enabled
	sleep      *sleep.Runner   // consolidation jobs, triggered by /sleep (and later the heart)

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

	// The heart routes events into autonomous episodes, which need the cognitive
	// kernel. With episodes off, every routed event would fail with "cognitive
	// kernel unavailable" and any due follow-up would burn silently. Reject the
	// combination here, before any resources are allocated, rather than running a
	// loop that can only fail.
	if cfg.Agent.HeartEnabled && !cfg.Agent.EpisodeEnabled {
		return nil, fmt.Errorf("config: agent.heart_enabled requires agent.episode_enabled (the heart drives episodes through the kernel)")
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
	gw.EpisodeRunner = episode.NewRunner(agentSub.Provider, gw.toolSub.WorldStore, &gw.toolSub.WorldIdentity, eventBus)
	gw.EpisodeRunner.SetValues(gw.toolSub.ValuesStore)
	gw.EpisodeEnabled = cfg.Agent.EpisodeEnabled

	// Sleep: consolidation jobs over the world model. The digest job regenerates
	// the agent's self-summary; the drift job flags active values that recent
	// activity contradicts. Both use the LLM provider. Triggered on demand via
	// /sleep today; the heart can schedule it later.
	sleepSummarizer := &completerAdapter{provider: agentSub.Provider, model: cfg.LLM.Model, maxTokens: 1024}
	gw.sleep = sleep.NewRunner(
		sleep.NewDigestJob(gw.toolSub.WorldStore, sleepSummarizer),
		sleep.NewDriftJob(gw.toolSub.ValuesStore, gw.toolSub.WorldStore, sleepSummarizer),
		sleep.NewRollupJob(gw.toolSub.WorldStore, sleepSummarizer),
	)

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
	gw.agent.SetKernel(gw.EpisodeRunner, gw.EpisodeEnabled)

	gw.health = InitHealth(cfg, gw.db)
	gw.commands = InitCommands(gw)

	// SchedulerChannel notifier is wired post-construction in main.go
	// after the Telegram channel is created.
	gw.scheduler = InitScheduler(gw.db, nil, gw.taskLedger)
	gw.AddChannel(gw.scheduler.Channel)

	// Heart: the autonomous event path. Built only when enabled; when off, the
	// binary behaves exactly as before (chat path untouched). The dispatch
	// handler needs gw.agent and gw.channels, so it is wired here after both exist.
	if cfg.Agent.HeartEnabled {
		// Invariant (heart ⇒ episode) is enforced at the top of New().
		gw.heart = InitHeart(cfg, gw.db, agentSub.Provider, gw.toolSub.WorldStore)
		gw.heart.heart = heart.New(gw.heart.store, gw.newEventDispatcher().handle)

		// Timer follow-ups planted by episodes re-enter through the heart: the
		// runner gets a planter backed by the follow-up store, and a source polls
		// the queue to fire due entries as internal.followup events.
		followUps := heart.NewFollowUpStore(gw.db.DB)
		gw.EpisodeRunner.SetPlanter(followUpPlanter{store: followUps, now: time.Now})
		gw.heart.heart.Register(&heart.FollowUpSource{Store: followUps})

		if mins := cfg.Agent.Heart.HeartbeatIntervalMinutes; mins > 0 {
			gw.heart.heart.Register(&heart.TimerSource{
				SourceName: "timer",
				Kind:       "internal.heartbeat",
				Interval:   time.Duration(mins) * time.Minute,
			})
		}

		// Sleep can now learn from routing corrections: the synthesize-rules job
		// needs the heart's feedback + event stores, which only exist when the heart
		// is enabled. Registered here (construction, before Start) so no cycle runs.
		gw.sleep.Register(sleep.NewSynthesizeRulesJob(
			feedbackCorrectionSource{feedback: gw.heart.feedback, events: gw.heart.store},
			rulesFileSink{path: attentionRulesPath()},
		))
		slog.Info("heart enabled",
			"heartbeat_interval_minutes", cfg.Agent.Heart.HeartbeatIntervalMinutes,
			"model_router", cfg.Agent.Heart.ModelRouter)
	}

	gw.config.OnReload(func(newCfg *config.Config) {
		// A running heart requires the kernel; never disable episodes out from
		// under it on reload, or routed events would start failing mid-session.
		episodeEnabled := newCfg.Agent.EpisodeEnabled
		if gw.heart != nil && !episodeEnabled {
			slog.Warn("config reload: ignoring episode_enabled=false while heart is running (heart requires the kernel)")
			episodeEnabled = true
		}
		if gw.agent != nil {
			gw.agent.SetModel(newCfg.LLM.Model)
			gw.agent.SetKernel(gw.EpisodeRunner, episodeEnabled)
			gw.agent.EventBus().Publish(agent.ConfigChanged{Path: opt.ConfigPath})
		}
		gw.EpisodeEnabled = episodeEnabled
	})

	gw.subsystems = Subsystems{gw.memory, gw.channels, gw.mcpSub, gw.health, gw.config, gw.scheduler, gw.telemetry}
	if gw.heart != nil {
		gw.subsystems = append(gw.subsystems, gw.heart)
	}
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

	// Heart runs after channels so its wake_user path can reach them. Its run
	// loop (backlog recovery + sources) lives on this serve ctx and exits at
	// shutdown when ctx is cancelled.
	if gw.heart != nil {
		if err := gw.heart.Start(ctx); err != nil {
			return err
		}
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

	// Chat ingress through the heart (when enabled): record the message in the
	// unified event stream for audit + idempotent dedup. A redelivered message
	// (same channel-native id) is skipped so the turn is not handled twice. A
	// recording error is best-effort — never drop the user's turn over an audit
	// failure, so fall through to normal handling. Disabled ⇒ inserted=true.
	if inserted, recErr := gw.heart.RecordChatEvent(ctx, msg); recErr != nil {
		slog.Warn("gateway: record chat event failed; handling anyway", "channel", msg.Channel, "err", recErr)
	} else if !inserted {
		slog.Info("gateway: skipping redelivered chat message", "channel", msg.Channel, "message_id", msg.MessageID)
		return
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
	if ch == nil {
		// No interactive channel (autonomous internal episode): we cannot get a
		// human sign-off, so deny tools that require approval. Only auto-approved
		// / read-only tools run autonomously. Routing autonomous approvals to
		// Telegram is a later increment.
		return false, nil
	}
	if sender, ok := ch.(channel.ApprovalSender); ok {
		return sender.SendApprovalRequest(ctx, target, toolName, input)
	}
	return true, nil
}

type completerAdapter struct {
	provider agent.Provider
	model    string
	// maxTokens caps the response; 0 falls back to completerDefaultMaxTokens so
	// existing callers keep their prior behavior.
	maxTokens int
}

const completerDefaultMaxTokens = 512

func (a *completerAdapter) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	maxTokens := a.maxTokens
	if maxTokens <= 0 {
		maxTokens = completerDefaultMaxTokens
	}
	req := agent.CompletionRequest{
		Model: a.model, System: systemPrompt,
		Messages:  []agent.CompletionMessage{{Role: "user", Content: userMessage}},
		MaxTokens: maxTokens,
	}
	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.StopReason == agent.StopMaxToken {
		slog.Warn("completerAdapter: response truncated at max_tokens",
			"model", a.model, "max_tokens", maxTokens)
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

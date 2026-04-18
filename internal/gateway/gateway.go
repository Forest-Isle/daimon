package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/eval"
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
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
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
	taskLedger      *taskledger.SQLiteTaskLedger
	teamCoordinator *taskledger.TeamCoordinator
	staleDetector   *taskledger.StaleDetector
	stopCh          chan struct{} // closed in Stop() to signal background goroutines
	stopOnce        sync.Once    // ensures stopCh is closed exactly once
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
	// Evolution engine must exist before cognitive agent registers hooks.
	gw.evoEngine = evolution.NewEngine(cfg.Evolution)
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

	// Task ledger
	gw.taskLedger = taskledger.NewSQLiteTaskLedger(gw.db)
	gw.runtime.SetTaskLedger(gw.taskLedger)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetTaskLedger(gw.taskLedger)
	}

	// Team coordinator
	if cfg.Agent.Team.Enabled {
		maxWorkers := cfg.Agent.Team.MaxWorkers
		if maxWorkers <= 0 {
			maxWorkers = 3
		}
		tc := taskledger.NewTeamCoordinator(gw.taskLedger, maxWorkers)
		tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
			return gw.executeTeamTask(ctx, task)
		})
		gw.teamCoordinator = tc
	}

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
	// Start MCP servers asynchronously — npx/uvx process startup can take
	// several seconds and should not block the TUI from appearing.
	if len(gw.cfg.Tools.MCP.Servers) > 0 {
		go func() {
			if err := gw.mcpManager.StartServers(ctx, gw.cfg.Tools.MCP.Servers, gw.tools); err != nil {
				slog.Error("some MCP servers failed to start", "err", err)
			}
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

	// Start stale task detector
	if gw.taskLedger != nil {
		gw.staleDetector = taskledger.NewStaleDetector(
			gw.taskLedger, 2*time.Minute, 30*time.Second,
			func(t taskledger.Task) {
				slog.Info("stale-detector: task marked stale", "id", t.ID, "title", t.Title)
			},
		)
		gw.staleDetector.Start()
		slog.Info("stale task detector started")
	}

	// Start RL trainer
	if gw.rlTrainer != nil {
		gw.rlTrainer.Start(ctx)
		slog.Info("RL trainer started")
	}

	// Start evolution engine
	if gw.evoEngine != nil {
		gw.evoEngine.Start()

		// If both RL and evolution are enabled, import historical trajectories
		// into the RL replay buffer for warm-starting.
		if gw.rlTrainer != nil && gw.cfg.Evolution.Enabled {
			go gw.importTrajectoriesToRL()
		}
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

	// Persist evolution state and stop engine
	if gw.evoEngine != nil {
		prefPath := ""
		if p, err := gw.resolveEvolutionPreferencePath(gw.cfg.Evolution.PreferenceFile); err == nil {
			prefPath = p
		}
		gw.evoEngine.SaveState(prefPath)
		gw.evoEngine.Stop()
	}

	if gw.staleDetector != nil {
		gw.staleDetector.Stop()
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

// NewEvalRunner creates an eval.AgentRunner backed by the gateway's cognitive
// agent. Returns nil if the gateway is not in cognitive mode.
func (gw *Gateway) NewEvalRunner() *eval.CognitiveAgentRunner {
	if gw.cognitiveAgent == nil {
		return nil
	}
	return eval.NewCognitiveAgentRunner(gw.cognitiveAgent)
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

	// Handle /tasks command — list active and recent tasks from task ledger
	if msg.Text == "/tasks" {
		gw.handleTasksCommand(ctx, ch, msg)
		return
	}

	// Handle /team <goal> command — break goal into parallel tasks
	if strings.HasPrefix(msg.Text, "/team ") {
		goal := strings.TrimPrefix(msg.Text, "/team ")
		result := gw.handleTeamCommand(ctx, strings.TrimSpace(goal))
		_ = ch.Send(ctx, channel.OutboundMessage{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			Text:      result,
		})
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

// handleTasksCommand lists running and pending tasks from the task ledger.
func (gw *Gateway) handleTasksCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	target := channel.OutboundMessage{Channel: msg.Channel, ChannelID: msg.ChannelID}

	if gw.taskLedger == nil {
		target.Text = "Task ledger not available."
		_ = ch.Send(ctx, target)
		return
	}

	running := taskledger.TaskStateRunning
	runningTasks, err := gw.taskLedger.List(ctx, taskledger.TaskFilter{State: &running})
	if err != nil {
		target.Text = "⚠️ Failed to list tasks: " + err.Error()
		_ = ch.Send(ctx, target)
		return
	}

	pending := taskledger.TaskStatePending
	pendingTasks, err := gw.taskLedger.List(ctx, taskledger.TaskFilter{State: &pending})
	if err != nil {
		target.Text = "⚠️ Failed to list tasks: " + err.Error()
		_ = ch.Send(ctx, target)
		return
	}

	var b strings.Builder
	b.WriteString("📋 Task Ledger\n\n")

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
	_ = ch.Send(ctx, target)
}

// handleTeamCommand breaks a goal into parallel tasks using the LLM and executes them.
func (gw *Gateway) handleTeamCommand(ctx context.Context, goal string) string {
	if gw.teamCoordinator == nil {
		return "Team mode is not enabled. Set agent.team.enabled: true in config."
	}

	prompt := fmt.Sprintf(taskledger.TeamPlanPrompt, goal)
	req := agent.CompletionRequest{
		Model:     gw.cfg.LLM.Model,
		System:    "You are a task planning assistant. Output only valid JSON.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: gw.cfg.LLM.MaxTokens,
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
		Title: truncateGoal(goal, 100),
	}
	if err := gw.taskLedger.Register(ctx, rootTask); err != nil {
		return fmt.Sprintf("Failed to register root task: %v", err)
	}

	tasks, err := taskledger.ParseTaskPlan(resp.Text, rootID)
	if err != nil {
		return fmt.Sprintf("Failed to parse plan: %v", err)
	}

	for _, t := range tasks {
		if err := gw.teamCoordinator.AddTask(ctx, t); err != nil {
			return fmt.Sprintf("Failed to add task %s: %v", t.ID, err)
		}
	}

	result, err := gw.teamCoordinator.RunWithExecutor(ctx)
	if err != nil {
		return fmt.Sprintf("Team execution failed: %v", err)
	}

	now := time.Now().UTC()
	rootTask.State = taskledger.TaskStateCompleted
	rootTask.CompletedAt = &now
	rootTask.Result = result.Summary
	_ = gw.taskLedger.Update(ctx, rootTask)

	return fmt.Sprintf("Team completed: %d tasks done, %d failed", result.TasksCompleted, result.TasksFailed)
}

// executeTeamTask runs a single team task by creating a temporary session
// and routing through the main agent runtime.
func (gw *Gateway) executeTeamTask(ctx context.Context, task taskledger.Task) (string, error) {
	req := agent.CompletionRequest{
		Model:     gw.cfg.LLM.Model,
		System:    "You are an agent executing a specific task. Be concise and focused.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: task.Description}},
		MaxTokens: gw.cfg.LLM.MaxTokens,
	}
	resp, err := gw.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func truncateGoal(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// importTrajectoriesToRL warm-starts the RL replay buffer from historical
// trajectory data. Runs once in the background at startup.
func (gw *Gateway) importTrajectoriesToRL() {
	trajDir, err := gw.resolveEvolutionTrajDir()
	if err != nil {
		return
	}
	since := time.Now().AddDate(0, 0, -7)
	exps, err := evolution.ConvertFromDir(trajDir, since)
	if err != nil {
		slog.Warn("gateway: RL trajectory import failed", "err", err)
		return
	}
	for _, exp := range exps {
		gw.rlTrainer.AddExperience(rl.Experience{
			State: &rl.RLState{
				ComplexitySimple:   exp.ComplexitySimple,
				ComplexityModerate: exp.ComplexityModerate,
				ComplexityComplex:  exp.ComplexityComplex,
				ToolCount:          exp.ToolCount,
				SubTaskCount:       exp.SubTaskCount,
				ReplanCount:        exp.ReplanCount,
				SuccessCount:       exp.SuccessCount,
				FailureCount:       exp.FailureCount,
				Progress:           exp.Progress,
				PlanConfidence:     exp.PlanConfidence,
				ReflectionConfidence: exp.ReflectionConf,
			},
			Action: []float64{exp.Reward},
			Reward: exp.Reward,
			Done:   true,
			Level:  rl.LevelPPO,
		})
	}
	if len(exps) > 0 {
		slog.Info("gateway: imported trajectories into RL buffer", "experiences", len(exps))
	}
}

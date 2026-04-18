package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/rl"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

const MaxReplanAttempts = 2

// CognitiveAgent implements the structured PERCEIVE→PLAN→ACT→OBSERVE→REFLECT loop.
type CognitiveAgent struct {
	runtime            *Runtime
	perceiver          *Perceiver
	planner            *Planner
	executor           *Executor
	observer           *Observer
	reflector          *Reflector
	sessions           *session.Manager
	db                 *store.DB
	cfg                config.AgentConfig
	llmCfg             config.LLMConfig
	debateCfg          config.DebateSettings
	memStore           memory.Store
	skillMgr        *skill.Manager
	agentMgr        *AgentManager
	orchestrator    *AgentOrchestrator
	entityExtractor *graph.LLMEntityExtractor
	rlPolicy        RLPolicy  // RL policy interface (nil if disabled)
	rlTrainer       RLTrainer // RL trainer interface (nil if disabled)
	evoEngine       *evolution.Engine // self-evolution event dispatcher (nil if disabled)
	hookMgr         *hook.Manager
	permEngine      *tool.PermissionEngine
	checkpointStore CheckpointStore
}

// NewCognitiveAgent creates a CognitiveAgent, wiring all phases together.
func NewCognitiveAgent(
	provider Provider,
	tools *tool.Registry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *CognitiveAgent {
	ca := &CognitiveAgent{
		sessions: sessions,
		db:       db,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}

	cogCfg := cfg.Cognitive

	// Build runtime for simple-task delegation
	ca.runtime = NewRuntime(provider, tools, sessions, db, cfg, llmCfg)

	// Build phase components
	ca.perceiver = NewPerceiver(nil) // memStore injected via SetMemoryStore
	scanner := NewProjectContextScanner()
	ca.perceiver.SetProjectScanner(scanner)
	gitProvider := NewGitContextProvider()
	ca.perceiver.SetGitProvider(gitProvider)
	ca.planner = NewPlanner(provider, tools, cogCfg, llmCfg.Model)
	ca.executor = NewExecutor(tools, db, nil, cogCfg) // approvalFunc set via SetApprovalFunc
	ca.executor.SetToolCache(NewToolResultCache())
	ca.observer = NewObserver()
	ca.reflector = NewReflector(provider, nil, cogCfg, llmCfg.Model)

	return ca
}

// SetMemoryStore injects the memory.md store into all phases that need it.
func (ca *CognitiveAgent) SetMemoryStore(s memory.Store) {
	ca.memStore = s
	ca.runtime.SetMemoryStore(s)
	// Preserve existing searcher, graph, scanner, and git provider when rebuilding the perceiver.
	oldSearcher := ca.perceiver.searcher
	oldGraph := ca.perceiver.graph
	oldScanner := ca.perceiver.scanner
	oldGitProvider := ca.perceiver.gitProvider
	ca.perceiver = NewPerceiver(s)
	if oldSearcher != nil {
		ca.perceiver.SetKnowledgeSearcher(oldSearcher)
	}
	if oldGraph != nil {
		ca.perceiver.SetKnowledgeGraph(oldGraph)
	}
	if oldScanner != nil {
		ca.perceiver.SetProjectScanner(oldScanner)
	}
	if oldGitProvider != nil {
		ca.perceiver.SetGitProvider(oldGitProvider)
	}
	ca.reflector = NewReflector(
		ca.planner.provider,
		s,
		ca.cfg.Cognitive,
		ca.llmCfg.Model,
	)
}

// SetKnowledgeSearcher injects a knowledge searcher (KB or HybridRetriever) into the perceiver.
func (ca *CognitiveAgent) SetKnowledgeSearcher(s knowledge.Searcher) {
	ca.perceiver.SetKnowledgeSearcher(s)
}

// SetKnowledgeGraph injects a knowledge graph into the perceiver.
func (ca *CognitiveAgent) SetKnowledgeGraph(g graph.Graph) {
	ca.perceiver.SetKnowledgeGraph(g)
}

// SetEntityExtractor injects an entity extractor for graph population during reflection.
func (ca *CognitiveAgent) SetEntityExtractor(e *graph.LLMEntityExtractor) {
	ca.entityExtractor = e
	ca.reflector.SetEntityExtractor(e)
}

// SetFactExtractor injects a fact extractor into the reflector.
func (ca *CognitiveAgent) SetFactExtractor(fe *memory.LLMFactExtractor) {
	ca.reflector.SetFactExtractor(fe)
}

// SetLifecycleManager injects a lifecycle manager into the reflector.
func (ca *CognitiveAgent) SetLifecycleManager(lm *memory.LifecycleManager) {
	ca.reflector.SetLifecycleManager(lm)
}

// SetApprovalFunc injects the approval function into executor and runtime.
func (ca *CognitiveAgent) SetApprovalFunc(fn ApprovalFunc) {
	ca.executor.approvalFunc = fn
	ca.runtime.SetApprovalFunc(fn)
}

// SetHookManager injects a hook manager into the cognitive agent, its executor, and inner runtime.
func (ca *CognitiveAgent) SetHookManager(mgr *hook.Manager) {
	ca.hookMgr = mgr
	ca.executor.SetHookManager(mgr)
	ca.runtime.SetHookManager(mgr)
}

// SetPermissionEngine injects a permission engine into the cognitive agent, its executor, and inner runtime.
func (ca *CognitiveAgent) SetPermissionEngine(pe *tool.PermissionEngine) {
	ca.permEngine = pe
	ca.executor.SetPermissionEngine(pe)
	ca.runtime.SetPermissionEngine(pe)
}

// SetSkillManager injects a skill manager into the cognitive agent and its inner runtime.
func (ca *CognitiveAgent) SetSkillManager(m *skill.Manager) {
	ca.skillMgr = m
	ca.runtime.SetSkillManager(m)
}

// SetAgentManager injects an agent manager into the cognitive agent and its inner runtime.
func (ca *CognitiveAgent) SetAgentManager(m *AgentManager) {
	ca.agentMgr = m
	ca.runtime.SetAgentManager(m)
}

// SetDebateConfig sets the debate configuration from the agents config.
func (ca *CognitiveAgent) SetDebateConfig(cfg config.DebateSettings) {
	ca.debateCfg = cfg
}

// SetOrchestrator injects an agent orchestrator into the cognitive agent.
func (ca *CognitiveAgent) SetOrchestrator(o *AgentOrchestrator) {
	ca.orchestrator = o
}

// SetEvolutionEngine injects the self-evolution event dispatcher.
func (ca *CognitiveAgent) SetEvolutionEngine(e *evolution.Engine) {
	ca.evoEngine = e
}

// SetRLPolicy injects an RL policy into the cognitive agent.
func (ca *CognitiveAgent) SetRLPolicy(policy RLPolicy) {
	ca.rlPolicy = policy
	ca.perceiver.SetRLPolicy(policy)
	ca.planner.SetRLPolicy(policy)
	ca.executor.SetRLPolicy(policy)
	ca.reflector.SetRLPolicy(policy)
}

// SetRLTrainer injects an RL trainer into the cognitive agent.
func (ca *CognitiveAgent) SetRLTrainer(trainer RLTrainer) {
	ca.rlTrainer = trainer
}

// SetMemoryNotifyFunc injects a callback for sending memory operation summaries.
func (ca *CognitiveAgent) SetMemoryNotifyFunc(fn MemoryNotifyFunc) {
	ca.reflector.SetMemoryNotifyFunc(fn)
}

// SetCheckpointStore injects a checkpoint store for task resume support.
func (ca *CognitiveAgent) SetCheckpointStore(cs CheckpointStore) {
	ca.checkpointStore = cs
}

// HandleMessage processes an inbound message through the cognitive loop.
func (ca *CognitiveAgent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	sess, err := ca.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Add user message to session
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	// Compact history before perceive
	if err := CompactHistory(ctx, ca.planner.provider, sess, ca.llmCfg.Model); err != nil {
		slog.Warn("cognitive: history compaction failed", "session", sess.ID, "err", err)
	}

	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	// ── PERCEIVE ──────────────────────────────────────────────────────────────
	state, err := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	if err != nil {
		return fmt.Errorf("perceive: %w", err)
	}

	// Inject skills into cognitive state for use in PLAN phase
	if ca.skillMgr != nil {
		state.Skills = ca.skillMgr.BuildPromptSection(msg.Text)
	}

	// Inject agents into cognitive state for use in PLAN phase
	if ca.agentMgr != nil {
		state.Agents = ca.agentMgr.BuildPromptSection()
	}

	// Inject personality and persistent rules from config (Soul.md / Memory.md)
	state.Personality = ca.cfg.Personality
	state.PersistentRules = ca.cfg.PersistentRules

	// Inject self-evolution context: learned preferences, strategy hints, and model routing
	if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
		if pl := ca.evoEngine.PreferenceLearnerHook(); pl != nil {
			state.Preferences = pl.BuildPromptSection()
		}
		if so := ca.evoEngine.StrategyOptimizerHook(); so != nil {
			state.StrategyHints = so.BuildPromptSection()
		}
		if rr := ca.evoEngine.Router().SelectModel(string(state.Goal.Complexity)); rr.Routed {
			state.ModelOverride = rr.Model
			state.MaxTokensOverride = rr.MaxTokens
		}
	}

	// Delegate simple tasks to the plain Runtime
	if state.Goal.Complexity == ComplexitySimple {
		slog.Info("cognitive: simple task, delegating to runtime", "session", sess.ID)
		return ca.runtime.HandleMessage(ctx, ch, msg)
	}

	// Check if debate mode should be triggered
	if ca.shouldDebate(state) {
		slog.Info("cognitive: debate mode triggered", "session", sess.ID)
		return ca.handleDebate(ctx, ch, msg, sess, state, target)
	}

	cogCfg := ca.cfg.Cognitive
	confidenceThreshold := cogCfg.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.6
	}
	maxReplans := cogCfg.MaxReplanAttempts
	if maxReplans <= 0 {
		maxReplans = MaxReplanAttempts
	}

	// ── RL: build initial state after PERCEIVE ──────────────────────────────
	var rlState *rl.RLState
	var episodeCollector *EpisodeCollector
	rlEnabled := ca.rlPolicy != nil && ca.rlPolicy.IsEnabled()
	if rlEnabled {
		rlState = buildInitialRLState(state, len(ca.executor.tools.All()))
		episodeCollector = &EpisodeCollector{
			State:     rlState,
			StartTime: time.Now(),
		}
	}

	// Hoist loop-scoped variables for goto compatibility
	var finalAnswer string
	var plan *TaskPlan
	var obsResult *ObservationResult
	var reflection *Reflection
	var ppoStrategy *rl.PlanStrategyAction
	var dqnReplanAction rl.ReplanActionType

	cognitiveTurnStart := time.Now()

	for attempt := 0; attempt <= maxReplans; attempt++ {
		if attempt > 0 {
			slog.Info("cognitive: replanning", "attempt", attempt, "max", maxReplans, "session", sess.ID)
		}

		// ── PLAN ──────────────────────────────────────────────────────────────
		plan, err = ca.planner.Run(ctx, state)
		if err != nil {
			slog.Error("cognitive: plan failed", "err", err)
			break
		}
		plan.ReplanCount = attempt

		// RL: apply PPO plan strategy adjustment
		if rlEnabled && rlState != nil {
			ppoStrategy = ca.rlPolicy.SelectPlanStrategy(rlState)
			if ppoStrategy != nil {
				plan.OverallConfidence = clampRL(
					plan.OverallConfidence+ppoStrategy.ConfidenceAdj, 0, 1)
			}
			updateRLStateWithPlan(rlState, plan)
		}

		// Direct reply — skip ACT/OBSERVE
		if plan.DirectReply != "" {
			finalAnswer = plan.DirectReply
			slog.Info("cognitive: direct reply from plan phase", "session", sess.ID)
			if err := ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
				slog.Warn("cognitive: stream direct reply failed", "err", err)
			}
			break
		}

		// ── ACT ───────────────────────────────────────────────────────────────
		// Create TaskContext for multi-agent collaboration
		taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), state.UserMessage)
		observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, episodeCollector)
		if actErr != nil {
			slog.Error("cognitive: act failed", "err", actErr)
			break
		}

		// ── OBSERVE ───────────────────────────────────────────────────────────
		obsResult = ca.observer.Run(observations, plan)
		slog.Info("cognitive: observe complete",
			"success", obsResult.SuccessCount,
			"failure", obsResult.FailureCount,
			"progress", fmt.Sprintf("%.0f%%", obsResult.OverallProgress*100),
		)

		if ca.checkpointStore != nil && obsResult != nil {
			obsJSON, _ := json.Marshal(obsResult.Observations)
			planJSON, _ := json.Marshal(plan)
			cp := &TaskCheckpoint{
				ID:               fmt.Sprintf("cp-%s-%d", sess.ID, attempt),
				SessionID:        sess.ID,
				SubTaskIndex:     len(obsResult.Observations),
				ObservationsJSON: string(obsJSON),
				PlanJSON:         string(planJSON),
			}
			_ = ca.checkpointStore.Save(ctx, cp)
		}

		// RL: update state with observation results
		if rlEnabled && rlState != nil {
			updateRLStateWithObservation(rlState, obsResult)
		}

		// ── REFLECT ───────────────────────────────────────────────────────────
		reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
		if err != nil {
			slog.Error("cognitive: reflect failed", "err", err)
			break
		}

		finalAnswer = reflection.FinalAnswer
		if finalAnswer == "" {
			finalAnswer = "Task completed."
		}

		// Stream final answer to user
		if err := ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
			slog.Warn("cognitive: stream final answer failed", "err", err)
		}

		// RL: update reflection confidence and apply DQN replan adjustment
		if rlEnabled && rlState != nil && reflection != nil {
			rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
			if reflection.NeedsReplan {
				dqnReplanAction = ca.rlPolicy.SelectReplanAction(rlState)
				dqnWeight := ca.cfg.RL.DQN.ReplanWeight
				adjConfidence, shouldAbort := applyDQNReplanAdjustment(
					reflection.OverallConfidence, dqnReplanAction, dqnWeight,
				)
				slog.Info("cognitive: DQN replan adjustment",
					"action", dqnReplanAction.String(),
					"original_confidence", reflection.OverallConfidence,
					"adjusted_confidence", adjConfidence,
					"should_abort", shouldAbort,
				)
				if shouldAbort {
					slog.Info("cognitive: DQN recommends abort", "session", sess.ID)
					goto persist
				}
				reflection.OverallConfidence = adjConfidence
			}
		}

		// Check if replan is needed
		if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
			decision, _ := ca.reflector.RequestReplanApproval(ctx, ch, target, reflection)
			switch decision {
			case ReplanAbort:
				slog.Info("cognitive: replan aborted by user", "session", sess.ID)
				goto persist
			case ReplanContinue:
				slog.Info("cognitive: replan skipped (continue)", "session", sess.ID)
				goto persist
			case ReplanAdjust:
				slog.Info("cognitive: adjusting and replanning", "session", sess.ID)
				if reflection.SuggestedAdjustment != "" {
					state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
				}
				continue // next attempt
			}
		}

		break // done, no replan needed
	}

persist:
	if ca.checkpointStore != nil {
		_ = ca.checkpointStore.Delete(ctx, sess.ID)
	}

	// Optional user feedback (thumbs up/down) for RL and/or self-evolution.
	var userFeedback float64
	needFeedback := (rlEnabled && episodeCollector != nil && ca.rlTrainer != nil) ||
		(ca.evoEngine != nil && ca.evoEngine.IsEnabled())
	if needFeedback {
		if sender, ok := ch.(channel.FeedbackSender); ok {
			feedbackCtx, feedbackCancel := context.WithTimeout(ctx, 20*time.Second)
			fb, err := sender.SendFeedbackRequest(feedbackCtx, target)
			feedbackCancel()
			if err != nil {
				slog.Debug("cognitive: feedback collection failed", "err", err)
			} else {
				userFeedback = fb
			}
		}
	}
	if rlEnabled && episodeCollector != nil && ca.rlTrainer != nil {
		ca.recordRLEpisode(state, plan, obsResult, reflection, ppoStrategy, episodeCollector, userFeedback, dqnReplanAction)
	}

	// Evolution: record model routing outcome and dispatch events.
	if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
		if state.ModelOverride != "" {
			succeeded := reflection != nil && reflection.Succeeded
			ca.evoEngine.Router().RecordOutcome(string(state.Goal.Complexity), succeeded)
		}
		ca.dispatchEvolutionEvents(state, plan, obsResult, reflection, userFeedback, cognitiveTurnStart)
	}

	// Persist session
	if err := ca.sessions.Persist(ctx, sess); err != nil {
		slog.Error("cognitive: failed to persist session", "err", err)
	}

	// Save user message to memory.md
	if ca.memStore != nil {
		if err := ca.memStore.Save(ctx, memory.Entry{
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("cognitive: failed to save memory.md", "err", err)
		}
	}

	return nil
}

// streamFinalAnswer sends the final answer to the user via streaming.
func (ca *CognitiveAgent) streamFinalAnswer(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	sess *session.Session,
	answer string,
) error {
	// Record in session
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "assistant",
		Content:   answer,
		CreatedAt: time.Now(),
	})

	// Try streaming
	updater, err := ch.SendStreaming(ctx, target)
	if err != nil {
		// Fallback to non-streaming
		return ch.Send(ctx, channel.OutboundMessage{
			Channel:   target.Channel,
			ChannelID: target.ChannelID,
			Text:      answer,
		})
	}
	return updater.Finish(answer)
}

// shouldDebate checks if the current task should trigger debate mode.
func (ca *CognitiveAgent) shouldDebate(state *CognitiveState) bool {
	if ca.agentMgr == nil {
		return false
	}

	agents := ca.agentMgr.All()
	if len(agents) < 2 {
		return false
	}

	// Check for decision/comparison keywords
	lower := strings.ToLower(state.UserMessage)
	debateKeywords := []string{
		"compare", "versus", "vs", "better", "worse", "pros and cons",
		"advantages", "disadvantages", "evaluate", "assess", "decide",
		"choose", "which", "should i", "recommend",
	}

	for _, kw := range debateKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	return false
}

// handleDebate executes a debate between two agents and synthesizes the result.
func (ca *CognitiveAgent) handleDebate(
	ctx context.Context,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
	state *CognitiveState,
	target channel.MessageTarget,
) error {
	agents := ca.agentMgr.All()
	proposer, critic := SelectDebateAgents(agents, state.UserMessage)

	if proposer == "" || critic == "" {
		slog.Warn("cognitive: insufficient agents for debate, falling back to normal mode")
		return ca.runtime.HandleMessage(ctx, ch, msg)
	}

	// Build debate plan
	maxRounds := ca.debateCfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3 // fallback default
	}
	debatePlan := BuildDebatePlan(state.UserMessage, proposer, critic, DebateConfig{MaxRounds: maxRounds})

	// Execute debate via ACT phase
	taskCtx := NewTaskContext(fmt.Sprintf("debate_%d", time.Now().UnixNano()), state.UserMessage)
	observations, err := ca.executor.RunWithContext(ctx, ch, sess, target, debatePlan, taskCtx, nil, nil)
	if err != nil {
		return fmt.Errorf("debate execution failed: %w", err)
	}

	// Synthesize debate results
	synthesis := SynthesizeDebate(observations, proposer, critic)

	// Use reflector to generate final answer
	obsResult := ca.observer.Run(observations, debatePlan)
	reflection, err := ca.reflector.Run(ctx, ch, target, state, debatePlan, obsResult, 0)
	if err != nil {
		slog.Error("cognitive: debate reflection failed", "err", err)
		// Fallback: use synthesis directly
		return ca.streamFinalAnswer(ctx, ch, target, sess, synthesis)
	}

	finalAnswer := reflection.FinalAnswer
	if finalAnswer == "" {
		finalAnswer = synthesis
	}

	return ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer)
}

// dispatchEvolutionEvents fires self-evolution events based on the completed
// cognitive cycle. This enables the preference learner, skill synthesizer,
// and strategy optimizer to observe and learn from each interaction.
func (ca *CognitiveAgent) dispatchEvolutionEvents(
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	userFeedback float64,
	turnStart time.Time,
) {
	now := time.Now()

	// Collect tool names from observations.
	var toolsUsed []string
	if obsResult != nil {
		for _, obs := range obsResult.Observations {
			toolsUsed = append(toolsUsed, obs.ToolName)
		}
	}

	// Dispatch reflection event (feeds PreferenceLearner).
	succeeded := reflection != nil && reflection.Succeeded
	confidence := 0.0
	var lessons []string
	var finalAnswer string
	if reflection != nil {
		confidence = reflection.OverallConfidence
		lessons = reflection.LessonsLearned
		finalAnswer = reflection.FinalAnswer
	}
	replanCount := 0
	if plan != nil {
		replanCount = plan.ReplanCount
	}

	ca.evoEngine.DispatchReflection(evolution.ReflectionEvent{
		SessionID:      state.SessionID,
		UserID:         state.UserID,
		Goal:           state.Goal.Raw,
		Complexity:     string(state.Goal.Complexity),
		Succeeded:      succeeded,
		Confidence:     confidence,
		LessonsLearned: lessons,
		ToolsUsed:      toolsUsed,
		ReplanCount:    replanCount,
		UserFeedback:   userFeedback,
		FinalAnswer:    finalAnswer,
		Timestamp:      now,
	})

	// Dispatch episode event (feeds SkillSynthesizer and StrategyOptimizer).
	totalReward := 0.0
	if reflection != nil && obsResult != nil {
		totalReward = computeSimpleEpisodeReward(reflection, obsResult)
	}
	durationMs := int64(0)
	if !turnStart.IsZero() {
		durationMs = now.Sub(turnStart).Milliseconds()
	}

	ca.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
		SessionID:    state.SessionID,
		EpisodeID:    fmt.Sprintf("ep_%d", now.UnixNano()),
		Goal:         state.Goal.Raw,
		Complexity:   string(state.Goal.Complexity),
		Succeeded:    succeeded,
		TotalReward:  totalReward,
		ToolSequence: toolsUsed,
		ReplanCount:  replanCount,
		DurationMs:   durationMs,
		UserFeedback: userFeedback,
		Timestamp:    now,
	})

	// Dispatch individual tool execution events (feeds future hooks).
	if obsResult != nil {
		for _, obs := range obsResult.Observations {
			ca.evoEngine.DispatchToolExec(evolution.ToolExecEvent{
				SessionID:  state.SessionID,
				ToolName:   obs.ToolName,
				Succeeded:  !obs.Denied && obs.Error == "",
				Denied:     obs.Denied,
				DurationMs: obs.DurationMs,
				Timestamp:  now,
			})
		}
	}
}

// recordRLEpisode records PPO/DQN experiences and the full episode to the trainer.
// Runs asynchronously to avoid blocking the main cognitive loop.
func (ca *CognitiveAgent) recordRLEpisode(
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	ppoStrategy *rl.PlanStrategyAction,
	collector *EpisodeCollector,
	userFeedback float64,
	dqnAction rl.ReplanActionType,
) {
	rlState := collector.State
	episodeReward := computeSimpleEpisodeReward(reflection, obsResult)
	episodeReward += computeReflectionBonus(reflection)

	// Record PPO experience (plan strategy → episode outcome)
	if ppoStrategy != nil {
		collector.Add(rl.Experience{
			State:     rlState,
			Action:    ppoStrategy.ToVector(),
			Reward:    episodeReward,
			NextState: nil,
			Done:      true,
			Level:     rl.LevelPPO,
		})
	}

	// Record DQN experience (replan decision → episode outcome)
	if reflection != nil && reflection.NeedsReplan {
		collector.Add(rl.Experience{
			State:     rlState,
			Action:    []float64{float64(dqnAction)},
			Reward:    episodeReward,
			NextState: nil,
			Done:      true,
			Level:     rl.LevelDQN,
		})
	}

	// Fire-and-forget: record episode + add experiences to trainer buffer
	experiences := collector.GetExperiences()
	go func() {
		bgCtx := context.Background()

		subtaskCount := 0
		replanCount := 0
		if plan != nil {
			subtaskCount = len(plan.SubTasks)
			replanCount = plan.ReplanCount
		}

		successCount, failureCount, deniedCount := 0, 0, 0
		if obsResult != nil {
			successCount = obsResult.SuccessCount
			failureCount = obsResult.FailureCount
			deniedCount = obsResult.DeniedCount
		}

		succeeded := reflection != nil && reflection.Succeeded

		durationMs := time.Since(collector.StartTime).Milliseconds()

		if err := ca.rlTrainer.RecordEpisode(bgCtx, rl.EpisodeParams{
			SessionID:     state.SessionID,
			Goal:          state.Goal.Raw,
			Complexity:    string(state.Goal.Complexity),
			Succeeded:     succeeded,
			DurationMs:    durationMs,
			MaxDurationMs: 120000, // 2-minute baseline
			SubtaskCount:  subtaskCount,
			ReplanCount:   replanCount,
			SuccessCount:  successCount,
			FailureCount:  failureCount,
			DeniedCount:   deniedCount,
			UserFeedback:  userFeedback,
			Experiences:   experiences,
		}); err != nil {
			slog.Warn("cognitive: failed to record RL episode", "err", err)
		}

		// Add individual experiences to the replay buffer
		for _, exp := range experiences {
			ca.rlTrainer.AddExperience(exp)
		}
	}()
}

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
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
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
	checkpointStore    CheckpointStore
	contextManager     ContextManager
	taskLedger         taskledger.TaskLedger
	observationCallback func(result *ObservationResult)
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
	ca.perceiver = NewPerceiver(nil, "") // memStore + memBaseDir injected via SetMemoryStore
	scanner := NewProjectContextScanner()
	ca.perceiver.SetProjectScanner(scanner)
	gitProvider := NewGitContextProvider()
	ca.perceiver.SetGitProvider(gitProvider)
	budgetAlloc := NewContextBudgetAllocator()
	ca.perceiver.SetBudgetAllocator(budgetAlloc)
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
	// Preserve existing searcher, graph, scanner, git provider, budget, and RL policy when rebuilding the perceiver.
	oldSearcher := ca.perceiver.searcher
	oldGraph := ca.perceiver.graph
	oldScanner := ca.perceiver.scanner
	oldGitProvider := ca.perceiver.gitProvider
	oldBudgetAlloc := ca.perceiver.budgetAlloc
	oldRLPolicy := ca.perceiver.rlPolicy
	ca.perceiver = NewPerceiver(s, ca.perceiver.memBaseDir)
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
	if oldBudgetAlloc != nil {
		ca.perceiver.SetBudgetAllocator(oldBudgetAlloc)
	}
	if oldRLPolicy != nil {
		ca.perceiver.SetRLPolicy(oldRLPolicy)
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

// EvolutionEngine returns the evolution engine, or nil if not configured.
func (ca *CognitiveAgent) EvolutionEngine() *evolution.Engine {
	return ca.evoEngine
}

// Sessions returns the session manager.
func (ca *CognitiveAgent) Sessions() *session.Manager {
	return ca.sessions
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

// SetContextManager injects a context manager for compression pipeline support.
// Propagates to the inner runtime so simple-task delegation also benefits.
func (ca *CognitiveAgent) SetContextManager(cm ContextManager) {
	ca.contextManager = cm
	ca.runtime.SetContextManager(cm)
}

// SetTaskLedger injects a task ledger for tracking cognitive subtasks.
func (ca *CognitiveAgent) SetTaskLedger(tl taskledger.TaskLedger) {
	ca.taskLedger = tl
	ca.runtime.SetTaskLedger(tl)
}

// SetObservationCallback registers a function that is called after each OBSERVE
// phase completes. Used by the eval harness to capture assertion statistics.
func (ca *CognitiveAgent) SetObservationCallback(fn func(result *ObservationResult)) {
	ca.observationCallback = fn
}

// HandleMessage processes an inbound message through the cognitive loop.
func (ca *CognitiveAgent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	sess, err := ca.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if ca.checkpointStore != nil && strings.HasPrefix(strings.TrimSpace(msg.Text), "/resume") {
		return ca.handleResume(ctx, ch, msg, sess)
	}

	var parentTaskID string
	if ca.taskLedger != nil {
		parentTaskID = fmt.Sprintf("cog_%d", time.Now().UnixNano())
		task := taskledger.Task{
			ID:    parentTaskID,
			Kind:  taskledger.TaskKindUserRequest,
			State: taskledger.TaskStateRunning,
			Title: truncateStr(msg.Text, 100),
		}
		if err := ca.taskLedger.Register(ctx, task); err != nil {
			slog.Warn("cognitive: failed to register task", "err", err)
			parentTaskID = ""
		} else {
			defer func() {
				task.State = taskledger.TaskStateCompleted
				now := time.Now()
				task.CompletedAt = &now
				if err := ca.taskLedger.Update(ctx, task); err != nil {
					slog.Warn("cognitive: failed to complete task", "err", err)
				}
			}()
		}
	}

	// Add user message to session
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	// Compress context before perceive
	if ca.contextManager != nil {
		if _, err := ca.contextManager.Compress(ctx, sess, ""); err != nil {
			slog.Warn("cognitive: context compression failed", "session", sess.ID, "err", err)
		}
	} else {
		if err := CompactHistory(ctx, ca.planner.provider, sess, ca.llmCfg.Model); err != nil {
			slog.Warn("cognitive: history compaction failed", "session", sess.ID, "err", err)
		}
	}

	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	// ── PERCEIVE ──────────────────────────────────────────────────────────────
	donePerceive := ca.registerSubtask(ctx, parentTaskID, "PERCEIVE phase")
	state, err := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	donePerceive()
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
		simpleStart := time.Now()
		err := ca.runtime.HandleMessage(ctx, ch, msg)

		if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
			durationMs := time.Since(simpleStart).Milliseconds()
			succeeded := err == nil
			ca.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
				SessionID:  sess.ID,
				Goal:       msg.Text,
				Complexity: string(ComplexitySimple),
				Succeeded:  succeeded,
				DurationMs: durationMs,
				Timestamp:  time.Now(),
			})
		}

		return err
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
	if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
		if so := ca.evoEngine.StrategyOptimizerHook(); so != nil && so.IsHardControlEnabled() {
			if evoThreshold := so.GetReplanThreshold(); evoThreshold > 0 {
				confidenceThreshold = evoThreshold
				slog.Debug("cognitive: using evolution replan threshold",
					"threshold", evoThreshold, "session", sess.ID)
			}
		}
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
		donePlan := ca.registerSubtask(ctx, parentTaskID, fmt.Sprintf("PLAN phase (attempt %d)", attempt))
		plan, err = ca.planner.Run(ctx, state)
		donePlan()
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
		doneAct := ca.registerSubtask(ctx, parentTaskID, "ACT phase")
		// Create TaskContext for multi-agent collaboration
		taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), state.UserMessage)
		observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, episodeCollector)
		doneAct()
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

		if ca.observationCallback != nil && obsResult != nil {
			ca.observationCallback(obsResult)
		}

		if ca.checkpointStore != nil && obsResult != nil {
			obsJSON, _ := json.Marshal(obsResult)
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
		doneReflect := ca.registerSubtask(ctx, parentTaskID, "REFLECT phase")
		reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
		doneReflect()
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

// handleResume restores execution from the last checkpoint for a session.
func (ca *CognitiveAgent) handleResume(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	parts := strings.Fields(msg.Text)
	resumeSessionID := sess.ID
	if len(parts) > 1 {
		resumeSessionID = parts[1]
	}

	cp, err := ca.checkpointStore.Load(ctx, resumeSessionID)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}
	if cp == nil {
		return ch.Send(ctx, channel.OutboundMessage{
			Channel:   target.Channel,
			ChannelID: target.ChannelID,
			Text:      "No checkpoint found for session " + resumeSessionID + ".",
		})
	}

	var plan TaskPlan
	if err := json.Unmarshal([]byte(cp.PlanJSON), &plan); err != nil {
		return fmt.Errorf("unmarshal checkpoint plan: %w", err)
	}

	var obsResult ObservationResult
	if err := json.Unmarshal([]byte(cp.ObservationsJSON), &obsResult); err != nil {
		return fmt.Errorf("unmarshal checkpoint observations: %w", err)
	}

	_ = ch.Send(ctx, channel.OutboundMessage{
		Channel:   target.Channel,
		ChannelID: target.ChannelID,
		Text: fmt.Sprintf(
			"Resuming from checkpoint: %d/%d subtasks completed (progress: %.0f%%).",
			obsResult.SuccessCount, len(plan.SubTasks), obsResult.OverallProgress*100,
		),
	})

	for _, st := range plan.SubTasks {
		completed := false
		for _, obs := range obsResult.Observations {
			if obs.SubTaskID == st.ID && obs.Error == "" && !obs.Denied {
				completed = true
				break
			}
		}
		if completed {
			st.Status = SubTaskDone
		} else {
			st.Status = SubTaskPending
		}
	}

	taskCtx := NewTaskContext(fmt.Sprintf("resume_%d", time.Now().UnixNano()), "Resume from checkpoint")
	observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, &plan, taskCtx, nil, nil)
	if actErr != nil {
		slog.Error("cognitive: resume act failed", "err", actErr)
	}

	newObsResult := ca.observer.Run(observations, &plan)

	state, _ := ca.perceiver.Run(ctx, sess, "Resume execution from checkpoint", msg.UserID)
	if state != nil {
		state.Personality = ca.cfg.Personality
		state.PersistentRules = ca.cfg.PersistentRules
	}

	reflection, err := ca.reflector.Run(ctx, ch, target, state, &plan, newObsResult, 0)
	if err != nil {
		slog.Error("cognitive: resume reflect failed", "err", err)
	}

	if reflection != nil && reflection.FinalAnswer != "" {
		_ = ca.streamFinalAnswer(ctx, ch, target, sess, reflection.FinalAnswer)
	}

	_ = ca.checkpointStore.Delete(ctx, resumeSessionID)

	if err := ca.sessions.Persist(ctx, sess); err != nil {
		slog.Error("cognitive: persist failed after resume", "err", err)
	}

	return nil
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

	// Dispatch tool execution events first (synchronous — populates per-tool
	// buffers in TrajectoryRecorder before the async episode dispatch reads them).
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
	// Runs after DispatchToolExec so that per-tool buffers are already populated.
	durationMs := int64(0)
	if !turnStart.IsZero() {
		durationMs = now.Sub(turnStart).Milliseconds()
	}
	totalReward := 0.0
	if reflection != nil && obsResult != nil {
		totalReward = computeSimpleEpisodeReward(reflection, obsResult, durationMs, replanCount, userFeedback)
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
}

// registerSubtask records a cognitive subtask in the task ledger and returns
// a function that marks it completed. If the ledger is unavailable the
// returned function is a no-op.
func (ca *CognitiveAgent) registerSubtask(ctx context.Context, parentID, title string) func() {
	if ca.taskLedger == nil || parentID == "" {
		return func() {}
	}
	task := taskledger.Task{
		ID:       fmt.Sprintf("sub_%d", time.Now().UnixNano()),
		ParentID: parentID,
		Kind:     taskledger.TaskKindCognitiveSubtask,
		State:    taskledger.TaskStateRunning,
		Title:    title,
	}
	if err := ca.taskLedger.Register(ctx, task); err != nil {
		slog.Warn("cognitive: failed to register subtask", "title", title, "err", err)
		return func() {}
	}
	return func() {
		task.State = taskledger.TaskStateCompleted
		now := time.Now()
		task.CompletedAt = &now
		if err := ca.taskLedger.Update(ctx, task); err != nil {
			slog.Warn("cognitive: failed to complete subtask", "title", title, "err", err)
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
	durationMs := time.Since(collector.StartTime).Milliseconds()
	replanCount := 0
	if plan != nil {
		replanCount = plan.ReplanCount
	}
	episodeReward := computeSimpleEpisodeReward(reflection, obsResult, durationMs, replanCount, userFeedback)
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
		if plan != nil {
			subtaskCount = len(plan.SubTasks)
		}

		successCount, failureCount, deniedCount := 0, 0, 0
		if obsResult != nil {
			successCount = obsResult.SuccessCount
			failureCount = obsResult.FailureCount
			deniedCount = obsResult.DeniedCount
		}

		succeeded := reflection != nil && reflection.Succeeded

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

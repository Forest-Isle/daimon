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
	"github.com/Forest-Isle/IronClaw/internal/cortex"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/observability"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const MaxReplanAttempts = 3
// CognitiveLoop implements the structured PERCEIVE->PLAN->ACT->OBSERVE->REFLECT loop
// as a LoopStrategy. It is a self-contained 5-phase agent loop that handles complex
// multi-step tasks with replanning support.
type CognitiveLoop struct {
	// Phase components (always set after NewCognitiveLoop)
	perceiver *Perceiver
	planner   *Planner
	executor  *Executor
	observer  *Observer
	reflector *Reflector

	// Optional advanced planners (nil = disabled)
	mctsPlanner  *MCTSPlanner
	treePlanner  *StrategicTreePlanner
	codebaseIndex *CodebaseIndex
	cortex       *cortex.UnifiedRetriever

	// Config
	debateCfg      config.DebateSettings
	entityExtractor *graph.LLMEntityExtractor
	evoEngine      *evolution.Engine
	checkpointStore CheckpointStore
	planMode       *PlanMode

	// Transient
	approvalFunc        ApprovalFunc
	observationCallback func(result *ObservationResult)
}

// NewCognitiveLoop creates a CognitiveLoop, wiring all phase components together
// from the agent's dependency bundle and optional subsystems.
func NewCognitiveLoop(deps AgentDeps, opts *CognitiveAgentOptions) *CognitiveLoop {
	cl := &CognitiveLoop{}

	cogCfg := deps.Core.Cfg.Cognitive

	// Build phase components using deps
	cl.perceiver = NewPerceiver(deps.Memory.Store, deps.Memory.BaseDir)
	scanner := NewProjectContextScanner()
	cl.perceiver.SetProjectScanner(scanner)
	gitProvider := NewGitContextProvider()
	cl.perceiver.SetGitProvider(gitProvider)
	budgetAlloc := NewContextBudgetAllocator()
	cl.perceiver.SetBudgetAllocator(budgetAlloc)
	cl.planner = NewPlanner(deps.Core.Provider, deps.Core.Tools, cogCfg, deps.Core.LLMCfg.Model)
	cl.executor = NewExecutor(deps.Core.Tools, deps.Core.DB, nil, cogCfg)
	cl.executor.SetToolCache(NewToolResultCache())
	cl.observer = NewObserver()
	cl.reflector = NewReflector(deps.Core.Provider, deps.Memory.Store, cogCfg, deps.Core.LLMCfg.Model)

	// Wire deps to subsystems
	if deps.Security.HookMgr != nil {
		cl.executor.SetHookManager(deps.Security.HookMgr)
	}
	if deps.Security.PermEngine != nil {
		cl.executor.SetPermissionEngine(deps.Security.PermEngine)
	}
	if deps.Security.Interceptor != nil {
		cl.executor.SetInterceptorChain(deps.Security.Interceptor)
	}
	cl.executor.SetDashboardEmitter(deps.Observability.Emitter)
	if deps.Memory.FactExtractor != nil {
		cl.reflector.SetFactExtractor(deps.Memory.FactExtractor)
	}
	if deps.Memory.LifecycleMgr != nil {
		cl.reflector.SetLifecycleManager(deps.Memory.LifecycleMgr)
	}
	if deps.Observability.ReplayRecorder != nil {
		cl.executor.SetReplayRecorder(deps.Observability.ReplayRecorder)
	}

	cl.applyOptions(opts)

	return cl
}

func (cl *CognitiveLoop) applyOptions(opts *CognitiveAgentOptions) {
	if opts == nil {
		return
	}
	if opts.CodebaseIndex != nil {
		cl.codebaseIndex = opts.CodebaseIndex
	}
	if opts.KnowledgeSearcher != nil {
		cl.perceiver.SetKnowledgeSearcher(opts.KnowledgeSearcher)
	}
	if opts.KnowledgeGraph != nil {
		cl.perceiver.SetKnowledgeGraph(opts.KnowledgeGraph)
	}
	if opts.EntityExtractor != nil {
		cl.entityExtractor = opts.EntityExtractor
		cl.reflector.SetEntityExtractor(opts.EntityExtractor)
	}
	if opts.TreePlanner != nil {
		cl.treePlanner = opts.TreePlanner
	}
	if opts.MCTSPlanner != nil {
		cl.mctsPlanner = opts.MCTSPlanner
	}
	if opts.EvolutionEngine != nil {
		cl.evoEngine = opts.EvolutionEngine
	}
	if opts.MemoryNotifyFunc != nil {
		cl.reflector.SetMemoryNotifyFunc(opts.MemoryNotifyFunc)
	}
	if opts.CheckpointStore != nil {
		cl.checkpointStore = opts.CheckpointStore
	}
	if opts.ObservationCallback != nil {
		cl.observationCallback = opts.ObservationCallback
	}
	if opts.ApprovalFunc != nil {
		cl.approvalFunc = opts.ApprovalFunc
		cl.executor.approvalFunc = opts.ApprovalFunc
	}
	if opts.PlanMode != nil {
		cl.planMode = opts.PlanMode
		if cl.executor != nil {
			cl.executor.SetPlanMode(opts.PlanMode)
		}
	}
	if opts.CortexRetriever != nil {
		cl.cortex = opts.CortexRetriever
		cl.perceiver.SetCortexRetriever(opts.CortexRetriever)
	}
	if opts.DebateConfig != (config.DebateSettings{}) {
		cl.debateCfg = opts.DebateConfig
	}
}

// Execute runs the 5-phase cognitive loop for a single inbound message.
// This is the LoopStrategy entry point called by Agent.HandleMessage.
func (cl *CognitiveLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	sessionStart := time.Now()

	// Checkpoint resume support
	if cl.checkpointStore != nil && strings.HasPrefix(strings.TrimSpace(msg.Text), "/resume") {
		return cl.handleResume(ctx, a, ch, msg, sess, target)
	}

	// Register parent task for cognitive subtask tracking
	parentTaskID := cl.registerParentTask(ctx, a, msg)

	// Phase 1: PERCEIVE
	state, err := cl.runPerceivePhase(ctx, a, sess, msg, parentTaskID)
	if err != nil {
		return err
	}

	// Simple tasks bypass the cognitive loop
	if state.Goal.Complexity == ComplexitySimple {
		return cl.delegateToRuntime(ctx, a, ch, msg, sess)
	}

	// Debate mode for comparison/decision tasks
	if cl.shouldDebate(a, state) {
		return cl.handleDebate(ctx, a, ch, msg, sess, state, target)
	}

	// Phase 2-5: PLAN -> ACT -> OBSERVE -> REFLECT loop with replan support
	cognitiveTurnStart := time.Now()
	plan, mctsCandidates, mctsActive, treePlanner := cl.runPrePlanSearch(ctx, a, sess, state)

	var (
		finalAnswer      string
		obsResult        *ObservationResult
		reflection       *Reflection
	)
	cogCfg := a.deps.Core.Cfg.Cognitive
	confidenceThreshold := cl.resolveConfidenceThreshold(cogCfg, a)
	maxReplans := cogCfg.MaxReplanAttempts
	if maxReplans <= 0 {
		maxReplans = MaxReplanAttempts
	}

	// Main replan loop
	for attempt := 0; attempt <= maxReplans; attempt++ {
		if attempt > 0 {
			slog.Info("cognitive: replanning", "attempt", attempt, "max", maxReplans, "session", sess.ID)
			cl.publishPhaseEvent(a, sess.ID, "REPLAN_START")
			if emitter := a.deps.Observability.Emitter; emitter != nil {
				reason := "low_confidence"
				if reflection != nil && reflection.SuggestedAdjustment != "" {
					reason = "adjustment: " + util.TruncateStr(reflection.SuggestedAdjustment, 100)
				}
				emitter.EmitReplanStart(sess.ID, attempt, reason)
			}
		}

		// Phase 2: PLAN
		plan, err = cl.runPlanPhase(ctx, a, sess, target, state, plan, mctsCandidates, mctsActive, treePlanner, attempt, parentTaskID)
		if err != nil {
			return fmt.Errorf("plan: %w", err)
		}

		// Set plan metadata
		plan.ReplanCount = attempt
		if emitter := a.deps.Observability.Emitter; emitter != nil {
			complexity := string(state.Goal.Complexity)
			emitter.EmitPlanGenerated(sess.ID, len(plan.SubTasks), complexity, plan.DirectReply != "")
		}
		// Direct reply shortcut (no tools needed)
		if plan.DirectReply != "" {
			finalAnswer = plan.DirectReply
			slog.Info("cognitive: direct reply from plan phase", "session", sess.ID)
			if err := cl.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
				slog.Warn("cognitive: stream direct reply failed", "err", err)
			}
			// No RESOLVE phase needed for direct replies — skip to finalization
			reflection = &Reflection{
				OverallConfidence: plan.OverallConfidence,
				Succeeded:         true,
				FinalAnswer:       finalAnswer,
			}
			break
		}

		// Phase 3: ACT
		observations, actErr := cl.runActPhase(ctx, a, ch, sess, target, plan, parentTaskID)
		if actErr != nil {
			return fmt.Errorf("act: %w", actErr)
		}

		// Phase 4: OBSERVE
		obsResult = cl.runObservePhase(ctx, a, sess, observations, plan)

		// Observation callback
		if cl.observationCallback != nil && obsResult != nil {
			cl.observationCallback(obsResult)
		}

		// Checkpoint save
		if cl.checkpointStore != nil && obsResult != nil {
			cl.saveCheckpoint(ctx, sess.ID, plan, obsResult, attempt)
		}
		// Phase 5: REFLECT
		reflection, err = cl.runReflectPhase(ctx, a, ch, target, sess, state, plan, obsResult, attempt, parentTaskID)
		if err != nil {
			return fmt.Errorf("reflect: %w", err)
		}

		// Assertion-based override for reflection success
		if reflection != nil && !reflection.Succeeded && obsResult != nil {
			cl.overrideReflectionWithAssertions(reflection, obsResult)
		}

		// Final answer
		finalAnswer = reflection.FinalAnswer
		if finalAnswer == "" {
			finalAnswer = "Task completed."
		}
		if err := cl.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
			slog.Warn("cognitive: stream final answer failed", "err", err)
		}
		// Replan decision
		if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
			decision, _ := cl.reflector.RequestReplanApproval(ctx, ch, target, reflection)
			switch decision {
			case ReplanAbort:
				slog.Info("cognitive: replan aborted by user", "session", sess.ID)
				return nil
			case ReplanContinue:
				slog.Info("cognitive: replan skipped (continue)", "session", sess.ID)
				return nil
			case ReplanAdjust:
				slog.Info("cognitive: adjusting and replanning", "session", sess.ID)
				if reflection.SuggestedAdjustment != "" {
					state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
				}
				continue
			}
		}

		// Success — exit the replan loop
		break
	}

	// Finalize cognitive session: evolution events, checkpoint cleanup
	cl.finalizeCognitiveSession(ctx, a, ch, sess, target, msg, state, plan, obsResult, reflection,
		sessionStart, cognitiveTurnStart)
	return nil
	}
	// ────────────────────────────── Phase Runners ──────────────────────────────
// runPerceivePhase executes the PERCEIVE phase: parse goal, retrieve memories, assess complexity.
func (cl *CognitiveLoop) runPerceivePhase(
	ctx context.Context,
	a *Agent,
	sess *session.Session,
	msg channel.InboundMessage,
	parentTaskID string,
) (*CognitiveState, error) {
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseStart(sess.ID, "PERCEIVE")
	}
	perceiveStart := time.Now()
	donePerceive := cl.registerSubtask(ctx, a, parentTaskID, "PERCEIVE phase")
	state, err := cl.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	donePerceive()
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseEnd(sess.ID, "PERCEIVE", time.Since(perceiveStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(perceiveStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "perceive")))
	if err != nil {
		return nil, fmt.Errorf("perceive: %w", err)
	}

	// Semantic code search (optional codebase index)
	if cl.codebaseIndex != nil && cl.codebaseIndex.IsAvailable() {
		if results, searchErr := cl.codebaseIndex.Search(ctx, msg.Text, 3); searchErr != nil {
			slog.Warn("cognitive: semantic code search failed", "session", sess.ID, "err", searchErr)
		} else if len(results) > 0 {
			for _, chunk := range results {
				state.KnowledgeContext = append(state.KnowledgeContext,
					fmt.Sprintf("Code match %s:%d-%d (score %.3f)\n%s",
						chunk.FilePath, chunk.StartLine, chunk.EndLine, chunk.Score, chunk.Content))
			}
		}
	}

	// Skills and agents prompt sections
	if a.deps.MultiAgent.SkillMgr != nil {
		state.Skills = a.deps.MultiAgent.SkillMgr.BuildPromptSection(msg.Text)
	}
	if a.deps.MultiAgent.AgentMgr != nil {
		state.Agents = a.deps.MultiAgent.AgentMgr.BuildPromptSection()
	}
	state.Personality = a.deps.Core.Cfg.Personality
	state.PersistentRules = a.deps.Core.Cfg.PersistentRules

	// Evolution integration
	if cl.evoEngine != nil && cl.evoEngine.IsEnabled() {
		if pl := cl.evoEngine.PreferenceLearnerHook(); pl != nil {
			state.Preferences = pl.BuildPromptSection()
		}
		if so := cl.evoEngine.StrategyOptimizerHook(); so != nil {
			state.StrategyHints = so.BuildPromptSection()
		}
		if rr := cl.evoEngine.Router().SelectModel(string(state.Goal.Complexity)); rr.Routed {
			state.ModelOverride = rr.Model
			state.MaxTokensOverride = rr.MaxTokens
		}
	}

	cl.publishPhaseEvent(a, sess.ID, "PERCEIVE_COMPLETE")

	return state, nil
}

// runPlanPhase executes the PLAN phase: LLM call to produce a structured TaskPlan.
func (cl *CognitiveLoop) runPlanPhase(
	ctx context.Context,
	a *Agent,
	sess *session.Session,
	target channel.MessageTarget,
	state *CognitiveState,
	plan *TaskPlan,
	mctsCandidates []PlanCandidate,
	mctsActive bool,
	treePlanner *StrategicTreePlanner,
	attempt int,
	parentTaskID string,
) (*TaskPlan, error) {
	// MCTS active path
	if mctsActive {
		if attempt > 0 && len(mctsCandidates) > 1 {
			nextIdx := attempt - 1
			if nextIdx < len(mctsCandidates) {
				plan = mctsCandidates[nextIdx].Plan
				slog.Info("mcts: replan from candidate tree",
					"attempt", attempt,
					"candidate_idx", nextIdx,
					"strategy", mctsCandidates[nextIdx].Strategy,
					"score", mctsCandidates[nextIdx].LLMScore,
				)
			}
		}
		slog.Info("cognitive: using MCTS plan", "attempt", attempt, "plan_steps", len(plan.SubTasks))
		return plan, nil
	}

	// Tree planner path
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseStart(sess.ID, "PLAN")
	}
	planStart := time.Now()
	donePlan := cl.registerSubtask(ctx, a, parentTaskID, fmt.Sprintf("PLAN phase (attempt %d)", attempt))

	var err error
	if treePlanner != nil {
		plan = treePlanner.Select()
		if plan == nil {
			// Expand tree with failure context from previous attempt
			failureSummary := buildTreeFailureSummary(nil, nil) // first attempt
			if expandErr := treePlanner.Expand(ctx, failureSummary); expandErr != nil {
				donePlan()
				if emitter := a.deps.Observability.Emitter; emitter != nil {
					emitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
				}
				observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
					metric.WithAttributes(attribute.String("phase", "plan")))
				return nil, fmt.Errorf("tree-planner expand: %w", expandErr)
			}
			plan = treePlanner.Select()
			if plan == nil {
				donePlan()
				if emitter := a.deps.Observability.Emitter; emitter != nil {
					emitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
				}
				observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
					metric.WithAttributes(attribute.String("phase", "plan")))
				return nil, fmt.Errorf("tree-planner select: no plan available after expand")
			}
		}
		slog.Info("tree-planner: selected plan",
			"strategy", treePlanner.currentNode.Candidates[treePlanner.currentIdx].Strategy,
			"score", treePlanner.currentNode.Candidates[treePlanner.currentIdx].LLMScore,
			"depth", treePlanner.currentNode.Depth)
	} else {
		plan, err = cl.planner.Run(ctx, state)
	}
	donePlan()
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "plan")))
	if err != nil {
		return nil, fmt.Errorf("plan llm: %w", err)
	}

	cl.publishPhaseEvent(a, sess.ID, "PLAN_COMPLETE")
	return plan, nil
}

// runActPhase executes the ACT phase: topological scheduling + parallel tool execution.
func (cl *CognitiveLoop) runActPhase(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	plan *TaskPlan,
	parentTaskID string,
) ([]Observation, error) {
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseStart(sess.ID, "ACT")
	}
	actStart := time.Now()
	doneAct := cl.registerSubtask(ctx, a, parentTaskID, "ACT phase")
	taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), "Cognitive execution")
	observations, err := cl.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx)
	doneAct()
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseEnd(sess.ID, "ACT", time.Since(actStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(actStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "act")))
	if err != nil {
		return nil, fmt.Errorf("executor run: %w", err)
	}

	cl.publishPhaseEvent(a, sess.ID, "ACT_COMPLETE")
	return observations, nil
}

// runObservePhase executes the OBSERVE phase: analyze observations and produce aggregate results.
func (cl *CognitiveLoop) runObservePhase(
	ctx context.Context,
	a *Agent,
	sess *session.Session,
	observations []Observation,
	plan *TaskPlan,
) *ObservationResult {
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseStart(sess.ID, "OBSERVE")
	}
	observeStart := time.Now()
	obsResult := cl.observer.Run(observations, plan)
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseEnd(sess.ID, "OBSERVE", time.Since(observeStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(observeStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "observe")))

	slog.Info("cognitive: observe complete",
		"success", obsResult.SuccessCount,
		"failure", obsResult.FailureCount,
		"progress", fmt.Sprintf("%.0f%%", obsResult.OverallProgress*100),
	)
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitObservationResult(sess.ID, obsResult.SuccessCount, obsResult.FailureCount,
			obsResult.SuccessCount+obsResult.FailureCount, obsResult.OverallProgress)
	}

	cl.publishPhaseEvent(a, sess.ID, "OBSERVE_COMPLETE")
	return obsResult
}

// runReflectPhase executes the REFLECT phase: LLM evaluation + confidence assessment.
func (cl *CognitiveLoop) runReflectPhase(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	target channel.MessageTarget,
	sess *session.Session,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	attempt int,
	parentTaskID string,
) (*Reflection, error) {
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseStart(sess.ID, "REFLECT")
	}
	reflectStart := time.Now()
	doneReflect := cl.registerSubtask(ctx, a, parentTaskID, "REFLECT phase")
	reflection, err := cl.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
	doneReflect()
	if emitter := a.deps.Observability.Emitter; emitter != nil {
		emitter.EmitPhaseEnd(sess.ID, "REFLECT", time.Since(reflectStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(reflectStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "reflect")))
	if err != nil {
		return nil, fmt.Errorf("reflector run: %w", err)
	}

	cl.publishPhaseEvent(a, sess.ID, "REFLECT_COMPLETE")
	return reflection, nil
}

// ────────────────────────────── Pre-Plan Search ──────────────────────────────

// runPrePlanSearch runs MCTS or tree-planning search before the main loop.
func (cl *CognitiveLoop) runPrePlanSearch(
	ctx context.Context,
	a *Agent,
	sess *session.Session,
	state *CognitiveState,
) (*TaskPlan, []PlanCandidate, bool, *StrategicTreePlanner) {
	var (
		plan           *TaskPlan
		mctsCandidates []PlanCandidate
		mctsActive     bool
	)
	treePlanner := cl.treePlanner
	mctsPlanner := cl.mctsPlanner

	if mctsPlanner != nil {
		slog.Info("cognitive: running MCTS search", "session", sess.ID)
		mctsPlan, candidates, err := mctsPlanner.Search(ctx, state)
		if err != nil {
			slog.Warn("mcts: search failed, falling back", "err", err)
		} else {
			plan = mctsPlan
			mctsCandidates = candidates
			mctsActive = true
			slog.Info("mcts: search complete",
				"plan_steps", len(plan.SubTasks),
				"candidates", len(candidates),
				"tree_stats", mctsPlanner.TreeStats(),
			)
		}
	}

	if !mctsActive && treePlanner != nil {
		if err := treePlanner.GenerateCandidates(ctx, state); err != nil {
			slog.Warn("tree-planner: generate candidates failed, falling back to linear", "err", err)
			treePlanner = nil
		}
	}

	return plan, mctsCandidates, mctsActive, treePlanner
}

// ────────────────────────────── Finalization ──────────────────────────────

// finalizeCognitiveSession handles post-loop cleanup: evolution events, checkpoint cleanup.
func (cl *CognitiveLoop) finalizeCognitiveSession(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	msg channel.InboundMessage,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	sessionStart time.Time,
	cognitiveTurnStart time.Time,
) {
	// Clean up checkpoint
	if cl.checkpointStore != nil {
		if err := cl.checkpointStore.Delete(ctx, sess.ID); err != nil {
			slog.Warn("cognitive: delete checkpoint failed", "session", sess.ID, "err", err)
		}
	}

	// Collect user feedback
	var userFeedback float64
	needFeedback := cl.evoEngine != nil && cl.evoEngine.IsEnabled()
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

	// Evolution events
	if cl.evoEngine != nil && cl.evoEngine.IsEnabled() {
		if state.ModelOverride != "" {
			succeeded := reflection != nil && reflection.Succeeded
			cl.evoEngine.Router().RecordOutcome(string(state.Goal.Complexity), succeeded)
		}
		cl.dispatchEvolutionEvents(ctx, a, state, plan, obsResult, reflection, userFeedback, cognitiveTurnStart)
	}
	if cl.evoEngine != nil {
		cl.evoEngine.WaitPending()
	}
}

// ────────────────────────────── Debate Handling ──────────────────────────────

// shouldDebate checks if the current task should trigger debate mode.
func (cl *CognitiveLoop) shouldDebate(a *Agent, state *CognitiveState) bool {
	if a.deps.MultiAgent.AgentMgr == nil {
		return false
	}
	agents := a.deps.MultiAgent.AgentMgr.All()
	if len(agents) < 2 {
		return false
	}

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
func (cl *CognitiveLoop) handleDebate(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
	state *CognitiveState,
	target channel.MessageTarget,
) error {
	agents := a.deps.MultiAgent.AgentMgr.All()
	proposer, critic := SelectDebateAgents(agents, state.UserMessage)

	if proposer == "" || critic == "" {
		slog.Warn("cognitive: insufficient agents for debate, falling back to normal mode")
		rt := NewAgent(a.deps, &SimpleLoop{}, NewEventBus())
		if cl.approvalFunc != nil {
			rt.SetApprovalFunc(cl.approvalFunc)
		}
		return rt.HandleMessage(ctx, ch, msg)
	}

	maxRounds := cl.debateCfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	debatePlan := BuildDebatePlan(state.UserMessage, proposer, critic, DebateConfig{MaxRounds: maxRounds})

	taskCtx := NewTaskContext(fmt.Sprintf("debate_%d", time.Now().UnixNano()), state.UserMessage)
	observations, err := cl.executor.RunWithContext(ctx, ch, sess, target, debatePlan, taskCtx)
	if err != nil {
		return fmt.Errorf("debate execution failed: %w", err)
	}

	synthesis := SynthesizeDebate(observations, proposer, critic)
	obsResult := cl.observer.Run(observations, debatePlan)
	reflection, err := cl.reflector.Run(ctx, ch, target, state, debatePlan, obsResult, 0)
	if err != nil {
		slog.Error("cognitive: debate reflection failed", "err", err)
		return cl.streamFinalAnswer(ctx, ch, target, sess, synthesis)
	}

	finalAnswer := reflection.FinalAnswer
	if finalAnswer == "" {
		finalAnswer = synthesis
	}
	return cl.streamFinalAnswer(ctx, ch, target, sess, finalAnswer)
}

// ────────────────────────────── Delegate to Runtime ──────────────────────────────

// delegateToRuntime hands off simple tasks to the basic Runtime loop.
func (cl *CognitiveLoop) delegateToRuntime(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
) error {
	slog.Info("cognitive: simple task, delegating to runtime", "session", sess.ID)
	simpleStart := time.Now()
	rt := NewAgent(a.deps, &SimpleLoop{}, NewEventBus())
	if cl.approvalFunc != nil {
		rt.SetApprovalFunc(cl.approvalFunc)
	}
	err := rt.HandleMessage(ctx, ch, msg)

	if cl.evoEngine != nil && cl.evoEngine.IsEnabled() {
		durationMs := time.Since(simpleStart).Milliseconds()
		succeeded := err == nil
		cl.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
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

// ────────────────────────────── Checkpoint Resume ──────────────────────────────

// handleResume restores execution from the last checkpoint for a session.
func (cl *CognitiveLoop) handleResume(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
	target channel.MessageTarget,
) error {
	parts := strings.Fields(msg.Text)
	resumeSessionID := sess.ID
	if len(parts) > 1 {
		resumeSessionID = parts[1]
	}

	cp, err := cl.checkpointStore.Load(ctx, resumeSessionID)
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

	if err := ch.Send(ctx, channel.OutboundMessage{
		Channel:   target.Channel,
		ChannelID: target.ChannelID,
		Text: fmt.Sprintf(
			"Resuming from checkpoint: %d/%d subtasks completed (progress: %.0f%%).",
			obsResult.SuccessCount, len(plan.SubTasks), obsResult.OverallProgress*100,
		),
	}); err != nil {
		slog.Warn("cognitive: resume progress send failed", "err", err)
	}

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
	observations, actErr := cl.executor.RunWithContext(ctx, ch, sess, target, &plan, taskCtx)
	if actErr != nil {
		slog.Error("cognitive: resume act failed", "err", actErr)
	}

	newObsResult := cl.observer.Run(observations, &plan)
	resumeState, _ := cl.perceiver.Run(ctx, sess, "Resume execution from checkpoint", msg.UserID)
	if resumeState != nil {
		resumeState.Personality = a.deps.Core.Cfg.Personality
		resumeState.PersistentRules = a.deps.Core.Cfg.PersistentRules
	}

	reflection, err := cl.reflector.Run(ctx, ch, target, resumeState, &plan, newObsResult, 0)
	if err != nil {
		slog.Error("cognitive: resume reflect failed", "err", err)
	}
	if reflection != nil && reflection.FinalAnswer != "" {
		if err := cl.streamFinalAnswer(ctx, ch, target, sess, reflection.FinalAnswer); err != nil {
			slog.Warn("cognitive: resume stream final answer failed", "err", err)
		}
	}
	if err := cl.checkpointStore.Delete(ctx, resumeSessionID); err != nil {
		slog.Warn("cognitive: resume delete checkpoint failed", "session", resumeSessionID, "err", err)
	}

	return nil
}

// ────────────────────────────── Helper Methods ──────────────────────────────

// streamFinalAnswer sends the final answer to the user via streaming.
func (cl *CognitiveLoop) streamFinalAnswer(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	sess *session.Session,
	answer string,
) error {
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "assistant",
		Content:   answer,
		CreatedAt: time.Now(),
	})
	updater, err := ch.SendStreaming(ctx, target)
	if err != nil {
		return ch.Send(ctx, channel.OutboundMessage{
			Channel:   target.Channel,
			ChannelID: target.ChannelID,
			Text:      answer,
		})
	}
	return updater.Finish(answer)
}

// dispatchEvolutionEvents fires self-evolution events based on the completed cognitive cycle.
func (cl *CognitiveLoop) dispatchEvolutionEvents(
	ctx context.Context,
	a *Agent,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	userFeedback float64,
	turnStart time.Time,
) {
	now := time.Now()

	// Collect tool names from observations
	var toolsUsed []string
	if obsResult != nil {
		for _, obs := range obsResult.Observations {
			toolsUsed = append(toolsUsed, obs.ToolName)
		}
	}

	// Dispatch tool execution events (synchronous)
	if obsResult != nil {
		for _, obs := range obsResult.Observations {
			cl.evoEngine.DispatchToolExec(evolution.ToolExecEvent{
				SessionID:  state.SessionID,
				ToolName:   obs.ToolName,
				Succeeded:  !obs.Denied && obs.Error == "",
				Denied:     obs.Denied,
				DurationMs: obs.DurationMs,
				Timestamp:  now,
			})
		}
	}

	// Barrier: ensure all tool event hooks finish before dispatching episode/reflection
	cl.evoEngine.WaitPending()

	// Dispatch reflection event
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

	cl.evoEngine.DispatchReflection(evolution.ReflectionEvent{
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

	// Procedural cortex recording
	if cl.cortex != nil && reflection != nil && reflection.Succeeded {
		var toolsUsedObs []string
		if obsResult != nil {
			for _, obs := range obsResult.Observations {
				toolsUsedObs = append(toolsUsedObs, obs.ToolName)
			}
		}
		if proc := cl.cortex.GetProcedural(); proc != nil {
			go func() {
				if err := proc.RecordStrategy(context.WithoutCancel(ctx), state.Goal.Raw, toolsUsedObs, nil, true, state.SessionID, state.UserID); err != nil {
					slog.Warn("cognitive: procedural record failed", "err", err)
				}
			}()
		}
	}

	// Dispatch episode event
	durationMs := int64(0)
	if !turnStart.IsZero() {
		durationMs = now.Sub(turnStart).Milliseconds()
	}
	totalReward := 0.0
	if reflection != nil && obsResult != nil {
		totalReward = computeSimpleEpisodeReward(reflection, obsResult, durationMs, replanCount, userFeedback)
	}

	cl.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
		SessionID:      state.SessionID,
		EpisodeID:      fmt.Sprintf("ep_%d", now.UnixNano()),
		Goal:           state.Goal.Raw,
		Complexity:     string(state.Goal.Complexity),
		Succeeded:      succeeded,
		TotalReward:    totalReward,
		ToolSequence:   toolsUsed,
		LessonsLearned: lessons,
		ReplanCount:    replanCount,
		DurationMs:     durationMs,
		UserFeedback:   userFeedback,
		Timestamp:      now,
	})
}

	// computeSimpleEpisodeReward computes a basic reward score for the episode.
func computeSimpleEpisodeReward(reflection *Reflection, obsResult *ObservationResult, durationMs int64, replanCount int, userFeedback float64) float64 {
	succeeded := reflection != nil && reflection.Succeeded
	progress := 0.0
	if obsResult != nil && (obsResult.SuccessCount+obsResult.FailureCount) > 0 {
		total := float64(obsResult.SuccessCount + obsResult.FailureCount)
		progress = float64(obsResult.SuccessCount) / total
	}
	reward := 0.0
	if succeeded {
		reward += 1.0
	} else {
		reward -= 1.0
	}
	reward += progress * 0.5
	reward += userFeedback * 0.3
	slog.Debug("cognitive: episode reward computed", "reward", reward, "succeeded", succeeded, "progress", progress, "user_feedback", userFeedback)
	return reward
}
// registerSubtask records a cognitive subtask in the task ledger and returns
// a function that marks it completed. If the ledger is unavailable the
// returned function is a no-op.
func (cl *CognitiveLoop) registerSubtask(ctx context.Context, a *Agent, parentID, title string) func() {
	if a.deps.MultiAgent.TaskLedger == nil || parentID == "" {
		return func() {}
	}
	task := taskledger.Task{
		ID:       fmt.Sprintf("sub_%d", time.Now().UnixNano()),
		ParentID: parentID,
		Kind:     taskledger.TaskKindCognitiveSubtask,
		State:    taskledger.TaskStateRunning,
		Title:    title,
	}
	if err := a.deps.MultiAgent.TaskLedger.Register(ctx, task); err != nil {
		slog.Warn("cognitive: failed to register subtask", "title", title, "err", err)
		return func() {}
	}
	return func() {
		task.State = taskledger.TaskStateCompleted
		now := time.Now()
		task.CompletedAt = &now
		if err := a.deps.MultiAgent.TaskLedger.Update(ctx, task); err != nil {
			slog.Warn("cognitive: failed to complete subtask", "title", title, "err", err)
		}
	}
}

// registerParentTask creates a parent task in the ledger for cognitive subtasks.
func (cl *CognitiveLoop) registerParentTask(ctx context.Context, a *Agent, msg channel.InboundMessage) string {
	if a.deps.MultiAgent.TaskLedger == nil {
		return ""
	}
	parentTaskID := fmt.Sprintf("cog_%d", time.Now().UnixNano())
	task := taskledger.Task{
		ID:    parentTaskID,
		Kind:  taskledger.TaskKindUserRequest,
		State: taskledger.TaskStateRunning,
		Title: util.TruncateStr(msg.Text, 100),
	}
	if err := a.deps.MultiAgent.TaskLedger.Register(ctx, task); err != nil {
		slog.Warn("cognitive: failed to register parent task", "err", err)
		return ""
	}
	return parentTaskID
}

func (cl *CognitiveLoop) saveCheckpoint(ctx context.Context, sessionID string, plan *TaskPlan, obsResult *ObservationResult, attempt int) {
	obsJSON, _ := json.Marshal(obsResult)
	planJSON, _ := json.Marshal(plan)
	cp := &TaskCheckpoint{
		ID:               fmt.Sprintf("cp-%s-%d", sessionID, attempt),
		SessionID:        sessionID,
		SubTaskIndex:     len(obsResult.Observations),
		ObservationsJSON: string(obsJSON),
		PlanJSON:         string(planJSON),
	}
	if err := cl.checkpointStore.Save(ctx, cp); err != nil {
		slog.Warn("cognitive: save checkpoint failed", "session", sessionID, "err", err)
	}
}

// resolveConfidenceThreshold determines the effective confidence threshold for replan decisions.
func (cl *CognitiveLoop) resolveConfidenceThreshold(cogCfg config.CognitiveConfig, a *Agent) float64 {
	confidenceThreshold := cogCfg.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.6
	}
	if cl.evoEngine != nil && cl.evoEngine.IsEnabled() {
		if so := cl.evoEngine.StrategyOptimizerHook(); so != nil && so.IsHardControlEnabled() {
			if evoThreshold := so.GetReplanThreshold(); evoThreshold > 0 {
				confidenceThreshold = evoThreshold
				slog.Debug("cognitive: using evolution replan threshold",
					"threshold", evoThreshold)
			}
		}
	}
	return confidenceThreshold
}

func (cl *CognitiveLoop) overrideReflectionWithAssertions(reflection *Reflection, obsResult *ObservationResult) {
	passed := 0
	for _, a := range obsResult.Assertions {
		if a.Passed {
			passed++
		}
	}
	total := len(obsResult.Assertions)
	if total > 0 {
		assertRate := float64(passed) / float64(total)
		if assertRate >= 0.85 {
			slog.Info("reflect: overriding succeeded=false via high assertion pass rate",
				"assertion_rate", assertRate,
				"passed", passed,
				"total", total,
				"llm_confidence", reflection.OverallConfidence,
			)
			reflection.Succeeded = true
		}
	}
}

// publishPhaseEvent publishes a PhaseChanged event to the agent's event bus.
func (cl *CognitiveLoop) publishPhaseEvent(a *Agent, sessionID, phase string) {
	a.eventBus.Publish(PhaseChanged{
		SessionID: sessionID,
		Phase:     phase,
		IsStart:   !strings.HasSuffix(phase, "_COMPLETE") && !strings.HasSuffix(phase, "_START"),
	})
}

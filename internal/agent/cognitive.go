package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/cortex"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/observability"
	"github.com/Forest-Isle/IronClaw/internal/rl"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const MaxReplanAttempts = 2

var errResumeHandled = fmt.Errorf("resume handled")

// RunResult is the output of a cognitive agent execution via Run().
// Deprecated: Use CognitiveAgentV2 which returns LoopResult directly.
type RunResult struct {
	Output     string
	ToolCalls  []ToolResult
	TurnCount  int
	Assertions []AssertionResult
	Learnings  []string
}

// CognitiveAgent implements the structured PERCEIVE→PLAN→ACT→OBSERVE→REFLECT loop.
type CognitiveAgent struct {
	runtime             *Runtime
	perceiver           *Perceiver
	planner             *Planner
	executor            *Executor
	observer            *Observer
	reflector           *Reflector
	sessions            *session.Manager
	db                  *store.DB
	cfg                 config.AgentConfig
	llmCfg              config.LLMConfig
	debateCfg           config.DebateSettings
	memStore            memory.Store
	skillMgr            *skill.Manager
	agentMgr            *AgentManager
	orchestrator        *AgentOrchestrator
	teamManager         *TeamManager
	entityExtractor     *graph.LLMEntityExtractor
	rlPolicy            RLPolicy          // RL policy interface (nil if disabled)
	rlTrainer           RLTrainer         // RL trainer interface (nil if disabled)
	evoEngine           *evolution.Engine // self-evolution event dispatcher (nil if disabled)
	hookMgr             *hook.Manager
	permEngine          *tool.PermissionEngine
	checkpointStore     CheckpointStore
	contextManager      ContextManager
	taskLedger          taskledger.TaskLedger
	dashEmitter         DashboardEmitter
	planMode            *PlanMode
	observationCallback func(result *ObservationResult)
	replayRecorder      *ReplayRecorder
	selfHealEngine      *SelfHealEngine
	treePlanner         *StrategicTreePlanner
	mctsPlanner         *MCTSPlanner
	codebaseIndex       *CodebaseIndex
	cortex              *cortex.UnifiedRetriever
}

// CognitiveAgentOptions bundles all optional dependencies for the cognitive agent.
// Fields left nil are silently skipped (feature not enabled).
type CognitiveAgentOptions struct {
	MemoryStore         memory.Store
	FactExtractor       *memory.LLMFactExtractor
	LifecycleManager    *memory.LifecycleManager
	CodebaseIndex       *CodebaseIndex
	KnowledgeSearcher   knowledge.Searcher
	KnowledgeGraph      graph.Graph
	EntityExtractor     *graph.LLMEntityExtractor
	SelfHealEngine      *SelfHealEngine
	TreePlanner         *StrategicTreePlanner
	MCTSPlanner         *MCTSPlanner
	HookManager         *hook.Manager
	PermissionEngine    *tool.PermissionEngine
	InterceptorChain    *tool.InterceptorChain
	SkillManager        *skill.Manager
	AgentManager        *AgentManager
	Orchestrator        *AgentOrchestrator
	TeamManager         *TeamManager
	EvolutionEngine     *evolution.Engine
	RLPolicy            RLPolicy
	RLTrainer           RLTrainer
	MemoryNotifyFunc    MemoryNotifyFunc
	CheckpointStore     CheckpointStore
	ContextManager      ContextManager
	TaskLedger          taskledger.TaskLedger
	DashboardEmitter    DashboardEmitter
	ReplayRecorder      *ReplayRecorder
	ObservationCallback func(result *ObservationResult)
	ApprovalFunc        ApprovalFunc
	PlanMode            *PlanMode
	DebateConfig        config.DebateSettings
	CortexRetriever     *cortex.UnifiedRetriever
}

// NewCognitiveAgent creates a CognitiveAgent, wiring all phases together.
func NewCognitiveAgent(
	provider Provider,
	tools *tool.Registry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
	opts *CognitiveAgentOptions,
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
	ca.applyOptions(opts)

	return ca
}

func (ca *CognitiveAgent) applyOptions(opts *CognitiveAgentOptions) {
	if opts == nil {
		return
	}
	if opts.MemoryStore != nil {
		ca.SetMemoryStore(opts.MemoryStore)
	}
	if opts.FactExtractor != nil {
		ca.SetFactExtractor(opts.FactExtractor)
	}
	if opts.LifecycleManager != nil {
		ca.SetLifecycleManager(opts.LifecycleManager)
	}
	if opts.CodebaseIndex != nil {
		ca.SetCodebaseIndex(opts.CodebaseIndex)
	}
	if opts.KnowledgeSearcher != nil {
		ca.SetKnowledgeSearcher(opts.KnowledgeSearcher)
	}
	if opts.KnowledgeGraph != nil {
		ca.SetKnowledgeGraph(opts.KnowledgeGraph)
	}
	if opts.EntityExtractor != nil {
		ca.SetEntityExtractor(opts.EntityExtractor)
	}
	if opts.SelfHealEngine != nil {
		ca.SetSelfHealEngine(opts.SelfHealEngine)
	}
	if opts.TreePlanner != nil {
		ca.SetTreePlanner(opts.TreePlanner)
	}
	if opts.MCTSPlanner != nil {
		ca.SetMCTSPlanner(opts.MCTSPlanner)
	}
	if opts.HookManager != nil {
		ca.SetHookManager(opts.HookManager)
	}
	if opts.PermissionEngine != nil {
		ca.SetPermissionEngine(opts.PermissionEngine)
	}
	if opts.InterceptorChain != nil {
		ca.SetInterceptorChain(opts.InterceptorChain)
	}
	if opts.SkillManager != nil {
		ca.SetSkillManager(opts.SkillManager)
	}
	if opts.AgentManager != nil {
		ca.SetAgentManager(opts.AgentManager)
	}
	if opts.Orchestrator != nil {
		ca.SetOrchestrator(opts.Orchestrator)
	}
	if opts.TeamManager != nil {
		ca.SetTeamManager(opts.TeamManager)
	}
	if opts.EvolutionEngine != nil {
		ca.SetEvolutionEngine(opts.EvolutionEngine)
	}
	if opts.RLPolicy != nil {
		ca.SetRLPolicy(opts.RLPolicy)
	}
	if opts.RLTrainer != nil {
		ca.SetRLTrainer(opts.RLTrainer)
	}
	if opts.MemoryNotifyFunc != nil {
		ca.SetMemoryNotifyFunc(opts.MemoryNotifyFunc)
	}
	if opts.CheckpointStore != nil {
		ca.SetCheckpointStore(opts.CheckpointStore)
	}
	if opts.ContextManager != nil {
		ca.SetContextManager(opts.ContextManager)
	}
	if opts.TaskLedger != nil {
		ca.SetTaskLedger(opts.TaskLedger)
	}
	if opts.DashboardEmitter != nil {
		ca.SetDashboardEmitter(opts.DashboardEmitter)
	}
	if opts.ReplayRecorder != nil {
		ca.SetReplayRecorder(opts.ReplayRecorder)
	}
	if opts.ObservationCallback != nil {
		ca.SetObservationCallback(opts.ObservationCallback)
	}
	if opts.ApprovalFunc != nil {
		ca.SetApprovalFunc(opts.ApprovalFunc)
	}
	if opts.PlanMode != nil {
		ca.SetPlanMode(opts.PlanMode)
	}
	if opts.CortexRetriever != nil {
		ca.SetCortexRetriever(opts.CortexRetriever)
	}
	ca.SetDebateConfig(opts.DebateConfig)
}

// MemoryStore returns the active memory store, or nil when memory is disabled.
// Used by the eval harness to inject test fixtures directly into the store the
// agent reads from during PERCEIVE.
func (ca *CognitiveAgent) MemoryStore() memory.Store { return ca.memStore }

// SetMemoryStore injects the memory.md store into all phases that need it.
func (ca *CognitiveAgent) SetMemoryStore(s memory.Store) {
	ca.memStore = s
	ca.runtime.SetMemoryStore(s)
	// Preserve existing searcher, graph, scanner, git provider, budget, and RL policy when rebuilding the perceiver.
	oldSearcher := ca.perceiver.searcher
	oldGraph := ca.perceiver.graph
	oldCortex := ca.perceiver.cortex
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
	if oldCortex != nil {
		ca.perceiver.SetCortexRetriever(oldCortex)
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

// SetCortexRetriever injects the unified cortex retriever into the perceiver and agent.
func (ca *CognitiveAgent) SetCortexRetriever(ur *cortex.UnifiedRetriever) {
	ca.cortex = ur
	ca.perceiver.SetCortexRetriever(ur)
}

// SetCodebaseIndex injects a semantic codebase index into the cognitive agent.
func (ca *CognitiveAgent) SetCodebaseIndex(index *CodebaseIndex) {
	ca.codebaseIndex = index
}

func (ca *CognitiveAgent) SetSelfHealEngine(eng *SelfHealEngine) {
	ca.selfHealEngine = eng
}

func (ca *CognitiveAgent) SetTreePlanner(tp *StrategicTreePlanner) {
	ca.treePlanner = tp
}

// SetMCTSPlanner injects a Monte Carlo Tree Search planner.
// When set, MCTS takes priority over the simpler tree planner during PLAN.
func (ca *CognitiveAgent) SetMCTSPlanner(mp *MCTSPlanner) {
	ca.mctsPlanner = mp
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

// SetPlanMode injects a PlanMode instance into the cognitive agent.
// When set, the executor enforces plan→approve→execute flow for write tools.
func (ca *CognitiveAgent) SetPlanMode(pm *PlanMode) {
	ca.planMode = pm
	if ca.executor != nil {
		ca.executor.SetPlanMode(pm)
	}
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

// SetInterceptorChain injects an interceptor chain into the cognitive agent's executor and inner runtime.
func (ca *CognitiveAgent) SetInterceptorChain(chain *tool.InterceptorChain) {
	ca.executor.SetInterceptorChain(chain)
	ca.runtime.SetInterceptorChain(chain)
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

// SetModel updates the default model on the cognitive agent, its inner runtime,
// and the planner/reflector components.
func (ca *CognitiveAgent) SetModel(model string) {
	ca.llmCfg.Model = model
	ca.runtime.SetModel(model)
	if ca.planner != nil {
		ca.planner.llmModel = model
	}
	if ca.reflector != nil {
		ca.reflector.llmModel = model
	}
}

// SetDebateConfig sets the debate configuration from the agents config.
func (ca *CognitiveAgent) SetDebateConfig(cfg config.DebateSettings) {
	ca.debateCfg = cfg
}

func (ca *CognitiveAgent) BuildNodeDeps(ch channel.Channel) NodeDeps {
	return NodeDeps{
		Perceiver: ca.perceiver,
		Planner:   ca.planner,
		Executor:  ca.executor,
		Observer:  ca.observer,
		Reflector: ca.reflector,
		Sessions:  ca.sessions,
		Channel:   ch,
	}
}

// SetOrchestrator injects an agent orchestrator into the cognitive agent.
func (ca *CognitiveAgent) SetOrchestrator(o *AgentOrchestrator) {
	ca.orchestrator = o
}

// SetTeamManager injects a team manager for multi-agent team coordination.
func (ca *CognitiveAgent) SetTeamManager(tm *TeamManager) {
	ca.teamManager = tm
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

// SetDashboardEmitter injects a dashboard event emitter into the cognitive agent and its inner runtime.
func (ca *CognitiveAgent) SetDashboardEmitter(e DashboardEmitter) {
	ca.dashEmitter = e
	ca.executor.SetDashboardEmitter(e)
	ca.runtime.SetDashboardEmitter(e)
}

// SetReplayRecorder injects a replay recorder into the cognitive agent and delegates.
func (ca *CognitiveAgent) SetReplayRecorder(rr *ReplayRecorder) {
	ca.replayRecorder = rr
	ca.runtime.SetReplayRecorder(rr)
	if ca.executor != nil {
		ca.executor.SetReplayRecorder(rr)
	}
}

// SetObservationCallback registers a function that is called after each OBSERVE
// phase completes. Used by the eval harness to capture assertion statistics.
func (ca *CognitiveAgent) SetObservationCallback(fn func(result *ObservationResult)) {
	ca.observationCallback = fn
}

func (ca *CognitiveAgent) setupCognitiveSession(
	ctx context.Context,
	ch channel.Channel,
	msg channel.InboundMessage,
) (*session.Session, channel.MessageTarget, string, func(), error) {
	sess, err := ca.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return nil, channel.MessageTarget{}, "", func() {}, fmt.Errorf("get session: %w", err)
	}
	if ca.dashEmitter != nil {
		ca.dashEmitter.EmitSessionStart(sess.ID, msg.Channel)
	}

	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	if ca.checkpointStore != nil && strings.HasPrefix(strings.TrimSpace(msg.Text), "/resume") {
		if err := ca.handleResume(ctx, ch, msg, sess); err != nil {
			return nil, target, "", func() {}, err
		}
		return nil, target, "", func() {}, errResumeHandled
	}

	var parentTaskID string
	cleanup := func() {}
	if ca.taskLedger != nil {
		parentTaskID = fmt.Sprintf("cog_%d", time.Now().UnixNano())
		task := taskledger.Task{
			ID:    parentTaskID,
			Kind:  taskledger.TaskKindUserRequest,
			State: taskledger.TaskStateRunning,
			Title: util.TruncateStr(msg.Text, 100),
		}
		if err := ca.taskLedger.Register(ctx, task); err != nil {
			slog.Warn("cognitive: failed to register task", "err", err)
			parentTaskID = ""
		} else {
			cleanup = func() {
				task.State = taskledger.TaskStateCompleted
				now := time.Now()
				task.CompletedAt = &now
				if err := ca.taskLedger.Update(ctx, task); err != nil {
					slog.Warn("cognitive: failed to complete task", "err", err)
				}
			}
		}
	}

	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	if ca.contextManager != nil {
		if _, err := ca.contextManager.Compress(ctx, sess, ""); err != nil {
			slog.Warn("cognitive: context compression failed", "session", sess.ID, "err", err)
		}
	} else {
		if err := CompactHistory(ctx, ca.planner.provider, sess, ca.llmCfg.Model); err != nil {
			slog.Warn("cognitive: history compaction failed", "session", sess.ID, "err", err)
		}
	}

	return sess, target, parentTaskID, cleanup, nil
}

func (ca *CognitiveAgent) runPerceivePhase(
	ctx context.Context,
	sess *session.Session,
	msg channel.InboundMessage,
	parentTaskID string,
) (*CognitiveState, error) {
	if ca.dashEmitter != nil {
		ca.dashEmitter.EmitPhaseStart(sess.ID, "PERCEIVE")
	}
	perceiveStart := time.Now()
	donePerceive := ca.registerSubtask(ctx, parentTaskID, "PERCEIVE phase")
	state, err := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
	donePerceive()
	if ca.dashEmitter != nil {
		ca.dashEmitter.EmitPhaseEnd(sess.ID, "PERCEIVE", time.Since(perceiveStart).Milliseconds())
	}
	observability.CognitivePhasesDuration.Record(ctx, time.Since(perceiveStart).Milliseconds(),
		metric.WithAttributes(attribute.String("phase", "perceive")))
	if err != nil {
		return nil, fmt.Errorf("perceive: %w", err)
	}

	if ca.codebaseIndex != nil && ca.codebaseIndex.IsAvailable() {
		if results, searchErr := ca.codebaseIndex.Search(ctx, msg.Text, 3); searchErr != nil {
			slog.Warn("cognitive: semantic code search failed", "session", sess.ID, "err", searchErr)
		} else if len(results) > 0 {
			for _, chunk := range results {
				state.KnowledgeContext = append(state.KnowledgeContext,
					fmt.Sprintf("Code match %s:%d-%d (score %.3f)\n%s",
						chunk.FilePath, chunk.StartLine, chunk.EndLine, chunk.Score, chunk.Content))
			}
		}
	}

	if ca.skillMgr != nil {
		state.Skills = ca.skillMgr.BuildPromptSection(msg.Text)
	}
	if ca.agentMgr != nil {
		state.Agents = ca.agentMgr.BuildPromptSection()
	}
	state.Personality = ca.cfg.Personality
	state.PersistentRules = ca.cfg.PersistentRules

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

	return state, nil
}

func (ca *CognitiveAgent) delegateToRuntime(
	ctx context.Context,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
) error {
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

func (ca *CognitiveAgent) runPrePlanSearch(
	ctx context.Context,
	sess *session.Session,
	state *CognitiveState,
) (*TaskPlan, []PlanCandidate, bool, *StrategicTreePlanner) {
	var (
		plan           *TaskPlan
		mctsCandidates []PlanCandidate
		mctsActive     bool
	)
	treePlanner := ca.treePlanner
	mctsPlanner := ca.mctsPlanner

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

func (ca *CognitiveAgent) runCognitiveLoop(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	state *CognitiveState,
	plan *TaskPlan,
	mctsCandidates []PlanCandidate,
	mctsActive bool,
	treePlanner *StrategicTreePlanner,
	parentTaskID string,
) (string, *TaskPlan, *ObservationResult, *Reflection, *rl.PlanStrategyAction, *EpisodeCollector, rl.ReplanActionType, *rl.RLState, error) {
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

	var (
		finalAnswer      string
		obsResult        *ObservationResult
		reflection       *Reflection
		ppoStrategy      *rl.PlanStrategyAction
		dqnReplanAction  rl.ReplanActionType
		episodeCollector *EpisodeCollector
		rlState          *rl.RLState
	)
	rlEnabled := ca.rlPolicy != nil && ca.rlPolicy.IsEnabled()
	if rlEnabled {
		rlState = buildInitialRLState(state, len(ca.executor.tools.All()))
		episodeCollector = &EpisodeCollector{State: rlState, StartTime: time.Now()}
	}

	for attempt := 0; attempt <= maxReplans; attempt++ {
		if attempt > 0 {
			slog.Info("cognitive: replanning", "attempt", attempt, "max", maxReplans, "session", sess.ID)
			if ca.dashEmitter != nil {
				reason := "low_confidence"
				if reflection != nil && reflection.SuggestedAdjustment != "" {
					reason = "adjustment: " + util.TruncateStr(reflection.SuggestedAdjustment, 100)
				}
				ca.dashEmitter.EmitReplanStart(sess.ID, attempt, reason)
			}
		}

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
		} else {
			var err error
			if ca.dashEmitter != nil {
				ca.dashEmitter.EmitPhaseStart(sess.ID, "PLAN")
			}
			planStart := time.Now()
			donePlan := ca.registerSubtask(ctx, parentTaskID, fmt.Sprintf("PLAN phase (attempt %d)", attempt))
			if treePlanner != nil {
				plan = treePlanner.Select()
				if plan == nil {
					failureSummary := buildTreeFailureSummary(reflection, obsResult)
					if expandErr := treePlanner.Expand(ctx, failureSummary); expandErr != nil {
						donePlan()
						if ca.dashEmitter != nil {
							ca.dashEmitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
						}
						observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
							metric.WithAttributes(attribute.String("phase", "plan")))
						return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState,
							fmt.Errorf("tree-planner expand: %w", expandErr)
					}
					plan = treePlanner.Select()
					if plan == nil {
						donePlan()
						if ca.dashEmitter != nil {
							ca.dashEmitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
						}
						observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
							metric.WithAttributes(attribute.String("phase", "plan")))
						return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState,
							fmt.Errorf("tree-planner select: no plan available after expand")
					}
				}
				slog.Info("tree-planner: selected plan",
					"strategy", treePlanner.currentNode.Candidates[treePlanner.currentIdx].Strategy,
					"score", treePlanner.currentNode.Candidates[treePlanner.currentIdx].LLMScore,
					"depth", treePlanner.currentNode.Depth)
			} else {
				plan, err = ca.planner.Run(ctx, state)
			}
			donePlan()
			if ca.dashEmitter != nil {
				ca.dashEmitter.EmitPhaseEnd(sess.ID, "PLAN", time.Since(planStart).Milliseconds())
			}
			observability.CognitivePhasesDuration.Record(ctx, time.Since(planStart).Milliseconds(),
				metric.WithAttributes(attribute.String("phase", "plan")))
			if err != nil {
				slog.Error("cognitive: plan failed", "err", err)
				return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState,
					fmt.Errorf("plan: %w", err)
			}
		}

		plan.ReplanCount = attempt
		if ca.dashEmitter != nil {
			complexity := string(state.Goal.Complexity)
			ca.dashEmitter.EmitPlanGenerated(sess.ID, len(plan.SubTasks), complexity, plan.DirectReply != "")
		}

		if rlEnabled && rlState != nil {
			ppoStrategy = ca.rlPolicy.SelectPlanStrategy(rlState)
			if ppoStrategy != nil {
				plan.OverallConfidence = clampRL(plan.OverallConfidence+ppoStrategy.ConfidenceAdj, 0, 1)
			}
			updateRLStateWithPlan(rlState, plan)
		}

		if plan.DirectReply != "" {
			finalAnswer = plan.DirectReply
			slog.Info("cognitive: direct reply from plan phase", "session", sess.ID)
			if err := ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
				slog.Warn("cognitive: stream direct reply failed", "err", err)
			}
			return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
		}

		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "ACT")
		}
		actStart := time.Now()
		doneAct := ca.registerSubtask(ctx, parentTaskID, "ACT phase")
		taskCtx := NewTaskContext(fmt.Sprintf("task_%d", time.Now().UnixNano()), state.UserMessage)
		observations, actErr := ca.executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, episodeCollector)
		doneAct()
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "ACT", time.Since(actStart).Milliseconds())
		}
		observability.CognitivePhasesDuration.Record(ctx, time.Since(actStart).Milliseconds(),
			metric.WithAttributes(attribute.String("phase", "act")))
		if actErr != nil {
			slog.Error("cognitive: act failed", "err", actErr)
			return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState,
				fmt.Errorf("act: %w", actErr)
		}

		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "OBSERVE")
		}
		observeStart := time.Now()
		obsResult = ca.observer.Run(observations, plan)
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "OBSERVE", time.Since(observeStart).Milliseconds())
		}
		observability.CognitivePhasesDuration.Record(ctx, time.Since(observeStart).Milliseconds(),
			metric.WithAttributes(attribute.String("phase", "observe")))
		slog.Info("cognitive: observe complete",
			"success", obsResult.SuccessCount,
			"failure", obsResult.FailureCount,
			"progress", fmt.Sprintf("%.0f%%", obsResult.OverallProgress*100),
		)
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitObservationResult(sess.ID, obsResult.SuccessCount, obsResult.FailureCount,
				obsResult.SuccessCount+obsResult.FailureCount, obsResult.OverallProgress)
		}
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
			if err := ca.checkpointStore.Save(ctx, cp); err != nil {
				slog.Warn("cognitive: save checkpoint failed", "session", sess.ID, "err", err)
			}
		}

		if rlEnabled && rlState != nil {
			updateRLStateWithObservation(rlState, obsResult)
		}

		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseStart(sess.ID, "REFLECT")
		}
		reflectStart := time.Now()
		doneReflect := ca.registerSubtask(ctx, parentTaskID, "REFLECT phase")
		var err error
		reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult, attempt)
		doneReflect()
		if ca.dashEmitter != nil {
			ca.dashEmitter.EmitPhaseEnd(sess.ID, "REFLECT", time.Since(reflectStart).Milliseconds())
		}
		observability.CognitivePhasesDuration.Record(ctx, time.Since(reflectStart).Milliseconds(),
			metric.WithAttributes(attribute.String("phase", "reflect")))
		if err != nil {
			slog.Error("cognitive: reflect failed", "err", err)
			return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState,
				fmt.Errorf("reflect: %w", err)
		}

		if reflection != nil && !reflection.Succeeded && obsResult != nil {
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

		finalAnswer = reflection.FinalAnswer
		if finalAnswer == "" {
			finalAnswer = "Task completed."
		}
		if err := ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
			slog.Warn("cognitive: stream final answer failed", "err", err)
		}

		if rlEnabled && rlState != nil && reflection != nil {
			rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
			if reflection.NeedsReplan {
				dqnReplanAction = ca.rlPolicy.SelectReplanAction(rlState)
				dqnWeight := ca.cfg.RL.DQN.ReplanWeight
				adjConfidence, shouldAbort := applyDQNReplanAdjustment(reflection.OverallConfidence, dqnReplanAction, dqnWeight)
				slog.Info("cognitive: DQN replan adjustment",
					"action", dqnReplanAction.String(),
					"original_confidence", reflection.OverallConfidence,
					"adjusted_confidence", adjConfidence,
					"should_abort", shouldAbort,
				)
				if shouldAbort {
					slog.Info("cognitive: DQN recommends abort", "session", sess.ID)
					return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
				}
				reflection.OverallConfidence = adjConfidence
			}
		}

		if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
			decision, _ := ca.reflector.RequestReplanApproval(ctx, ch, target, reflection)
			switch decision {
			case ReplanAbort:
				slog.Info("cognitive: replan aborted by user", "session", sess.ID)
				return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
			case ReplanContinue:
				slog.Info("cognitive: replan skipped (continue)", "session", sess.ID)
				return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
			case ReplanAdjust:
				slog.Info("cognitive: adjusting and replanning", "session", sess.ID)
				if reflection.SuggestedAdjustment != "" {
					state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
				}
				continue
			}
		}

		return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
	}

	return finalAnswer, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, nil
}

func (ca *CognitiveAgent) finalizeCognitiveSession(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	msg channel.InboundMessage,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	ppoStrategy *rl.PlanStrategyAction,
	episodeCollector *EpisodeCollector,
	dqnReplanAction rl.ReplanActionType,
	rlState *rl.RLState,
	sessionStart time.Time,
	cognitiveTurnStart time.Time,
) error {
	var finalizeErr error
	if ca.checkpointStore != nil {
		if err := ca.checkpointStore.Delete(ctx, sess.ID); err != nil {
			slog.Warn("cognitive: delete checkpoint failed", "session", sess.ID, "err", err)
			finalizeErr = fmt.Errorf("delete checkpoint: %w", err)
		}
	}

	var userFeedback float64
	rlEnabled := ca.rlPolicy != nil && ca.rlPolicy.IsEnabled()
	_ = rlState
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
		ca.recordRLEpisode(ctx, state, plan, obsResult, reflection, ppoStrategy, episodeCollector, userFeedback, dqnReplanAction)
	}

	if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
		if state.ModelOverride != "" {
			succeeded := reflection != nil && reflection.Succeeded
			ca.evoEngine.Router().RecordOutcome(string(state.Goal.Complexity), succeeded)
		}
		ca.dispatchEvolutionEvents(ctx, state, plan, obsResult, reflection, userFeedback, cognitiveTurnStart)
	}
	if ca.evoEngine != nil {
		ca.evoEngine.WaitPending()
	}

	if err := ca.sessions.Persist(ctx, sess); err != nil {
		slog.Error("cognitive: failed to persist session", "err", err)
		if finalizeErr == nil {
			finalizeErr = fmt.Errorf("persist session: %w", err)
		} else {
			finalizeErr = fmt.Errorf("%v; persist session: %w", finalizeErr, err)
		}
	}
	if ca.memStore != nil {
		if err := ca.memStore.Save(ctx, memory.Entry{
			ID:        fmt.Sprintf("conv_%d", time.Now().UnixNano()),
			Scope:     memory.ScopeSession,
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("cognitive: failed to save memory.md", "err", err)
			if finalizeErr == nil {
				finalizeErr = fmt.Errorf("save memory: %w", err)
			} else {
				finalizeErr = fmt.Errorf("%v; save memory: %w", finalizeErr, err)
			}
		}
	}

	if ca.dashEmitter != nil {
		succeeded := reflection != nil && reflection.Succeeded
		ca.dashEmitter.EmitSessionEnd(sess.ID, succeeded, time.Since(sessionStart).Milliseconds())
	}

	return finalizeErr
}

// HandleMessage processes an inbound message through the cognitive loop.
func (ca *CognitiveAgent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	sessionStart := time.Now()
	sess, target, parentTaskID, cleanup, err := ca.setupCognitiveSession(ctx, ch, msg)
	if err != nil {
		if err == errResumeHandled {
			return nil
		}
		return err
	}
	defer cleanup()

	state, err := ca.runPerceivePhase(ctx, sess, msg, parentTaskID)
	if err != nil {
		return err
	}
	if state.Goal.Complexity == ComplexitySimple {
		return ca.delegateToRuntime(ctx, ch, msg, sess)
	}
	if ca.shouldDebate(state) {
		return ca.handleDebate(ctx, ch, msg, sess, state, target)
	}
	if ca.cfg.Cognitive.StreamingEnabled {
		return ca.handleStreaming(ctx, ch, msg, sess, target, state, parentTaskID)
	}

	cognitiveTurnStart := time.Now()
	plan, mctsCandidates, mctsActive, treePlanner := ca.runPrePlanSearch(ctx, sess, state)
	_, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, loopErr := ca.runCognitiveLoop(
		ctx, ch, sess, target, state, plan, mctsCandidates, mctsActive, treePlanner, parentTaskID,
	)
	finalizeErr := ca.finalizeCognitiveSession(
		ctx, ch, sess, target, msg, state, plan, obsResult, reflection,
		ppoStrategy, episodeCollector, dqnReplanAction, rlState, sessionStart, cognitiveTurnStart,
	)
	if loopErr != nil {
		return loopErr
	}
	return finalizeErr
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
		if err := ca.streamFinalAnswer(ctx, ch, target, sess, reflection.FinalAnswer); err != nil {
			slog.Warn("cognitive: resume stream final answer failed", "err", err)
		}
	}

	if err := ca.checkpointStore.Delete(ctx, resumeSessionID); err != nil {
		slog.Warn("cognitive: resume delete checkpoint failed", "session", resumeSessionID, "err", err)
	}

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
	ctx context.Context,
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

	// Barrier: ensure all tool event hooks have finished before dispatching
	// episode/reflection events that may read per-tool buffers.
	ca.evoEngine.WaitPending()

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

	if ca.cortex != nil && reflection != nil && reflection.Succeeded {
		taskPattern := state.Goal.Raw
		var toolsUsed []string
		if obsResult != nil {
			for _, obs := range obsResult.Observations {
				toolsUsed = append(toolsUsed, obs.ToolName)
			}
		}
		if proc := ca.cortex.GetProcedural(); proc != nil {
			go func() {
				if err := proc.RecordStrategy(context.WithoutCancel(ctx), taskPattern, toolsUsed, nil, true, state.SessionID, state.UserID); err != nil {
					slog.Warn("cognitive: procedural record failed", "err", err)
				}
			}()
		}
	}

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
	ctx context.Context,
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
		bgCtx := context.WithoutCancel(ctx)

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

// --- Deprecation wrapper: CognitiveAgent delegates to CognitiveAgentV2 ---

// projectContextScannerAdapter wraps ProjectContextScanner as a ContextScanner.
type projectContextScannerAdapter struct {
	scanner *ProjectContextScanner
}

func (a *projectContextScannerAdapter) Name() string { return "project" }

func (a *projectContextScannerAdapter) Scan(ctx context.Context) (*ContextFragment, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	result := a.scanner.Scan(cwd)
	if result == nil {
		return nil, nil
	}
	return &ContextFragment{Source: "project", Content: result.RawContent, Priority: 1}, nil
}

// gitContextProviderAdapter wraps GitContextProvider as a ContextScanner.
type gitContextProviderAdapter struct {
	provider *GitContextProvider
}

func (a *gitContextProviderAdapter) Name() string { return "git" }

func (a *gitContextProviderAdapter) Scan(ctx context.Context) (*ContextFragment, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	result := a.provider.Collect(cwd)
	if result == nil {
		return nil, nil
	}
	return &ContextFragment{Source: "git", Content: result.RawContent, Priority: 2}, nil
}

// toV2 creates a CognitiveAgentV2 from the deprecated CognitiveAgent, wiring all
// available optional subsystems into the new single-loop architecture.
func (ca *CognitiveAgent) toV2() *CognitiveAgentV2 {
	v2 := NewCognitiveAgentV2(
		ca.runtime.provider,
		ca.executor.tools,
		ca.sessions,
		ca.db,
		ca.cfg,
		ca.llmCfg,
	)

	// Build ContextBuilder from old perceiver's scanners.
	cb := NewContextBuilder()
	if ca.perceiver != nil {
		if ca.perceiver.scanner != nil {
			cb.AddScanner(&projectContextScannerAdapter{scanner: ca.perceiver.scanner})
		}
		if ca.perceiver.gitProvider != nil {
			cb.AddScanner(&gitContextProviderAdapter{provider: ca.perceiver.gitProvider})
		}
	}
	v2.SetContextBuilder(cb)

	// Build ToolMiddlewareChain with available security/filtering subsystems.
	var mws []ToolMiddleware
	if ca.hookMgr != nil {
		mws = append(mws, NewHookMiddleware(ca.hookMgr))
	}
	if ca.permEngine != nil {
		mws = append(mws, NewPermissionMiddleware(ca.permEngine, ca.executor.approvalFunc))
	}
	if ca.executor != nil && ca.executor.interceptorChain != nil {
		mws = append(mws, NewSandboxMiddleware(ca.executor.interceptorChain))
	}
	if len(mws) > 0 {
		tm := NewToolMiddlewareChain(mws...)
		tm.SetCoreExecutor(defaultToolExecutor(ca.executor.tools))
		v2.SetToolMiddleware(tm)
	}

	// Build LoopHookChain with available lifecycle subsystems.
	var hooks []LoopHook
	if ca.checkpointStore != nil {
		hooks = append(hooks, NewCheckpointHook(ca.checkpointStore, ca.db))
	}
	if ca.contextManager != nil {
		hooks = append(hooks, NewCompressionHook(ca.contextManager, 0.85))
	}
	if ca.planMode != nil {
		hooks = append(hooks, NewPlanModeHook(ca.planMode, ca.executor.approvalFunc))
	}
	if ca.evoEngine != nil {
		hooks = append(hooks, NewEvolutionHook(ca.evoEngine, ca.dashEmitter))
	}
	if len(hooks) > 0 {
		v2.SetLoopHooks(NewLoopHookChain(hooks...))
	}

	// Set dashboard emitter.
	if ca.dashEmitter != nil {
		v2.SetDashboardEmitter(ca.dashEmitter)
	}

	return v2
}

// Run executes the cognitive loop.
//
// Deprecated: CognitiveAgent is replaced by CognitiveAgentV2 (single-loop
// architecture). Run() now delegates to CognitiveAgentV2 internally.
func (ca *CognitiveAgent) Run(
	ctx context.Context,
	sessionID string,
	userMessage string,
	extraContext string,
) (*RunResult, error) {
	v2 := ca.toV2()
	result, err := v2.Run(ctx, sessionID, userMessage, extraContext)
	if err != nil {
		return nil, err
	}
	return &RunResult{
		Output:     result.Output,
		ToolCalls:  convertToolResults(result.ToolResults),
		TurnCount:  result.TurnCount,
		Assertions: result.Assertions,
		Learnings:  result.Learnings,
	}, nil
}

// convertToolResults converts v2 ToolResult slices to the RunResult format.
// Maintained as a named function for backward-compatibility when types diverge.
func convertToolResults(results []ToolResult) []ToolResult {
	out := make([]ToolResult, len(results))
	copy(out, results)
	return out
}

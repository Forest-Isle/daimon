package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/knowledge"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/skill"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
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
	memStore           memory.Store
	skillMgr           *skill.Manager
	pendingReflections sync.Map
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
	ca.planner = NewPlanner(provider, tools, cogCfg, llmCfg.Model)
	ca.executor = NewExecutor(tools, db, nil, cogCfg) // approvalFunc set via SetApprovalFunc
	ca.observer = NewObserver()
	ca.reflector = NewReflector(provider, nil, cogCfg, llmCfg.Model, &ca.pendingReflections)

	return ca
}

// SetMemoryStore injects the memory store into all phases that need it.
func (ca *CognitiveAgent) SetMemoryStore(s memory.Store) {
	ca.memStore = s
	ca.runtime.SetMemoryStore(s)
	// Preserve existing knowledge base when rebuilding the perceiver.
	oldKB := ca.perceiver.kb
	ca.perceiver = NewPerceiver(s)
	if oldKB != nil {
		ca.perceiver.SetKnowledgeBase(oldKB)
	}
	ca.reflector = NewReflector(
		ca.planner.provider,
		s,
		ca.cfg.Cognitive,
		ca.llmCfg.Model,
		&ca.pendingReflections,
	)
}

// SetKnowledgeBase injects a knowledge base into the perceiver for context retrieval.
func (ca *CognitiveAgent) SetKnowledgeBase(kb knowledge.KnowledgeBase) {
	ca.perceiver.SetKnowledgeBase(kb)
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

// SetSkillManager injects a skill manager into the cognitive agent and its inner runtime.
func (ca *CognitiveAgent) SetSkillManager(m *skill.Manager) {
	ca.skillMgr = m
	ca.runtime.SetSkillManager(m)
}

// ResolveReplanDecision is called by the Gateway when the user responds to a replan keyboard.
func (ca *CognitiveAgent) ResolveReplanDecision(key string, decision ReplanDecision) {
	if v, ok := ca.pendingReflections.Load(key); ok {
		ch := v.(chan ReplanDecision)
		select {
		case ch <- decision:
		default:
		}
	}
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

	// Delegate simple tasks to the plain Runtime
	if state.Goal.Complexity == ComplexitySimple {
		slog.Info("cognitive: simple task, delegating to runtime", "session", sess.ID)
		return ca.runtime.HandleMessage(ctx, ch, msg)
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

	var finalAnswer string

	for attempt := 0; attempt <= maxReplans; attempt++ {
		if attempt > 0 {
			slog.Info("cognitive: replanning", "attempt", attempt, "max", maxReplans, "session", sess.ID)
		}

		// ── PLAN ──────────────────────────────────────────────────────────────
		plan, err := ca.planner.Run(ctx, state)
		if err != nil {
			slog.Error("cognitive: plan failed", "err", err)
			break
		}
		plan.ReplanCount = attempt

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
		observations, err := ca.executor.Run(ctx, ch, sess, target, plan)
		if err != nil {
			slog.Error("cognitive: act failed", "err", err)
			break
		}

		// ── OBSERVE ───────────────────────────────────────────────────────────
		obsResult := ca.observer.Run(observations, plan)
		slog.Info("cognitive: observe complete",
			"success", obsResult.SuccessCount,
			"failure", obsResult.FailureCount,
			"progress", fmt.Sprintf("%.0f%%", obsResult.OverallProgress*100),
		)

		// ── REFLECT ───────────────────────────────────────────────────────────
		reflection, err := ca.reflector.Run(ctx, ch, target, state, plan, obsResult)
		if err != nil {
			slog.Error("cognitive: reflect failed", "err", err)
			finalAnswer = "Task completed."
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
	// Persist session
	if err := ca.sessions.Persist(ctx, sess); err != nil {
		slog.Error("cognitive: failed to persist session", "err", err)
	}

	// Save user message to memory
	if ca.memStore != nil {
		if err := ca.memStore.Save(ctx, memory.Entry{
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("cognitive: failed to save memory", "err", err)
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

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

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

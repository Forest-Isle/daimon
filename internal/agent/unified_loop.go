package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// UnifiedLoop implements a linear LLM -> tools -> LLM -> tools loop
// with concurrent execution of independent tool_use blocks.
type UnifiedLoop struct{}

// Execute runs the unified agent loop for a single inbound message.
func (UnifiedLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	systemPrompt := a.buildSystemPrompt(ctx, msg.Text)
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	startTime := time.Now()

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("unified loop iteration", "iteration", iteration, "session", sess.ID)

		updater, toolCalls, iterErr := loopIteration(ctx, a, ch, sess, target, systemPrompt, iteration, maxIter)
		if iterErr != nil {
			notifyEpisodeComplete(a, sess, iteration, false, startTime)
			return iterErr
		}

		if len(toolCalls) == 0 {
			notifyEpisodeComplete(a, sess, iteration, true, startTime)
			return nil
		}

		// UnifiedLoop: parallel tool dispatch
		a.dispatchToolsParallel(ctx, ch, sess, target, toolCalls, computeBudgetPressure(iteration, maxIter))

		notifyLoopIteration(a, sess, toolCalls, iteration)
		_ = updater
	}

	notifyEpisodeComplete(a, sess, maxIter, true, startTime)
	return nil
}

// unifiedNonStreaming handles the non-streaming fallback path.
func unifiedNonStreaming(ctx context.Context, a *Agent, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string, maxIter int) error {
	startTime := time.Now()
	for iteration := 0; iteration < maxIter; iteration++ {
		toolCalls, err := loopIterationNonStreaming(ctx, a, ch, sess, target, systemPrompt, iteration, maxIter)
		if err != nil {
			notifyEpisodeComplete(a, sess, iteration, false, startTime)
			return err
		}

		if len(toolCalls) == 0 {
			notifyEpisodeComplete(a, sess, iteration, true, startTime)
			return nil
		}

		a.dispatchToolsParallel(ctx, ch, sess, target, toolCalls, computeBudgetPressure(iteration, maxIter))
		notifyLoopIteration(a, sess, toolCalls, iteration)
	}
	notifyEpisodeComplete(a, sess, maxIter, true, startTime)
	return nil
}

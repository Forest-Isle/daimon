package agent

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// LinearLoop implements a standard ReAct loop: LLM → tools → LLM → ...
// with parallel dispatch of independent tool_use blocks.
type LinearLoop struct{}

// Execute runs the agent loop for a single inbound message.
func (LinearLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	systemPrompt := a.buildSystemPrompt(ctx, sess, msg.Text)
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	reflectionsUsed := 0

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("linear loop iteration", "iteration", iteration, "session", sess.ID)

		updater, toolCalls, iterErr := loopIteration(ctx, a, ch, sess, target, systemPrompt, iteration, maxIter)
		if iterErr != nil {
			return iterErr
		}

		if len(toolCalls) == 0 {
			// Reflexion: if the model converged but its plan still has incomplete
			// steps, inject a self-critique turn and continue instead of stopping.
			if prompt := a.maybeReflect(sess, reflectionsUsed); prompt != "" {
				reflectionsUsed++
				a.injectReflection(sess, prompt, reflectionsUsed)
				continue
			}
			return nil
		}

		a.dispatchToolsParallel(ctx, ch, sess, target, toolCalls, computeBudgetPressure(iteration, maxIter))

		_ = updater
	}

	return nil
}

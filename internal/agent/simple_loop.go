package agent

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// SimpleLoop implements a linear LLM -> tools -> LLM -> tools loop.
type SimpleLoop struct{}

// Execute runs the simple agent loop for a single inbound message.
func (SimpleLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	systemPrompt := a.buildSystemPrompt(ctx, msg.Text)
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("agent iteration", "iteration", iteration, "session", sess.ID)

		updater, toolCalls, iterErr := loopIteration(ctx, a, ch, sess, target, systemPrompt, iteration, maxIter)
		if iterErr != nil {
			return iterErr
		}

		if len(toolCalls) == 0 {
			return nil
		}

		// SimpleLoop: serial tool dispatch
		budgetWarning := computeBudgetPressure(iteration, maxIter)
		for _, tc := range toolCalls {
			a.executeToolCall(ctx, ch, sess, target, tc, budgetWarning)
		}

		_ = updater
	}

	return nil
}

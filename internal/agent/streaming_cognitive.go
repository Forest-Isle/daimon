package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// handleStreaming is the streaming variant of the cognitive agent's message handler.
func (ca *CognitiveAgent) handleStreaming(
	ctx context.Context,
	ch channel.Channel,
	msg channel.InboundMessage,
	sess *session.Session,
	target channel.MessageTarget,
	state *CognitiveState,
	parentTaskID string,
) error {
	sessionStart := time.Now()
	cognitiveTurnStart := sessionStart
	pipeline := NewStreamingPipeline(ca.perceiver, ca.planner, ca.executor, ca.observer, ca.reflector)
	updater, err := ch.SendStreaming(ctx, target)
	if err != nil {
		return fmt.Errorf("stream updater: %w", err)
	}

	confidenceThreshold := ca.cfg.Cognitive.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.6
	}
	maxReplans := ca.cfg.Cognitive.MaxReplanAttempts
	if maxReplans <= 0 {
		maxReplans = MaxReplanAttempts
	}

	var (
		plan       *TaskPlan
		obsResult  *ObservationResult
		reflection *Reflection
	)
	finalize := func() error {
		return ca.finalizeCognitiveSession(
			ctx, ch, sess, target, msg, state, plan, obsResult, reflection,
			nil, nil, 0, nil, sessionStart, cognitiveTurnStart,
		)
	}

	for attempt := 0; attempt <= maxReplans; attempt++ {
		pipeline.channels = NewPipelineChannels()
		attemptCtx, cancel := context.WithCancel(ctx)
		streamDone := make(chan struct{})
		go func(streamCh <-chan string) {
			defer close(streamDone)
			var visible strings.Builder
			for text := range streamCh {
				if text == "" {
					continue
				}
				if visible.Len() > 0 && !strings.HasSuffix(visible.String(), "\n") {
					visible.WriteString("\n")
				}
				visible.WriteString(text)
				_ = updater.Update(visible.String())
			}
		}(pipeline.channels.StreamText)

		result, runErr := pipeline.Run(attemptCtx, ch, sess, target, state, attempt)
		cancel()
		<-streamDone
		if runErr != nil {
			return runErr
		}

		plan = result.Plan
		obsResult = result.ObsResult
		reflection = result.Reflection

		if plan != nil && plan.DirectReply != "" {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   plan.DirectReply,
				CreatedAt: time.Now(),
			})
			if err := updater.Finish(plan.DirectReply); err != nil {
				return err
			}
			return finalize()
		}

		if reflection == nil {
			if err := updater.Finish("Task completed."); err != nil {
				return err
			}
			return finalize()
		}

		finalAnswer := reflection.FinalAnswer
		if finalAnswer == "" {
			finalAnswer = "Task completed."
		}

		if reflection.OverallConfidence >= confidenceThreshold || !reflection.NeedsReplan {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   finalAnswer,
				CreatedAt: time.Now(),
			})
			if err := updater.Finish(finalAnswer); err != nil {
				return err
			}
			return finalize()
		}

		decision, _ := ca.reflector.RequestReplanApproval(ctx, ch, target, reflection)
		switch decision {
		case ReplanAbort, ReplanContinue:
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   finalAnswer,
				CreatedAt: time.Now(),
			})
			if err := updater.Finish(finalAnswer); err != nil {
				return err
			}
			return finalize()
		case ReplanAdjust:
			if reflection.SuggestedAdjustment != "" {
				state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
			}
			continue
		}
	}

	if reflection != nil && reflection.FinalAnswer != "" {
		sess.AddMessage(session.Message{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Role:      "assistant",
			Content:   reflection.FinalAnswer,
			CreatedAt: time.Now(),
		})
		if err := updater.Finish(reflection.FinalAnswer); err != nil {
			return err
		}
		return finalize()
	}
	_ = parentTaskID
	if err := updater.Finish("Task completed."); err != nil {
		return err
	}
	return finalize()
}

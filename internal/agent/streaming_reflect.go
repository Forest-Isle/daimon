package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// StreamingReflector wraps the existing Reflector with streaming LLM output.
type StreamingReflector struct {
	inner *Reflector
}

func NewStreamingReflector(inner *Reflector) *StreamingReflector {
	return &StreamingReflector{inner: inner}
}

// Stream runs REFLECT using provider.Stream() for streaming reflection text.
func (sr *StreamingReflector) Stream(
	ctx context.Context,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	assertionCh <-chan *Assertion,
	streamOut chan<- string,
	replanAttempt int,
) (*Reflection, error) {
	if state == nil {
		return nil, fmt.Errorf("nil cognitive state")
	}
	if plan == nil {
		plan = &TaskPlan{}
	}
	if obsResult == nil {
		obsResult = &ObservationResult{}
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

drain:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case a, ok := <-assertionCh:
			if !ok {
				break drain
			}
			if a == nil {
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(5 * time.Second)
		case <-timer.C:
			break drain
		}
	}

	userMsg := buildReflectUserMessage(state, plan, obsResult, replanAttempt)
	maxTokens := sr.inner.cfg.ReflectMaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	system := ReflectSystemPrompt
	if state.Personality != "" {
		system += "\n\nPERSONALITY (apply to final_answer tone):\n" + state.Personality
	}
	if state.PersistentRules != "" {
		system += "\n\nADDITIONAL RULES (must follow):\n" + state.PersistentRules
	}

	stream, err := sr.inner.provider.Stream(ctx, CompletionRequest{
		Model:     sr.inner.llmModel,
		System:    system,
		Messages:  []CompletionMessage{{Role: "user", Content: userMsg}},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("reflect llm stream: %w", err)
	}
	defer stream.Close()

	var fullText strings.Builder
	for {
		delta, err := stream.Next()
		if err != nil {
			return nil, fmt.Errorf("reflect stream next: %w", err)
		}
		if delta.Text != "" {
			fullText.WriteString(delta.Text)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case streamOut <- "[REFLECT] " + delta.Text:
			default:
			}
		}
		if delta.Done {
			break
		}
	}

	reflection, err := parseReflectResponse(fullText.String())
	if err != nil {
		reflection = &Reflection{
			OverallConfidence: 0.5,
			Succeeded:         obsResult.SuccessCount > 0,
			FinalAnswer:       "Task completed with partial results.",
		}
	}
	if reflection.Reasoning != nil {
		reflection.OverallConfidence = validateConfidenceFromDimensions(
			reflection.OverallConfidence, reflection.Reasoning,
		)
	}

	sr.inner.saveExperience(ctx, nil, channel.MessageTarget{}, state, plan, reflection)
	return reflection, nil
}

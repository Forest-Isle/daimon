package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/memory"
)

// ReflectionCallback is called by the Gateway when the user responds to a replan keyboard.
type ReflectionCallback func(key string, decision ReplanDecision)

// Reflector implements the REFLECT phase: LLM evaluation + confidence approval + memory write.
type Reflector struct {
	provider           Provider
	memStore           memory.Store
	cfg                config.CognitiveConfig
	llmModel           string
	pendingReflections *sync.Map
}

// NewReflector creates a new Reflector.
func NewReflector(
	provider Provider,
	memStore memory.Store,
	cfg config.CognitiveConfig,
	llmModel string,
	pendingReflections *sync.Map,
) *Reflector {
	model := cfg.ReflectModel
	if model == "" {
		model = llmModel
	}
	return &Reflector{
		provider:           provider,
		memStore:           memStore,
		cfg:                cfg,
		llmModel:           model,
		pendingReflections: pendingReflections,
	}
}

// Run executes the REFLECT phase. Returns a Reflection (with FinalAnswer).
func (r *Reflector) Run(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
) (*Reflection, error) {
	userMsg := buildReflectUserMessage(state, plan, obsResult)
	maxTokens := r.cfg.ReflectMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	req := CompletionRequest{
		Model:     r.llmModel,
		System:    ReflectSystemPrompt,
		Messages:  []CompletionMessage{{Role: "user", Content: userMsg}},
		Tools:     nil,
		MaxTokens: maxTokens,
	}

	resp, err := r.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reflect llm call: %w", err)
	}

	reflection, err := parseReflectResponse(resp.Text)
	if err != nil {
		slog.Warn("reflect: parse failed, using fallback", "err", err)
		reflection = &Reflection{
			OverallConfidence: 0.5,
			Succeeded:         obsResult.SuccessCount > 0,
			FinalAnswer:       "Task completed with partial results.",
		}
	}

	slog.Info("reflect complete",
		"confidence", reflection.OverallConfidence,
		"succeeded", reflection.Succeeded,
		"needs_replan", reflection.NeedsReplan,
	)

	// Write experience to memory regardless of success/failure
	r.saveExperience(ctx, state, plan, reflection)

	return reflection, nil
}

// RequestReplanApproval sends a Telegram inline keyboard and waits for the user's decision.
func (r *Reflector) RequestReplanApproval(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	reflection *Reflection,
) (ReplanDecision, error) {
	tgAdapter, ok := ch.(interface {
		SendReflectionRequest(chatID int64, reason string, confidence float64) (int, error)
	})
	if !ok {
		// Non-Telegram: auto-continue
		return ReplanContinue, nil
	}

	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return ReplanContinue, nil
	}

	key := fmt.Sprintf("reflect_%s_%d", target.ChannelID, time.Now().UnixNano())
	_, sendErr := tgAdapter.SendReflectionRequest(chatID, reflection.ReplanReason, reflection.OverallConfidence)
	if sendErr != nil {
		slog.Warn("reflect: failed to send approval request", "err", sendErr)
		return ReplanContinue, nil
	}

	// Register pending channel
	resultCh := make(chan ReplanDecision, 1)
	r.pendingReflections.Store(key, resultCh)
	defer r.pendingReflections.Delete(key)

	timeoutSeconds := r.cfg.ApprovalTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}

	select {
	case decision := <-resultCh:
		slog.Info("reflect: replan decision received", "decision", decision)
		return decision, nil
	case <-time.After(time.Duration(timeoutSeconds) * time.Second):
		slog.Info("reflect: replan approval timed out, defaulting to continue")
		return ReplanContinue, nil
	case <-ctx.Done():
		return ReplanContinue, ctx.Err()
	}
}

// saveExperience writes task outcome to the memory store for future retrieval.
func (r *Reflector) saveExperience(ctx context.Context, state *CognitiveState, plan *TaskPlan, reflection *Reflection) {
	if r.memStore == nil {
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("GOAL: %s\n", state.Goal.Raw))
	sb.WriteString(fmt.Sprintf("PLAN: %s\n", plan.Summary))
	sb.WriteString(fmt.Sprintf("OUTCOME: succeeded=%v, confidence=%.2f\n", reflection.Succeeded, reflection.OverallConfidence))
	if len(reflection.LessonsLearned) > 0 {
		sb.WriteString("LESSONS:\n")
		for _, lesson := range reflection.LessonsLearned {
			sb.WriteString("- " + lesson + "\n")
		}
	}

	err := r.memStore.Save(ctx, memory.Entry{
		SessionID: state.SessionID,
		Content:   sb.String(),
		Metadata: map[string]string{
			"type":       "cognitive_experience",
			"complexity": string(state.Goal.Complexity),
			"succeeded":  fmt.Sprintf("%v", reflection.Succeeded),
		},
		CreatedAt: time.Now(),
	})
	if err != nil {
		slog.Warn("reflect: failed to save experience to memory", "err", err)
	}
}

// buildReflectUserMessage fills in the ReflectUserPromptTemplate.
func buildReflectUserMessage(state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult) string {
	// Observations section
	var obsSB strings.Builder
	if len(obsResult.Observations) == 0 {
		obsSB.WriteString("(no tool executions)")
	} else {
		for _, obs := range obsResult.Observations {
			obsSB.WriteString(fmt.Sprintf("- SubTask %s [%s]:\n", obs.SubTaskID, obs.ToolName))
			if obs.Denied {
				obsSB.WriteString("  Status: DENIED\n")
			} else if obs.Error != "" {
				obsSB.WriteString(fmt.Sprintf("  Status: FAILED\n  Error: %s\n", obs.Error))
			} else {
				output := obs.Output
				if len(output) > 500 {
					output = output[:500] + "...[truncated]"
				}
				obsSB.WriteString(fmt.Sprintf("  Status: SUCCESS\n  Output: %s\n", output))
			}
		}
	}

	// Stats section
	stats := fmt.Sprintf(
		"Success: %d, Failures: %d, Denied: %d, Progress: %.0f%%, Error patterns: %s",
		obsResult.SuccessCount,
		obsResult.FailureCount,
		obsResult.DeniedCount,
		obsResult.OverallProgress*100,
		strings.Join(obsResult.ErrorPatterns, ", "),
	)

	msg := ReflectUserPromptTemplate
	msg = strings.ReplaceAll(msg, "{{GOAL}}", state.Goal.Raw)
	msg = strings.ReplaceAll(msg, "{{PLAN_SUMMARY}}", plan.Summary)
	msg = strings.ReplaceAll(msg, "{{OBSERVATIONS}}", obsSB.String())
	msg = strings.ReplaceAll(msg, "{{STATS}}", stats)
	return msg
}

// parseReflectResponse tries three fallbacks to extract JSON from LLM output.
func parseReflectResponse(text string) (*Reflection, error) {
	raw := strings.TrimSpace(text)

	var rj reflectJSON

	// Attempt 1: direct parse
	if err := json.Unmarshal([]byte(raw), &rj); err == nil {
		return reflectJSONToReflection(rj), nil
	}

	// Attempt 2: extract ```json ... ``` block
	if m := jsonBlockRe.FindStringSubmatch(raw); len(m) == 2 {
		if err := json.Unmarshal([]byte(m[1]), &rj); err == nil {
			return reflectJSONToReflection(rj), nil
		}
	}

	// Attempt 3: extract first {...} block
	if m := jsonObjectRe.FindString(raw); m != "" {
		if err := json.Unmarshal([]byte(m), &rj); err == nil {
			return reflectJSONToReflection(rj), nil
		}
	}

	return nil, fmt.Errorf("no valid JSON found in reflect response")
}

func reflectJSONToReflection(rj reflectJSON) *Reflection {
	return &Reflection{
		OverallConfidence:   rj.OverallConfidence,
		Succeeded:           rj.Succeeded,
		LessonsLearned:      rj.LessonsLearned,
		SuggestedAdjustment: rj.SuggestedAdjustment,
		FinalAnswer:         rj.FinalAnswer,
		NeedsReplan:         rj.NeedsReplan,
		ReplanReason:        rj.ReplanReason,
	}
}

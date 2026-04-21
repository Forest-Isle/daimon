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
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// ReflectionCallback is called by the Gateway when the user responds to a replan keyboard.
type ReflectionCallback func(key string, decision ReplanDecision)

// MemoryNotifyFunc is called after memory operations complete to notify the user.
type MemoryNotifyFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, summary string)

// Reflector implements the REFLECT phase: LLM evaluation + confidence approval + memory.md write.
type Reflector struct {
	provider       Provider
	memStore       memory.Store
	factExtractor  *memory.LLMFactExtractor
	lifecycleMgr   *memory.LifecycleManager
	graphExtractor *graph.LLMEntityExtractor
	cfg            config.CognitiveConfig
	llmModel       string
	rlPolicy       RLPolicy       // optional RL policy
	memoryNotify   MemoryNotifyFunc // optional notification callback
}

// NewReflector creates a new Reflector.
func NewReflector(
	provider Provider,
	memStore memory.Store,
	cfg config.CognitiveConfig,
	llmModel string,
) *Reflector {
	model := cfg.ReflectModel
	if model == "" {
		model = llmModel
	}
	return &Reflector{
		provider: provider,
		memStore: memStore,
		cfg:      cfg,
		llmModel: model,
	}
}

// SetFactExtractor injects a fact extractor for lifecycle-managed memory.md writes.
func (r *Reflector) SetFactExtractor(fe *memory.LLMFactExtractor) {
	r.factExtractor = fe
}

// SetLifecycleManager injects a lifecycle manager for ADD/UPDATE/DELETE/NOOP decisions.
func (r *Reflector) SetLifecycleManager(lm *memory.LifecycleManager) {
	r.lifecycleMgr = lm
}

// SetEntityExtractor injects a graph entity extractor for populating the knowledge graph.
func (r *Reflector) SetEntityExtractor(e *graph.LLMEntityExtractor) {
	r.graphExtractor = e
}

// SetRLPolicy injects an optional RL policy.
func (r *Reflector) SetRLPolicy(policy RLPolicy) {
	r.rlPolicy = policy
}

// SetMemoryNotifyFunc injects a callback for sending memory operation summaries.
func (r *Reflector) SetMemoryNotifyFunc(fn MemoryNotifyFunc) {
	r.memoryNotify = fn
}

// Run executes the REFLECT phase. Returns a Reflection (with FinalAnswer).
func (r *Reflector) Run(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	replanAttempt int,
) (*Reflection, error) {
	userMsg := buildReflectUserMessage(state, plan, obsResult, replanAttempt)
	maxTokens := r.cfg.ReflectMaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	// Build system prompt, appending personality and persistent rules if available
	system := ReflectSystemPrompt
	if state.Personality != "" {
		system += "\n\nPERSONALITY (apply to final_answer tone):\n" + state.Personality
	}
	if state.PersistentRules != "" {
		system += "\n\nADDITIONAL RULES (must follow):\n" + state.PersistentRules
	}

	req := CompletionRequest{
		Model:     r.llmModel,
		System:    system,
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

	// Cross-check overall_confidence against dimension-derived score.
	if reflection.Reasoning != nil {
		reflection.OverallConfidence = validateConfidenceFromDimensions(
			reflection.OverallConfidence, reflection.Reasoning,
		)
	}

	slog.Info("reflect complete",
		"confidence", reflection.OverallConfidence,
		"succeeded", reflection.Succeeded,
		"needs_replan", reflection.NeedsReplan,
	)

	// Write experience to memory.md regardless of success/failure
	r.saveExperience(ctx, ch, target, state, plan, reflection)

	return reflection, nil
}

// RequestReplanApproval asks the user via the channel whether to replan.
// Channels that implement channel.ReflectionSender get an interactive prompt;
// all others default to ReplanContinue.
func (r *Reflector) RequestReplanApproval(
	ctx context.Context,
	ch channel.Channel,
	target channel.MessageTarget,
	reflection *Reflection,
) (ReplanDecision, error) {
	sender, ok := ch.(channel.ReflectionSender)
	if !ok {
		// Channel does not support interactive reflection — auto-continue.
		return ReplanContinue, nil
	}

	chDecision, err := sender.SendReflectionRequest(ctx, target, reflection.ReplanReason, reflection.OverallConfidence)
	if err != nil {
		slog.Warn("reflect: failed to send reflection request", "err", err)
		return ReplanContinue, nil
	}

	// Convert channel.ReplanDecision to agent.ReplanDecision
	switch chDecision {
	case channel.ReplanContinue:
		return ReplanContinue, nil
	case channel.ReplanAdjust:
		return ReplanAdjust, nil
	case channel.ReplanAbort:
		return ReplanAbort, nil
	default:
		return ReplanContinue, nil
	}
}

// saveExperience extracts facts and uses lifecycle management if available.
// It runs asynchronously (fire-and-forget) to avoid blocking the main cognitive loop.
// The incoming ctx is intentionally not forwarded to the goroutine; a fresh background
// context is used so the write is not cancelled when the request context expires.
func (r *Reflector) saveExperience(_ context.Context, ch channel.Channel, target channel.MessageTarget, state *CognitiveState, plan *TaskPlan, reflection *Reflection) {
	if r.memStore == nil {
		return
	}

	go func() {
		bgCtx := context.Background()

		if r.factExtractor != nil && r.lifecycleMgr != nil {
			// Extract distilled facts from the goal/outcome pair.
			facts, err := r.factExtractor.Extract(bgCtx, state.Goal.Raw, reflection.FinalAnswer)
			if err != nil {
				slog.Warn("reflect: fact extraction failed", "err", err)
			} else {
				var summary memory.MemoryOperationSummary
				for _, fact := range facts {
					result, err := r.lifecycleMgr.Process(bgCtx, fact, state.SessionID, state.UserID, memory.ScopeSession)
					if err != nil {
						slog.Warn("reflect: lifecycle process failed", "err", err, "fact", fact.Content)
						continue
					}
					if result != nil {
						switch result.Action {
						case memory.ActionADD:
							summary.Added++
						case memory.ActionUPDATE:
							summary.Updated++
						case memory.ActionDELETE:
							summary.Deleted++
						}
					}
				}
				// Send notification if there were changes
				if summary.HasChanges() && r.memoryNotify != nil {
					r.memoryNotify(bgCtx, ch, target, summary.String())
				}
				if len(facts) > 0 {
					// Facts extracted and lifecycle-managed; skip raw experience save.
					// Still extract graph entities below.
					r.extractGraphEntities(bgCtx, state, reflection)
					return
				}
			}
		}

		// Fallback: save raw cognitive experience to the legacy memories table.
		var sb strings.Builder
		_, _ = fmt.Fprintf(&sb, "GOAL: %s\n", state.Goal.Raw)
		_, _ = fmt.Fprintf(&sb, "PLAN: %s\n", plan.Summary)
		_, _ = fmt.Fprintf(&sb, "OUTCOME: succeeded=%v, confidence=%.2f\n", reflection.Succeeded, reflection.OverallConfidence)
		if len(reflection.LessonsLearned) > 0 {
			sb.WriteString("LESSONS:\n")
			for _, lesson := range reflection.LessonsLearned {
				sb.WriteString("- " + lesson + "\n")
			}
		}

		err := r.memStore.Save(bgCtx, memory.Entry{
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
			slog.Warn("reflect: failed to save experience to memory.md", "err", err)
		}

		r.extractGraphEntities(bgCtx, state, reflection)
	}()
}

// extractGraphEntities populates the knowledge graph from the goal/outcome pair.
func (r *Reflector) extractGraphEntities(ctx context.Context, state *CognitiveState, reflection *Reflection) {
	if r.graphExtractor == nil {
		return
	}
	text := fmt.Sprintf("Goal: %s\nOutcome: %s", state.Goal.Raw, reflection.FinalAnswer)
	if err := r.graphExtractor.Extract(ctx, text, "reflection", state.SessionID); err != nil {
		slog.Warn("reflect: graph entity extraction failed", "err", err)
	}
}

// buildReflectUserMessage fills in the ReflectUserPromptTemplate.
func buildReflectUserMessage(state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult, replanAttempt int) string {
	// Observations section
	var obsSB strings.Builder
	if len(obsResult.Observations) == 0 {
		obsSB.WriteString("(no tool executions)")
	} else {
		for _, obs := range obsResult.Observations {
			_, _ = fmt.Fprintf(&obsSB, "- SubTask %s [%s]:\n", obs.SubTaskID, obs.ToolName)
			if obs.Denied {
				obsSB.WriteString("  Status: DENIED\n")
			} else if obs.Error != "" {
				_, _ = fmt.Fprintf(&obsSB, "  Status: FAILED\n  Error: %s\n", obs.Error)
			} else {
				output := obs.Output
				if len(output) > 1500 {
					output = output[:1500] + "...[truncated]"
				}
				_, _ = fmt.Fprintf(&obsSB, "  Status: SUCCESS\n  Output: %s\n", output)
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
	goalSection := state.Goal.Raw
	if len(obsResult.Observations) > 0 {
		goalSection += "\n\nSUB-GOAL VERIFICATION CHECKLIST (check each):"
		for i, obs := range obsResult.Observations {
			status := "VERIFIED"
			if obs.Denied {
				status = "DENIED (not completed)"
			} else if obs.Error != "" {
				status = "FAILED"
			}
			goalSection += fmt.Sprintf("\n  %d. [%s] %s → %s", i+1, obs.ToolName, obs.SubTaskID, status)
		}
	}
	msg = strings.ReplaceAll(msg, "{{GOAL}}", goalSection)
	msg = strings.ReplaceAll(msg, "{{PLAN_SUMMARY}}", plan.Summary)
	msg = strings.ReplaceAll(msg, "{{OBSERVATIONS}}", obsSB.String())
	msg = strings.ReplaceAll(msg, "{{STATS}}", stats)

	enriched := enrichFailureContexts(obsResult.Failures, replanAttempt)
	failureCtx := formatFailureContextForPrompt(enriched)
	msg = strings.ReplaceAll(msg, "{{FAILURE_CONTEXT}}", failureCtx)
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
		Reasoning:           rj.Reasoning,
	}
}

// confidenceDivergenceThreshold is the maximum allowed deviation between the
// LLM's self-reported overall_confidence and the score derived from the
// individual dimension scores. If the gap exceeds this, the dimension-derived
// value is used instead — acting as an automatic calibration guardrail.
const confidenceDivergenceThreshold = 0.15

// validateConfidenceFromDimensions cross-checks the LLM's overall_confidence
// against the sum of its own dimension scores. When the deviation exceeds
// confidenceDivergenceThreshold, the dimension-derived value is trusted
// because the per-dimension scoring (with explicit anchors and CoT reasoning)
// is more grounded than a single free-form float.
func validateConfidenceFromDimensions(llmConfidence float64, reasoning *ReflectReasoning) float64 {
	if reasoning == nil {
		return llmConfidence
	}

	derived := reasoning.DerivedConfidence()

	deviation := llmConfidence - derived
	if deviation < 0 {
		deviation = -deviation
	}

	if deviation > confidenceDivergenceThreshold {
		slog.Warn("reflect: confidence diverges from dimension scores, using derived value",
			"llm_confidence", llmConfidence,
			"derived_confidence", derived,
			"deviation", deviation,
		)
		return derived
	}

	return llmConfidence
}

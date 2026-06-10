package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// defaultMaxReflections bounds how many times the loop re-engages the model
// after it converges with an incomplete plan. maxIter remains the hard ceiling.
const defaultMaxReflections = 3

// incompletePlanSteps returns the steps of the session's active plan that are
// neither done nor failed. Returns nil if there is no plan or all steps are
// resolved — in which case reflection does not trigger.
func incompletePlanSteps(sess *session.Session) []tool.PlanStep {
	if sess == nil {
		return nil
	}
	raw := sess.GetMetadata("plan")
	if raw == "" {
		return nil
	}
	var plan tool.Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil
	}
	var incomplete []tool.PlanStep
	for _, s := range plan.Steps {
		if s.Status != "done" && s.Status != "failed" {
			incomplete = append(incomplete, s)
		}
	}
	return incomplete
}

// buildReflectionPrompt constructs a self-critique message grounded in the
// plan's incomplete steps and their success criteria (Reflexion pattern).
func buildReflectionPrompt(steps []tool.PlanStep) string {
	var b strings.Builder
	b.WriteString("[Self-check] You stopped, but your plan still has incomplete steps:\n\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "- [%s] %s", s.Status, s.Description)
		if s.Criteria != "" {
			fmt.Fprintf(&b, " (success criteria: %s)", s.Criteria)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nFor each incomplete step: either do the work and verify it against its success criteria (e.g. run test_run), or — if it is genuinely already satisfied or no longer needed — update the plan via the `plan` tool to mark it done or failed with a brief reason. Do not stop until every step is done or failed.")
	return b.String()
}

// maybeReflect decides whether to inject a self-correction turn. It returns a
// reflection prompt (to be added as a user message) when the model has
// converged but the active plan still has incomplete steps and the reflection
// budget is not exhausted. Returns "" to let the loop terminate normally.
func (a *Agent) maybeReflect(sess *session.Session, reflectionsUsed int) string {
	budget := a.deps.Core.Cfg.MaxReflections
	if budget < 0 {
		return "" // negative disables reflection
	}
	if budget == 0 {
		budget = defaultMaxReflections
	}
	if reflectionsUsed >= budget {
		return ""
	}
	steps := incompletePlanSteps(sess)
	if len(steps) == 0 {
		return ""
	}
	return buildReflectionPrompt(steps)
}

// injectReflection appends the self-critique message to the session and emits
// a ReflectionTriggered event for observability.
func (a *Agent) injectReflection(sess *session.Session, prompt string, attempt int) {
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   prompt,
		CreatedAt: time.Now(),
	})
	steps := incompletePlanSteps(sess)
	a.eventBus.Publish(ReflectionTriggered{
		SessionID:       sess.ID,
		IncompleteSteps: len(steps),
		Attempt:         attempt,
	})
	slog.Info("reflection triggered", "session", sess.ID, "incomplete_steps", len(steps), "attempt", attempt)
}

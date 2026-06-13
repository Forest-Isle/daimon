package action

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// ValueGate is the head of the action pipeline (values → trust → classify → …).
// It decides whether a non-low-risk action may run autonomously: a covering
// value decision (or earned trust) permits it; otherwise the action layer
// refuses autonomous release and the model must ask the user once (ask-once).
//
// A nil gate disables the check, leaving the interceptor observe-only — the
// default, so behavior is unchanged until the gateway wires a gate.
type ValueGate interface {
	// Permit reports whether an action of the given reversibility class and
	// context may run autonomously. ref is the permitting source recorded on the
	// receipt (for example "value:<id>", "trust:<level>", or "interactive"). When
	// permitted is false the interceptor blocks the action without executing it.
	Permit(ctx context.Context, class Class, contextKey string) (ref string, permitted bool)
}

// valueBlockedResult is returned (without executing the tool) when the value
// gate refuses an action. It is an error result so the model sees it and can
// close the episode blocked with an open question.
func valueBlockedResult(toolName string, class Class) *tool.ToolResult {
	return &tool.ToolResult{
		Error: fmt.Sprintf(
			"action blocked by value gate: a %s %q action requires an explicit value decision before it can run autonomously, and none covers it. The tool was NOT executed. "+
				"Close the episode with status \"blocked\" and an open_question asking the user to decide this tradeoff; once they answer, record it with the values tool so future actions in this domain are covered.",
			class, toolName),
		Metadata: map[string]string{
			"action_class":   class.String(),
			"value_blocked":  "true",
			"value_decision": "required",
		},
	}
}

package agent

import (
	"fmt"
	"strings"
)

const degradeThreshold = 3

// enrichFailureContexts returns a copy of failures with AttemptCount set to replanAttempt.
func enrichFailureContexts(failures []FailureContext, replanAttempt int) []FailureContext {
	out := make([]FailureContext, len(failures))
	copy(out, failures)
	for i := range out {
		out[i].AttemptCount = replanAttempt
	}
	return out
}

// formatFailureContextForPrompt renders failures into a structured block for the REFLECT re-plan prompt.
func formatFailureContextForPrompt(failures []FailureContext) string {
	if len(failures) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("FAILURE CONTEXT (structured):\n")

	for _, fc := range failures {
		fmt.Fprintf(&b, "- SubTask %s [%s] (attempt %d): %s — %s\n",
			fc.SubTaskID, fc.ToolName, fc.AttemptCount, fc.ErrorType, fc.ErrorMsg)
		for _, a := range fc.Assertions {
			tag := "FAIL"
			if a.Passed {
				tag = "PASS"
			}
			fmt.Fprintf(&b, "    [%s] %s → %s\n", tag, a.Check, a.Actual)
		}
	}

	if shouldDegradeRetry(failures) {
		b.WriteString("\nWARNING: Multiple retry attempts have failed. Consider a more conservative approach — simplify the command, break it into smaller steps, or use a different tool.\n")
	}

	return b.String()
}

// shouldDegradeRetry returns true when any failure has been retried at or beyond degradeThreshold.
func shouldDegradeRetry(failures []FailureContext) bool {
	for _, fc := range failures {
		if fc.AttemptCount >= degradeThreshold {
			return true
		}
	}
	return false
}

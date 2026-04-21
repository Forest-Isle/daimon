package agent

import (
	"fmt"
	"strings"
)

// Observer implements the OBSERVE phase: pure computation, no LLM calls.
type Observer struct{}

// NewObserver creates a new Observer.
func NewObserver() *Observer { return &Observer{} }

// Run analyzes observations and returns aggregate statistics.
func (o *Observer) Run(observations []Observation, plan *TaskPlan) *ObservationResult {
	result := &ObservationResult{
		Observations: observations,
	}

	total := len(plan.SubTasks)
	skippedCount := 0
	for _, st := range plan.SubTasks {
		if st.Status == SubTaskSkipped {
			skippedCount++
		}
	}

	// Track consecutive denials per tool for cascading failure detection.
	denialCounts := make(map[string]int)

	for _, obs := range observations {
		if obs.Denied {
			result.DeniedCount++
			denialCounts[obs.ToolName]++

			fc := FailureContext{
				SubTaskID: obs.SubTaskID,
				ToolName:  obs.ToolName,
				ErrorType: FailureDenied,
				ErrorMsg:  fmt.Sprintf("tool '%s' was denied — this is recoverable; consider alternative tools or reasoning from available context", obs.ToolName),
			}

			if idx := strings.Index(obs.Output, "[Recovery Hint:"); idx >= 0 {
				fc.ErrorMsg += ". " + obs.Output[idx:]
			}

			// Inject error-pattern specific recovery hints based on output content.
			lower := strings.ToLower(obs.Output)
			switch {
			case strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist"):
				fc.ErrorMsg += " Use bash with find/ls to locate the correct path before retrying."
			case strings.Contains(lower, "permission denied"):
				fc.ErrorMsg += " Check permissions with 'ls -la', consider read-only alternatives."
			}

			// Cascading failure: same tool denied 2+ times — instruct diagnostic pivot.
			if denialCounts[obs.ToolName] >= 2 {
				fc.ErrorMsg += " STOP retrying same tool — switch to diagnostic mode with bash to understand environment state."
			}

			result.Failures = append(result.Failures, fc)
			continue
		}

		assertions := generateAssertions(obs)
		result.Assertions = append(result.Assertions, assertions...)

		var failed []string
		for _, a := range assertions {
			if !a.Passed {
				failed = append(failed, a.Check)
			}
		}

		if len(failed) > 0 || obs.Error != "" {
			result.FailureCount++
			fc := FailureContext{
				SubTaskID:  obs.SubTaskID,
				ToolName:   obs.ToolName,
				ErrorMsg:   strings.Join(failed, "; "),
				Assertions: assertions,
			}
			if obs.Error != "" {
				fc.ErrorType = FailureToolError
				if len(failed) > 0 {
					fc.ErrorMsg = obs.Error + " [failed checks: " + strings.Join(failed, "; ") + "]"
				} else {
					fc.ErrorMsg = obs.Error
				}
			} else {
				fc.ErrorType = FailureAssertionFailed
			}
			result.Failures = append(result.Failures, fc)
		} else {
			result.SuccessCount++
		}
	}

	// Progress: done / (total - skipped)
	effective := total - skippedCount
	if effective > 0 {
		result.OverallProgress = float64(result.SuccessCount) / float64(effective)
	}

	// Error pattern detection
	result.ErrorPatterns = detectErrorPatterns(observations, result)

	return result
}

// detectErrorPatterns classifies common error types from observations.
func detectErrorPatterns(observations []Observation, result *ObservationResult) []string {
	var patterns []string

	if result.DeniedCount == len(observations) && len(observations) > 0 {
		patterns = append(patterns, "all_denied")
		return patterns
	}

	var permErr, netErr, toolNotFound bool
	for _, obs := range observations {
		if obs.Error == "" {
			continue
		}
		lower := strings.ToLower(obs.Error)
		if strings.Contains(lower, "permission") || strings.Contains(lower, "denied") ||
			strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") {
			permErr = true
		}
		if strings.Contains(lower, "network") || strings.Contains(lower, "connection") ||
			strings.Contains(lower, "timeout") || strings.Contains(lower, "dial") {
			netErr = true
		}
		if strings.Contains(lower, "tool not found") {
			toolNotFound = true
		}
	}

	if permErr {
		patterns = append(patterns, "permission_error")
	}
	if netErr {
		patterns = append(patterns, "network_error")
	}
	if toolNotFound {
		patterns = append(patterns, "tool_not_found")
	}

	if result.DeniedCount > 0 && result.DeniedCount < len(observations) {
		patterns = append(patterns, "partial_denied_recoverable")
	}

	return patterns
}

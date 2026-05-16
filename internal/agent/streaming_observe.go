package agent

import (
	"context"
	"fmt"
	"strings"
)

// StreamingObserver wraps the existing Observer with streaming assertion generation.
type StreamingObserver struct {
	inner *Observer
}

func NewStreamingObserver(inner *Observer) *StreamingObserver {
	return &StreamingObserver{inner: inner}
}

// Stream processes observations as they arrive from ACT.
func (so *StreamingObserver) Stream(
	ctx context.Context,
	obsCh <-chan *Observation,
	assertionCh chan<- *Assertion,
) (*ObservationResult, error) {
	result := &ObservationResult{}
	denialCounts := make(map[string]int)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case obs, ok := <-obsCh:
			if !ok {
				total := result.SuccessCount + result.FailureCount + result.DeniedCount
				if total > 0 {
					result.OverallProgress = float64(result.SuccessCount) / float64(total)
				}
				result.ErrorPatterns = detectErrorPatterns(result.Observations, result)
				return result, nil
			}
			if obs == nil {
				continue
			}

			result.Observations = append(result.Observations, *obs)

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
				if denialCounts[obs.ToolName] >= 2 {
					fc.ErrorMsg += " STOP retrying same tool — switch to diagnostic mode with bash to understand environment state."
				}
				result.Failures = append(result.Failures, fc)
				continue
			}

			assertions := generateAssertions(*obs)
			result.Assertions = append(result.Assertions, assertions...)
			for i := range assertions {
				a := assertions[i]
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case assertionCh <- &a:
				}
			}

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
	}
}

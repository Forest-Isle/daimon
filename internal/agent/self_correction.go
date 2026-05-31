package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// SelfCorrectionEngine verifies loop output and triggers replanning when results
// are empty or indicate errors. It wraps the Run method with retry logic.
type SelfCorrectionEngine struct {
	maxRetries int
}

// NewSelfCorrectionEngine creates an engine with the given maximum retry attempts.
func NewSelfCorrectionEngine(maxRetries int) *SelfCorrectionEngine {
	return &SelfCorrectionEngine{maxRetries: maxRetries}
}

// VerifyAndCorrect checks the loop result and, if it appears to have failed,
// re-runs through the provided runner function with additional error context.
func (e *SelfCorrectionEngine) VerifyAndCorrect(
	ctx context.Context,
	loopResult *LoopResult,
	runner func(ctx context.Context, failureContext string) (*LoopResult, error),
) (*LoopResult, error) {
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		// Basic heuristic: if output is non-empty and does not contain error markers, assume success
		if loopResult.Output != "" && !strings.Contains(strings.ToLower(loopResult.Output), "error:") {
			return loopResult, nil
		}

		if attempt >= e.maxRetries {
			slog.Warn("self-correction max retries reached", "attempts", attempt)
			return loopResult, nil
		}

		slog.Info("self-correction triggered", "attempt", attempt+1)
		failureCtx := "Previous attempt had issues. Please try again with a different approach."
		var err error
		loopResult, err = runner(ctx, failureCtx)
		if err != nil {
			return loopResult, fmt.Errorf("self-correction runner: %w", err)
		}
	}
	return loopResult, nil
}

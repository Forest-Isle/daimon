package hook

import (
	"context"
	"strings"
)

// SafetyAnalyzerHandler is a PreToolUse handler that checks tool inputs
// against blocked patterns. It migrates the legacy Policy blocklist into hook form.
type SafetyAnalyzerHandler struct {
	BlockPatterns []string
}

// NewSafetyAnalyzerHandler creates a safety analyzer with the given block patterns.
func NewSafetyAnalyzerHandler(patterns []string) *SafetyAnalyzerHandler {
	return &SafetyAnalyzerHandler{BlockPatterns: patterns}
}

func (h *SafetyAnalyzerHandler) OnPreToolUse(_ context.Context, event PreToolUseEvent) (PreToolUseResult, error) {
	input := strings.ToLower(event.Input)
	for _, pattern := range h.BlockPatterns {
		if strings.Contains(input, strings.ToLower(pattern)) {
			return PreToolUseResult{
				Action: "deny",
				Reason: "blocked by safety analyzer: " + pattern,
			}, nil
		}
	}
	return PreToolUseResult{Action: "passthrough"}, nil
}

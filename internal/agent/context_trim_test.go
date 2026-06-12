package agent

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/session"
)

// TestSafeTrimHistory_NoOrphanedToolResults verifies that trimming never leaves
// a tool_result whose tool_use was trimmed away — anywhere in the window, not
// just at the leading boundary. An orphaned tool_result makes the provider
// return HTTP 400. The prior implementation only stripped leading orphans, so a
// tool_result sitting after a user message survived.
func TestSafeTrimHistory_NoOrphanedToolResults(t *testing.T) {
	history := []session.Message{
		{ID: "tu_old", Role: "tool_use", ToolName: "bash"},      // [0] trimmed away
		{Role: "user", Content: "q1"},                           // [1] window start
		{Role: "tool_result", ToolName: "tu_old", Content: "x"}, // [2] mid-window ORPHAN
		{Role: "assistant", Content: "thinking"},                // [3]
		{ID: "tu_ok", Role: "tool_use", ToolName: "bash"},       // [4]
		{Role: "tool_result", ToolName: "tu_ok", Content: "y"},  // [5] valid pair
	}

	got := safeTrimHistory(history, 5) // start = len-maxLen = 1

	// Invariant 1: result starts with a user message (provider API requirement).
	if len(got) == 0 || got[0].Role != "user" {
		t.Fatalf("result must start with a user message; got %+v", got)
	}

	// Invariant 2: no orphaned tool_result remains in the result.
	present := make(map[string]bool)
	for _, m := range got {
		if m.Role == "tool_use" {
			present[m.ID] = true
		}
	}
	for i, m := range got {
		if m.Role == "tool_result" && !present[m.ToolName] {
			t.Errorf("orphaned tool_result at index %d references trimmed tool_use %q", i, m.ToolName)
		}
	}

	// The valid pair must be retained.
	if len(got) == 0 || got[len(got)-1].Role != "tool_result" || got[len(got)-1].ToolName != "tu_ok" {
		t.Errorf("valid tool_result pair should be retained; got %+v", got)
	}
}

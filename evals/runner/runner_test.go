package runner

import (
	"encoding/json"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/replay"
)

func result(errStr string) json.RawMessage {
	b, _ := json.Marshal(toolResult{Error: errStr})
	return b
}

func TestRun(t *testing.T) {
	sessions := []replay.Session{
		{
			SessionID: "s1",
			Tools: []agent.ToolRoundTrip{
				{ToolName: "world_read", Succeeded: true, ResultJSON: result("")},
				{ToolName: "memory", Succeeded: false, ResultJSON: result("execution denied by user")},
				{ToolName: "file_read", Succeeded: false, ResultJSON: result("read /x: is a directory")},
			},
		},
		{
			SessionID: "s2",
			Salvaged:  true,
			Tools: []agent.ToolRoundTrip{
				{ToolName: "memory", Succeeded: false, ResultJSON: result("execution denied by user")},
				{ToolName: "grep_code", Succeeded: false, ResultJSON: result("command timed out after 5s")},
			},
		},
	}

	res := Run(sessions)
	if res.Sessions != 2 {
		t.Fatalf("Sessions = %d, want 2", res.Sessions)
	}
	if res.ToolCalls != 5 {
		t.Fatalf("ToolCalls = %d, want 5", res.ToolCalls)
	}
	if res.Salvaged != 1 {
		t.Fatalf("Salvaged = %d, want 1", res.Salvaged)
	}
	f := res.Failures
	if f.Total != 4 {
		t.Fatalf("failures Total = %d, want 4 (success skipped)", f.Total)
	}
	if f.GovernanceDenied != 2 || f.AgentError != 1 || f.EnvError != 1 {
		t.Fatalf("class split wrong: %+v", f)
	}
	if f.DeniedByTool["memory"] != 2 {
		t.Fatalf("DeniedByTool[memory] = %d, want 2", f.DeniedByTool["memory"])
	}
}

func TestToolError_FallbackOnEmptyPayload(t *testing.T) {
	tr := agent.ToolRoundTrip{ToolName: "x", Succeeded: false} // no ResultJSON
	if got := toolError(tr); got != "unknown failure" {
		t.Fatalf("toolError empty payload = %q, want placeholder", got)
	}
	// A failed call with empty payload still counts as a failure (agent-error).
	res := Run([]replay.Session{{SessionID: "s", Tools: []agent.ToolRoundTrip{tr}}})
	if res.Failures.Total != 1 || res.Failures.AgentError != 1 {
		t.Fatalf("empty-payload failure miscounted: %+v", res.Failures)
	}
}

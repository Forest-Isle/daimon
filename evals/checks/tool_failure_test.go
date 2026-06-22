package checks

import "testing"

func TestClassifyToolError(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want FailureClass
	}{
		{"empty ok", "", ClassOK},
		{"denied", "execution denied by user", ClassGovernanceDenied},
		{"denied variant", "tool execution denied by user", ClassGovernanceDenied},
		{"timeout", "command timed out after 5s", ClassEnvError},
		{"context deadline", "context deadline exceeded", ClassEnvError},
		{"is a directory", "read /x/configs: is a directory", ClassAgentError},
		{"no such file", "open /x/~/.daimon/y: no such file or directory", ClassAgentError},
		{"unknown defaults to agent", "weird unmapped failure", ClassAgentError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyToolError(tc.err); got != tc.want {
				t.Fatalf("ClassifyToolError(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSummarizeFailures(t *testing.T) {
	// Mirrors the real corpus shape from error-analysis-v1.md: governance
	// denials dominate, concentrated on the memory tool.
	failures := []ToolFailure{
		{Tool: "memory", Error: "execution denied by user"},
		{Tool: "memory", Error: "execution denied by user"},
		{Tool: "bash", Error: "execution denied by user"},
		{Tool: "file_read", Error: "read /x/configs: is a directory"},
		{Tool: "grep_code", Error: "command timed out after 5s"},
		{Tool: "world_read", Error: ""}, // success — must be skipped
	}
	s := SummarizeFailures(failures)
	if s.Total != 5 {
		t.Fatalf("Total = %d, want 5 (success skipped)", s.Total)
	}
	if s.GovernanceDenied != 3 || s.AgentError != 1 || s.EnvError != 1 {
		t.Fatalf("class split wrong: %+v", s)
	}
	if s.DeniedByTool["memory"] != 2 || s.DeniedByTool["bash"] != 1 {
		t.Fatalf("DeniedByTool wrong: %+v", s.DeniedByTool)
	}
	if _, ok := s.DeniedByTool["file_read"]; ok {
		t.Fatalf("agent-error tool must not appear in DeniedByTool: %+v", s.DeniedByTool)
	}
}

func TestSummarizeFailures_Empty(t *testing.T) {
	s := SummarizeFailures(nil)
	if s.Total != 0 || len(s.DeniedByTool) != 0 {
		t.Fatalf("empty summary wrong: %+v", s)
	}
}

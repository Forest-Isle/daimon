package agent

import (
	"testing"
)

// TestCognitiveIntegration_ObserverAssertionPipeline tests the full
// OBSERVE phase with the expanded assertion system covering all tool types.
func TestCognitiveIntegration_ObserverAssertionPipeline(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
			{ID: "2", ToolName: "http", Status: SubTaskDone},
			{ID: "3", ToolName: "file_read", Status: SubTaskDone},
			{ID: "4", ToolName: "browser_search", Status: SubTaskDone},
			{ID: "5", ToolName: "mcp_github_search", Status: SubTaskDone},
			{ID: "6", ToolName: "skill_deploy", Status: SubTaskDone},
			{ID: "7", ToolName: "memory_search", Status: SubTaskDone},
		},
	}

	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"ok","stderr":"","exit_code":0,"status":"ok"}`},
		{SubTaskID: "2", ToolName: "http", Output: `{"status_code":200,"body":"ok"}`},
		{SubTaskID: "3", ToolName: "file_read", Output: "file content here"},
		{SubTaskID: "4", ToolName: "browser_search", Output: `{"results":[{"title":"r1"}],"error":""}`},
		{SubTaskID: "5", ToolName: "mcp_github_search", Output: `{"result":{"items":[]}}`},
		{SubTaskID: "6", ToolName: "skill_deploy", Output: "deployed successfully"},
		{SubTaskID: "7", ToolName: "memory_search", Output: `[{"content":"fact"}]`},
	}

	result := obs.Run(observations, plan)

	if result.SuccessCount != 7 {
		t.Errorf("SuccessCount = %d, want 7 (all tools should pass)", result.SuccessCount)
	}
	if result.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", result.FailureCount)
	}
	if len(result.Assertions) == 0 {
		t.Error("expected assertions from expanded tool coverage")
	}

	// Count assertions by tool type to verify coverage
	toolAssertions := make(map[string]int)
	for _, obs := range observations {
		assertions := generateAssertions(Observation{
			SubTaskID: obs.SubTaskID,
			ToolName:  obs.ToolName,
			Output:    obs.Output,
		})
		toolAssertions[obs.ToolName] = len(assertions)
	}

	for tool, count := range toolAssertions {
		if count == 0 {
			t.Errorf("tool %q produced 0 assertions — expected at least 1", tool)
		}
	}

	t.Logf("assertion coverage: %v", toolAssertions)
}

// TestCognitiveIntegration_MixedSuccessFailure tests the observer with a
// mix of passing and failing tools, verifying failure contexts are generated.
func TestCognitiveIntegration_MixedSuccessFailure(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
			{ID: "2", ToolName: "http", Status: SubTaskDone},
			{ID: "3", ToolName: "mcp_api", Status: SubTaskDone},
			{ID: "4", ToolName: "file_read", Status: SubTaskDone},
		},
	}

	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"","stderr":"fatal error","exit_code":1,"status":"failed"}`},
		{SubTaskID: "2", ToolName: "http", Output: `{"status_code":500,"body":"internal error"}`},
		{SubTaskID: "3", ToolName: "mcp_api", Error: "connection refused"},
		{SubTaskID: "4", ToolName: "file_read", Output: "some content"},
	}

	result := obs.Run(observations, plan)

	if result.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1 (only file_read passes)", result.SuccessCount)
	}
	if result.FailureCount != 3 {
		t.Errorf("FailureCount = %d, want 3", result.FailureCount)
	}
	if len(result.Failures) != 3 {
		t.Errorf("len(Failures) = %d, want 3", len(result.Failures))
	}

	// Verify failure types
	failureTypes := make(map[FailureErrorType]int)
	for _, f := range result.Failures {
		failureTypes[f.ErrorType]++
	}

	if failureTypes[FailureAssertionFailed] != 2 {
		t.Errorf("assertion_failed count = %d, want 2 (bash + http)", failureTypes[FailureAssertionFailed])
	}
	if failureTypes[FailureToolError] != 1 {
		t.Errorf("tool_error count = %d, want 1 (mcp)", failureTypes[FailureToolError])
	}
}

// TestCognitiveIntegration_AssertionPassRate verifies the assertion pass rate
// calculation across a mixed set of tools.
func TestCognitiveIntegration_AssertionPassRate(t *testing.T) {
	observations := []Observation{
		{ToolName: "bash", Output: `{"stdout":"ok","stderr":"","exit_code":0,"status":"ok"}`},
		{ToolName: "bash", Output: `{"stdout":"","stderr":"error","exit_code":1,"status":"failed"}`},
		{ToolName: "http", Output: `{"status_code":200,"body":"ok"}`},
		{ToolName: "http", Output: `{"status_code":404,"body":"not found"}`},
		{ToolName: "file_read", Output: "content"},
	}

	totalAssertions := 0
	passedAssertions := 0

	for _, obs := range observations {
		assertions := generateAssertions(obs)
		for _, a := range assertions {
			totalAssertions++
			if a.Passed {
				passedAssertions++
			}
		}
	}

	if totalAssertions == 0 {
		t.Fatal("expected at least some assertions")
	}

	rate := float64(passedAssertions) / float64(totalAssertions)
	t.Logf("assertion pass rate: %d/%d = %.1f%%", passedAssertions, totalAssertions, rate*100)

	if rate >= 1.0 {
		t.Error("expected some failures in the mixed set")
	}
	if rate <= 0.0 {
		t.Error("expected some passes in the mixed set")
	}
}

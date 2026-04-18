package agent

import (
	"testing"
)

func TestGenerateAssertions_Bash_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "s1",
		ToolName:  "bash",
		Input:     "echo hello",
		Output:    `{"stdout":"hello","stderr":"","exit_code":0,"status":"ok"}`,
	}

	results := generateAssertions(obs)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 assertions, got %d", len(results))
	}

	assertCheck(t, results, "exit_code == 0", true)
	assertCheck(t, results, "stderr has no error keywords", true)
}

func TestGenerateAssertions_Bash_Failed(t *testing.T) {
	obs := Observation{
		SubTaskID: "s2",
		ToolName:  "bash",
		Input:     "nosuchcmd",
		Output:    `{"stdout":"","stderr":"command not found","exit_code":127,"status":"failed"}`,
	}

	results := generateAssertions(obs)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 assertions, got %d", len(results))
	}

	assertCheck(t, results, "exit_code == 0", false)
	assertCheck(t, results, "stderr has no error keywords", false)
}

func TestGenerateAssertions_HTTP_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "s3",
		ToolName:  "http",
		Output:    `{"status_code":200,"body":"ok"}`,
	}

	results := generateAssertions(obs)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 assertion, got %d", len(results))
	}

	assertCheck(t, results, "status_code < 400", true)
}

func TestGenerateAssertions_HTTP_ServerError(t *testing.T) {
	obs := Observation{
		SubTaskID: "s4",
		ToolName:  "http",
		Output:    `{"status_code":500,"body":"internal error"}`,
	}

	results := generateAssertions(obs)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 assertion, got %d", len(results))
	}

	assertCheck(t, results, "status_code < 400", false)
}

func TestGenerateAssertions_UnknownTool(t *testing.T) {
	obs := Observation{
		SubTaskID: "s5",
		ToolName:  "custom_tool",
		Output:    "whatever",
	}

	results := generateAssertions(obs)
	if len(results) != 0 {
		t.Fatalf("expected 0 assertions for unknown tool, got %d", len(results))
	}
}

func TestGenerateAssertions_DeniedObservation(t *testing.T) {
	obs := Observation{
		SubTaskID: "s6",
		ToolName:  "bash",
		Output:    "denied",
		Denied:    true,
	}

	results := generateAssertions(obs)
	if len(results) != 0 {
		t.Fatalf("expected 0 assertions for denied observation, got %d", len(results))
	}
}

func TestGenerateAssertions_FileWrite_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "7",
		ToolName:  "file_write",
		Output:    "file written successfully",
		Error:     "",
	}
	results := generateAssertions(obs)
	if len(results) == 0 {
		t.Fatal("expected assertions for file_write")
	}
	assertCheck(t, results, "file operation succeeded", true)
}

func TestGenerateAssertions_FileWrite_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "8",
		ToolName:  "file_write",
		Output:    "",
		Error:     "permission denied: /etc/hosts",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file operation succeeded", false)
}

func TestGenerateAssertions_FileEdit(t *testing.T) {
	obs := Observation{
		SubTaskID: "9",
		ToolName:  "file_edit",
		Output:    "edit applied",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file operation succeeded", true)
}

func TestGenerateAssertions_Bash_InvalidJSON(t *testing.T) {
	obs := Observation{
		SubTaskID: "10",
		ToolName:  "bash",
		Output:    "this is not json",
		Error:     "exit status 1",
	}
	results := generateAssertions(obs)
	if len(results) == 0 {
		t.Fatal("expected at least one assertion for bash with error")
	}
}

func TestGenerateAssertions_HTTP_InvalidJSON(t *testing.T) {
	obs := Observation{
		SubTaskID: "11",
		ToolName:  "http",
		Output:    "<html>not json</html>",
		Error:     "",
	}
	results := generateAssertions(obs)
	for _, r := range results {
		if r.Check == "status_code < 400" && r.Passed {
			t.Error("should not pass status_code check on invalid JSON")
		}
	}
}

func TestObserverRun_PopulatesAssertions(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
			{ID: "2", ToolName: "http", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"ok","stderr":"","exit_code":0,"status":"ok"}`},
		{SubTaskID: "2", ToolName: "http", Output: `{"status_code":500,"body":"err"}`},
	}

	result := obs.Run(observations, plan)

	if len(result.Assertions) == 0 {
		t.Fatal("expected assertions to be populated")
	}
	if len(result.Failures) == 0 {
		t.Fatal("expected at least one failure context (http 500)")
	}

	var httpFailure *FailureContext
	for i := range result.Failures {
		if result.Failures[i].SubTaskID == "2" {
			httpFailure = &result.Failures[i]
		}
	}
	if httpFailure == nil {
		t.Fatal("expected failure context for subtask 2")
	}
	if httpFailure.ErrorType != FailureAssertionFailed {
		t.Errorf("ErrorType = %q, want %q", httpFailure.ErrorType, FailureAssertionFailed)
	}
}

func TestObserverRun_NoAssertionFailures(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Output: `{"stdout":"hello","stderr":"","exit_code":0,"status":"ok"}`},
	}

	result := obs.Run(observations, plan)

	if len(result.Failures) != 0 {
		t.Errorf("expected no failures, got %d", len(result.Failures))
	}
	if result.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", result.SuccessCount)
	}
}

func TestObserverRun_DeniedObservation(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "bash", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "bash", Denied: true},
	}

	result := obs.Run(observations, plan)

	if result.DeniedCount != 1 {
		t.Errorf("DeniedCount = %d, want 1", result.DeniedCount)
	}
	if len(result.Failures) != 1 || result.Failures[0].ErrorType != FailureDenied {
		t.Error("expected denied failure context")
	}
}

// assertCheck finds a result by Check name and verifies its Passed value.
func assertCheck(t *testing.T, results []AssertionResult, check string, wantPassed bool) {
	t.Helper()
	for _, r := range results {
		if r.Check == check {
			if r.Passed != wantPassed {
				t.Errorf("check %q: want passed=%v, got passed=%v (actual=%q)", check, wantPassed, r.Passed, r.Actual)
			}
			return
		}
	}
	t.Errorf("check %q not found in results", check)
}

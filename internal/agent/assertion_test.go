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

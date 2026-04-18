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

func TestGenerateAssertions_HTTP_Success_Metadata(t *testing.T) {
	obs := Observation{
		SubTaskID: "s3",
		ToolName:  "http",
		Output:    "HTTP 200 OK\n\nresponse body",
		Metadata:  map[string]any{"status_code": 200, "content_type": "text/plain"},
	}

	results := generateAssertions(obs)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 assertion, got %d", len(results))
	}

	assertCheck(t, results, "status_code < 400", true)
}

func TestGenerateAssertions_HTTP_Success_PlainText(t *testing.T) {
	obs := Observation{
		SubTaskID: "s3b",
		ToolName:  "http",
		Output:    "HTTP 200 OK\n\nresponse body",
	}

	results := generateAssertions(obs)
	assertCheck(t, results, "status_code < 400", true)
}

func TestGenerateAssertions_HTTP_ServerError_Metadata(t *testing.T) {
	obs := Observation{
		SubTaskID: "s4",
		ToolName:  "http",
		Output:    "HTTP 500 Internal Server Error\n\nerror",
		Metadata:  map[string]any{"status_code": 500},
	}

	results := generateAssertions(obs)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 assertion, got %d", len(results))
	}

	assertCheck(t, results, "status_code < 400", false)
}

func TestGenerateAssertions_HTTP_ServerError_PlainText(t *testing.T) {
	obs := Observation{
		SubTaskID: "s4b",
		ToolName:  "http",
		Output:    "HTTP 500 Internal Server Error\n\nerror",
	}

	results := generateAssertions(obs)
	assertCheck(t, results, "status_code < 400", false)
}

func TestGenerateAssertions_HTTP_LegacyJSON(t *testing.T) {
	obs := Observation{
		SubTaskID: "s4c",
		ToolName:  "http",
		Output:    `{"status_code":200,"body":"ok"}`,
	}

	results := generateAssertions(obs)
	assertCheck(t, results, "status_code < 400", true)
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

func TestGenerateAssertions_HTTP_UnparsableOutput(t *testing.T) {
	obs := Observation{
		SubTaskID: "11",
		ToolName:  "http",
		Output:    "<html>not json and no HTTP prefix</html>",
		Error:     "",
	}
	results := generateAssertions(obs)
	if len(results) == 0 {
		t.Fatal("expected at least one assertion")
	}
	// When status code can't be extracted, falls back to error check.
	assertCheck(t, results, "HTTP response received", true)
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
		{SubTaskID: "2", ToolName: "http", Output: "HTTP 500 Internal Server Error\n\nerr", Metadata: map[string]any{"status_code": 500}},
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

func TestObserverRun_UnknownToolWithError(t *testing.T) {
	obs := NewObserver()
	plan := &TaskPlan{
		SubTasks: []*SubTask{
			{ID: "1", ToolName: "mcp_custom_tool", Status: SubTaskDone},
		},
	}
	observations := []Observation{
		{SubTaskID: "1", ToolName: "mcp_custom_tool", Error: "connection refused"},
	}

	result := obs.Run(observations, plan)

	if result.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", result.FailureCount)
	}
	if result.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", result.SuccessCount)
	}
	if len(result.Failures) != 1 || result.Failures[0].ErrorType != FailureToolError {
		t.Error("expected tool_error failure context")
	}
}

// ---------- file_read assertions ----------

func TestGenerateAssertions_FileRead_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "fr1",
		ToolName:  "file_read",
		Output:    "line1\nline2\nline3",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file read succeeded", true)
	assertCheck(t, results, "file content is non-empty", true)
}

func TestGenerateAssertions_FileRead_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "fr2",
		ToolName:  "file_read",
		Output:    "",
		Error:     "file not found",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file read succeeded", false)
}

func TestGenerateAssertions_FileRead_EmptyContent(t *testing.T) {
	obs := Observation{
		SubTaskID: "fr3",
		ToolName:  "file_read",
		Output:    "   ",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file read succeeded", true)
	assertCheck(t, results, "file content is non-empty", false)
}

// ---------- browser_search assertions ----------

func TestGenerateAssertions_BrowserSearch_Success_Array(t *testing.T) {
	obs := Observation{
		SubTaskID: "bs1",
		ToolName:  "browser_search",
		Output:    `[{"title":"result1","url":"http://a.com","snippet":"..."},{"title":"result2","url":"http://b.com","snippet":"..."}]`,
		Metadata:  map[string]any{"query": "test", "result_count": 2},
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "search succeeded", true)
	assertCheck(t, results, "search returned results", true)
}

func TestGenerateAssertions_BrowserSearch_Success_Metadata(t *testing.T) {
	obs := Observation{
		SubTaskID: "bs1b",
		ToolName:  "browser_search",
		Output:    `[{"title":"r1"}]`,
		Metadata:  map[string]any{"result_count": 1},
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "search returned results", true)
}

func TestGenerateAssertions_BrowserSearch_Empty(t *testing.T) {
	obs := Observation{
		SubTaskID: "bs2",
		ToolName:  "browser_search",
		Output:    `[]`,
		Metadata:  map[string]any{"result_count": 0},
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "search succeeded", true)
	assertCheck(t, results, "search returned results", false)
}

func TestGenerateAssertions_BrowserSearch_LegacyFormat(t *testing.T) {
	obs := Observation{
		SubTaskID: "bs2b",
		ToolName:  "browser_search",
		Output:    `{"results":[{"title":"r1"}],"error":""}`,
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "search returned results", true)
}

func TestGenerateAssertions_BrowserSearch_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "bs3",
		ToolName:  "browser_search",
		Error:     "network timeout",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "search succeeded", false)
}

// ---------- browser_extract assertions ----------

func TestGenerateAssertions_BrowserExtract_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "be1",
		ToolName:  "browser_extract",
		Output:    "# Page Title\n\nSome content here with Markdown formatting.",
		Metadata:  map[string]any{"url": "http://example.com", "page": 1, "total_pages": 1},
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "extract succeeded", true)
	assertCheck(t, results, "extracted content is non-empty", true)
}

func TestGenerateAssertions_BrowserExtract_EmptyContent(t *testing.T) {
	obs := Observation{
		SubTaskID: "be2",
		ToolName:  "browser_extract",
		Output:    "   ",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "extract succeeded", true)
	assertCheck(t, results, "extracted content is non-empty", false)
}

func TestGenerateAssertions_BrowserExtract_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "be3",
		ToolName:  "browser_extract",
		Error:     "failed to fetch URL",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "extract succeeded", false)
}

// ---------- mcp_* assertions ----------

func TestGenerateAssertions_MCP_Success_PlainText(t *testing.T) {
	obs := Observation{
		SubTaskID: "mcp1",
		ToolName:  "mcp_github_search",
		Output:    "Found 5 repositories matching the query.",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "MCP tool succeeded", true)
	assertCheck(t, results, "MCP tool produced output", true)
}

func TestGenerateAssertions_MCP_Success_EmptyOutput(t *testing.T) {
	obs := Observation{
		SubTaskID: "mcp1b",
		ToolName:  "mcp_void_tool",
		Output:    "",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "MCP tool succeeded", true)
	assertCheck(t, results, "MCP tool produced output", false)
}

func TestGenerateAssertions_MCP_ToolError(t *testing.T) {
	obs := Observation{
		SubTaskID: "mcp2",
		ToolName:  "mcp_custom_tool",
		Error:     "connection refused",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "MCP tool succeeded", false)
}

func TestGenerateAssertions_MCP_ResponseError(t *testing.T) {
	obs := Observation{
		SubTaskID: "mcp3",
		ToolName:  "mcp_api_call",
		Output:    `{"error":"invalid token","result":null}`,
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "MCP tool succeeded", true)
	assertCheck(t, results, "MCP response has no error field", false)
}

// ---------- skill_* assertions ----------

func TestGenerateAssertions_Skill_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "sk1",
		ToolName:  "skill_deploy",
		Output:    "deployment completed successfully",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "skill execution succeeded", true)
	assertCheck(t, results, "skill produced output", true)
}

func TestGenerateAssertions_Skill_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "sk2",
		ToolName:  "skill_build",
		Error:     "build failed: syntax error",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "skill execution succeeded", false)
}

func TestGenerateAssertions_ReadSkill(t *testing.T) {
	obs := Observation{
		SubTaskID: "sk3",
		ToolName:  "read_skill",
		Output:    "# Skill Content\nDo these steps...",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "skill execution succeeded", true)
	assertCheck(t, results, "skill produced output", true)
}

// ---------- memory_* assertions ----------

func TestGenerateAssertions_Memory_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "mem1",
		ToolName:  "memory_search",
		Output:    `[{"content":"some memory"}]`,
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "memory operation succeeded", true)
}

func TestGenerateAssertions_Memory_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "mem2",
		ToolName:  "memory_add",
		Error:     "database locked",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "memory operation succeeded", false)
}

// ---------- file_list assertions ----------

func TestGenerateAssertions_FileList_Success(t *testing.T) {
	obs := Observation{
		SubTaskID: "fl1",
		ToolName:  "file_list",
		Output:    "main.go\ngo.mod\ngo.sum\ninternal/",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file list succeeded", true)
	assertCheck(t, results, "listing is non-empty", true)
}

func TestGenerateAssertions_FileList_Empty(t *testing.T) {
	obs := Observation{
		SubTaskID: "fl2",
		ToolName:  "file_list",
		Output:    "",
		Error:     "",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file list succeeded", true)
	assertCheck(t, results, "listing is non-empty", false)
}

func TestGenerateAssertions_FileList_Error(t *testing.T) {
	obs := Observation{
		SubTaskID: "fl3",
		ToolName:  "file_list",
		Error:     "directory not found",
	}
	results := generateAssertions(obs)
	assertCheck(t, results, "file list succeeded", false)
}

// ---------- generic (fallback) assertions ----------

func TestGenerateAssertions_Generic_WithError(t *testing.T) {
	obs := Observation{
		SubTaskID: "gen1",
		ToolName:  "some_new_tool",
		Error:     "unexpected failure",
	}
	results := generateAssertions(obs)
	if len(results) == 0 {
		t.Fatal("expected generic assertion for tool with error")
	}
	assertCheck(t, results, "tool execution succeeded", false)
}

func TestGenerateAssertions_Generic_NoError(t *testing.T) {
	obs := Observation{
		SubTaskID: "gen2",
		ToolName:  "some_new_tool",
		Output:    "ok",
		Error:     "",
	}
	results := generateAssertions(obs)
	if len(results) != 0 {
		t.Errorf("expected no assertions for unknown tool without error, got %d", len(results))
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

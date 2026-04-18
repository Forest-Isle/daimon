package agent

import (
	"strings"
	"testing"
)

func TestEnrichFailureContexts(t *testing.T) {
	original := []FailureContext{
		{SubTaskID: "s1", ToolName: "bash", ErrorType: FailureToolError, ErrorMsg: "exit 1", AttemptCount: 0},
		{SubTaskID: "s2", ToolName: "file_write", ErrorType: FailureAssertionFailed, ErrorMsg: "file not created", AttemptCount: 0},
		{SubTaskID: "s3", ToolName: "http", ErrorType: FailureDenied, ErrorMsg: "blocked", AttemptCount: 1},
	}

	enriched := enrichFailureContexts(original, 2)

	if len(enriched) != 3 {
		t.Fatalf("expected 3 enriched failures, got %d", len(enriched))
	}
	for i, fc := range enriched {
		if fc.AttemptCount != 2 {
			t.Errorf("enriched[%d].AttemptCount = %d, want 2", i, fc.AttemptCount)
		}
	}

	for i, fc := range original {
		if fc.AttemptCount == 2 {
			t.Errorf("original[%d].AttemptCount was mutated to 2; enrichFailureContexts must return a copy", i)
		}
	}
}

func TestFormatFailureContextForPrompt(t *testing.T) {
	failures := []FailureContext{
		{
			SubTaskID:    "s1",
			ToolName:     "bash",
			ErrorType:    FailureToolError,
			ErrorMsg:     "exit code 1",
			AttemptCount: 1,
			Assertions: []AssertionResult{
				{Check: "exit_code == 0", Passed: false, Actual: "exit_code = 1"},
				{Check: "stdout non-empty", Passed: true, Actual: "hello"},
			},
		},
		{
			SubTaskID:    "s2",
			ToolName:     "file_write",
			ErrorType:    FailureAssertionFailed,
			ErrorMsg:     "file not created",
			AttemptCount: 1,
			Assertions: []AssertionResult{
				{Check: "file exists", Passed: false, Actual: "not found"},
			},
		},
	}

	out := formatFailureContextForPrompt(failures)

	for _, want := range []string{
		"FAILURE CONTEXT (structured):",
		"SubTask s1",
		"[bash]",
		"(attempt 1)",
		"tool_error",
		"exit code 1",
		"[PASS] stdout non-empty",
		"[FAIL] exit_code == 0",
		"SubTask s2",
		"[file_write]",
		"assertion_failed",
		"file not created",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted output missing %q\ngot:\n%s", want, out)
		}
	}

	if strings.Contains(out, "WARNING") {
		t.Error("should not contain WARNING when AttemptCount < degradeThreshold")
	}
}

func TestFormatFailureContextForPrompt_Empty(t *testing.T) {
	out := formatFailureContextForPrompt(nil)
	if out != "" {
		t.Errorf("expected empty string for nil failures, got %q", out)
	}
	out = formatFailureContextForPrompt([]FailureContext{})
	if out != "" {
		t.Errorf("expected empty string for empty failures, got %q", out)
	}
}

func TestFormatFailureContextForPrompt_DegradeWarning(t *testing.T) {
	failures := []FailureContext{
		{SubTaskID: "s1", ToolName: "bash", ErrorType: FailureToolError, ErrorMsg: "err", AttemptCount: 3},
	}
	out := formatFailureContextForPrompt(failures)
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING in output when AttemptCount >= degradeThreshold\ngot:\n%s", out)
	}
}

func TestShouldDegradeRetry(t *testing.T) {
	tests := []struct {
		name     string
		failures []FailureContext
		want     bool
	}{
		{
			name:     "empty",
			failures: nil,
			want:     false,
		},
		{
			name: "below threshold",
			failures: []FailureContext{
				{AttemptCount: 1},
				{AttemptCount: 2},
			},
			want: false,
		},
		{
			name: "at threshold",
			failures: []FailureContext{
				{AttemptCount: 3},
			},
			want: true,
		},
		{
			name: "above threshold",
			failures: []FailureContext{
				{AttemptCount: 1},
				{AttemptCount: 5},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDegradeRetry(tt.failures)
			if got != tt.want {
				t.Errorf("shouldDegradeRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}

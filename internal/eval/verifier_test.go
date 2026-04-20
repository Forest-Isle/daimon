package eval

import "testing"

func TestVerifyReference_MustContain(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustContain: []string{"hello", "world"},
		},
	}
	result := VerifyReference(task, "hello world foo")
	if !result.Passed {
		t.Error("should pass — output contains both words")
	}
	if result.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", result.Score)
	}
	if len(result.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(result.Checks))
	}
}

func TestVerifyReference_MustContain_Partial(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustContain: []string{"hello", "missing"},
		},
	}
	result := VerifyReference(task, "hello world")
	if result.Passed {
		t.Error("should fail — 'missing' not found")
	}
	if result.Score != 0.5 {
		t.Errorf("score = %f, want 0.5", result.Score)
	}
}

func TestVerifyReference_MustNotContain(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			MustNotContain: []string{"error", "panic"},
		},
	}
	result := VerifyReference(task, "error occurred")
	if result.Passed {
		t.Error("should fail — output contains 'error'")
	}
}

func TestVerifyReference_FileChecks(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			FileChecks: []FileCheck{
				{Path: "/tmp/ironclaw_verifier_test_nonexistent_12345.txt", MustExist: true},
			},
		},
	}
	result := VerifyReference(task, "")
	if result.Passed {
		t.Error("should fail — file does not exist")
	}
	if result.Checks[0].Passed {
		t.Error("file check should fail")
	}
}

func TestVerifyReference_ExitCode(t *testing.T) {
	exitCode := 0
	task := TaskCase{
		Reference: &Reference{
			ExitCode: &exitCode,
		},
	}
	result := VerifyReference(task, `{"exit_code": 0, "stdout": "ok"}`)
	if !result.Passed {
		t.Error("should pass — exit_code matches")
	}
}

func TestVerifyReference_NilReference(t *testing.T) {
	task := TaskCase{}
	result := VerifyReference(task, "anything")
	if result == nil {
		t.Fatal("should return non-nil result even without reference")
	}
	if !result.Passed {
		t.Error("no reference means verification passes vacuously")
	}
	if result.Score != 1.0 {
		t.Errorf("no-reference score should be 1.0, got %f", result.Score)
	}
}

func TestVerifyReference_AnswerMatch(t *testing.T) {
	task := TaskCase{
		Reference: &Reference{
			Answer: "42",
		},
	}
	result := VerifyReference(task, "The answer is 42.")
	if !result.Passed {
		t.Error("should pass — output contains reference answer")
	}
}

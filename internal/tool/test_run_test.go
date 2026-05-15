package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectTestCommand_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	got := detectTestCommand(dir)
	if got != "go test ./..." {
		t.Fatalf("detectTestCommand() = %q, want %q", got, "go test ./...")
	}
}

func TestTestRun_ExecuteSimpleCommand(t *testing.T) {
	tool := NewTestRunTool(t.TempDir())
	input, _ := json.Marshal(testRunInput{Command: `echo "PASS"`})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var out testRunOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if !out.Success {
		t.Fatal("success = false, want true")
	}
	if out.Passed != 1 || out.Failed != 0 {
		t.Fatalf("passed/failed = %d/%d, want 1/0", out.Passed, out.Failed)
	}
	if out.Output != "PASS\n" {
		t.Fatalf("output = %q, want %q", out.Output, "PASS\n")
	}
}

func TestParseGoFailureOutput(t *testing.T) {
	output := strings.Join([]string{
		"=== RUN   TestFoo",
		"--- FAIL: TestFoo (0.00s)",
		"    foo_test.go:42: expected X got Y",
		"FAIL",
	}, "\n")

	parsed := parseTestOutput(output, false)
	if parsed.Failed != 1 {
		t.Fatalf("failed = %d, want 1", parsed.Failed)
	}
	if len(parsed.Failures) != 1 {
		t.Fatalf("len(failures) = %d, want 1", len(parsed.Failures))
	}
	if parsed.Failures[0].Name != "TestFoo" {
		t.Fatalf("failure name = %q, want %q", parsed.Failures[0].Name, "TestFoo")
	}
	if parsed.Failures[0].File != "foo_test.go:42" {
		t.Fatalf("failure file = %q, want %q", parsed.Failures[0].File, "foo_test.go:42")
	}
	if !strings.Contains(parsed.Failures[0].Message, "expected X got Y") {
		t.Fatalf("failure message = %q, want it to contain expected text", parsed.Failures[0].Message)
	}
}

func TestTestRun_Timeout(t *testing.T) {
	tool := NewTestRunTool(t.TempDir())
	input, _ := json.Marshal(testRunInput{
		Command:        `sleep 2`,
		TimeoutSeconds: 1,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Fatalf("result.Error = %q, want timeout", result.Error)
	}

	var out testRunOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !out.TimedOut {
		t.Fatal("timed_out = false, want true")
	}
	if out.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", out.ExitCode)
	}
}

func TestTestRun_CommandNotFound(t *testing.T) {
	tool := NewTestRunTool(t.TempDir())
	input, _ := json.Marshal(testRunInput{Command: "definitely-not-a-real-command-ironclaw"})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected result error for missing command")
	}

	var out testRunOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if out.ExitCode == 0 {
		t.Fatalf("exit_code = %d, want non-zero", out.ExitCode)
	}
	if len(out.Failures) == 0 {
		t.Fatal("expected parsed failures for missing command")
	}
}

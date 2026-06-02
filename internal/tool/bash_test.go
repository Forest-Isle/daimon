package tool

import (
	"context"
	"encoding/json"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBashTool_StructuredOutput_Success(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "echo hello"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}

	var out bashOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if out.Status != "ok" {
		t.Errorf("status = %q, want %q", out.Status, "ok")
	}
	if out.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", out.ExitCode)
	}
	if out.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", out.Stdout, "hello\n")
	}
	if out.Stderr != "" {
		t.Errorf("stderr = %q, want empty", out.Stderr)
	}
	if out.Truncated {
		t.Error("truncated should be false")
	}
	if out.DurationMs < 0 {
		t.Errorf("duration_ms = %d, want >= 0", out.DurationMs)
	}

	if result.Metadata == nil {
		t.Fatal("metadata is nil")
	}
	if result.Metadata["exit_code"] != 0 {
		t.Errorf("metadata exit_code = %v, want 0", result.Metadata["exit_code"])
	}
	if result.Metadata["status"] != "ok" {
		t.Errorf("metadata status = %v, want ok", result.Metadata["status"])
	}
}

func TestBashTool_StructuredOutput_Failure(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "exit 42"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out bashOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if out.Status != "failed" {
		t.Errorf("status = %q, want %q", out.Status, "failed")
	}
	if out.ExitCode != 42 {
		t.Errorf("exit_code = %d, want 42", out.ExitCode)
	}
	if !strings.Contains(result.Error, "exit code 42") {
		t.Errorf("result.Error = %q, want it to contain %q", result.Error, "exit code 42")
	}
}

func TestBashTool_StructuredOutput_Stderr(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "echo out && echo err >&2"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out bashOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if out.Stdout != "out\n" {
		t.Errorf("stdout = %q, want %q", out.Stdout, "out\n")
	}
	if out.Stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", out.Stderr, "err\n")
	}
	if out.Status != "ok" {
		t.Errorf("status = %q, want %q", out.Status, "ok")
	}
}

func TestBashTool_Timeout(t *testing.T) {
	bt := NewBashTool(100*time.Millisecond, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: "sleep 10"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("result.Error = %q, want it to contain %q", result.Error, "timed out")
	}
}

func TestBashTool_LargeOutput_Truncated(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	// Generate >8KB of output (each line ~80 chars, 200 lines ≈ 16KB)
	input, _ := json.Marshal(bashInput{Command: "for i in $(seq 1 200); do printf '%0*d\\n' 80 $i; done"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out bashOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, result.Output)
	}

	if !out.Truncated {
		t.Error("truncated should be true for large output")
	}
	if out.FilePath == "" {
		t.Error("file_path should be set for truncated output")
	}
	if !result.IsPartial {
		t.Error("result.IsPartial should be true for truncated output")
	}

	// Verify the temp file exists and contains valid JSON
	if out.FilePath != "" {
		data, err := os.ReadFile(out.FilePath)
		if err != nil {
			t.Fatalf("cannot read temp file %s: %v", out.FilePath, err)
		}
		var fullOut bashOutput
		if err := json.Unmarshal(data, &fullOut); err != nil {
			t.Fatalf("temp file is not valid JSON: %v", err)
		}
		if fullOut.Truncated {
			t.Error("full output in temp file should not be truncated")
		}
		_ = os.Remove(out.FilePath)
	}
}

func TestBashTool_InvalidInput(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))

	result, err := bt.Execute(context.Background(), []byte("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "invalid input") {
		t.Errorf("result.Error = %q, want it to contain %q", result.Error, "invalid input")
	}
}

func TestBashTool_EmptyCommand(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy(nil))
	input, _ := json.Marshal(bashInput{Command: ""})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "command is required") {
		t.Errorf("result.Error = %q, want it to contain %q", result.Error, "command is required")
	}
}

func TestBashTool_PolicyBlocked(t *testing.T) {
	bt := NewBashTool(10*time.Second, false, NewPolicy([]string{"rm -rf"}))
	input, _ := json.Marshal(bashInput{Command: "rm -rf /"})

	result, err := bt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "blocked") {
		t.Errorf("result.Error = %q, want it to contain %q", result.Error, "blocked")
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"long string truncated", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero maxLen", "hello", 0, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := util.TruncateStr(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("util.TruncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

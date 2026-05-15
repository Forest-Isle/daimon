package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/util"
)

const (
	defaultTestTimeout    = 120 * time.Second
	defaultMaxOutputLines = 200
	maxFailureMessageLen  = 512
)

var (
	goFailLineRE   = regexp.MustCompile(`^--- FAIL: ([^ ]+)`)
	goFileLineRE   = regexp.MustCompile(`^\s+([^\s:]+_test\.go:\d+):\s*(.*)$`)
	makeTestLineRE = regexp.MustCompile(`(?m)^test\s*:`)
)

type TestRunTool struct {
	workingDir string
}

type testRunInput struct {
	Command        string `json:"command"`
	WorkingDir     string `json:"working_dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxOutputLines int    `json:"max_output_lines"`
}

type testFailure struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
}

type testRunOutput struct {
	Success     bool          `json:"success"`
	ExitCode    int           `json:"exit_code"`
	TotalTests  int           `json:"total_tests"`
	Passed      int           `json:"passed"`
	Failed      int           `json:"failed"`
	Failures    []testFailure `json:"failures"`
	Summary     string        `json:"summary"`
	Command     string        `json:"command"`
	Output      string        `json:"output"`
	Truncated   bool          `json:"truncated,omitempty"`
	DurationMs  int64         `json:"duration_ms"`
	WorkingDir  string        `json:"working_dir"`
	TimedOut    bool          `json:"timed_out,omitempty"`
	EmptyOutput bool          `json:"empty_output,omitempty"`
}

func NewTestRunTool(workingDir string) *TestRunTool {
	return &TestRunTool{workingDir: workingDir}
}

func (t *TestRunTool) Name() string { return "test_run" }
func (t *TestRunTool) Description() string {
	return "Run test commands and return structured failure output for auto-fixing."
}
func (t *TestRunTool) RequiresApproval() bool { return false }
func (t *TestRunTool) IsReadOnly() bool       { return true }
func (t *TestRunTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
	}
}

func (t *TestRunTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Test command to run. Defaults to an auto-detected test command such as 'go test ./...'.",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Optional working directory override. Defaults to the tool working directory.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in seconds. Defaults to 120.",
			},
			"max_output_lines": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of output lines to capture. Defaults to 200.",
			},
		},
	}
}

func (t *TestRunTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in testRunInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	workingDir := t.workingDir
	if in.WorkingDir != "" {
		workingDir = in.WorkingDir
	}
	if workingDir == "" {
		workingDir = "."
	}

	command := strings.TrimSpace(in.Command)
	if command == "" {
		command = detectTestCommand(workingDir)
	}
	if command == "" {
		return Result{Error: "command is required or auto-detection failed"}, nil
	}

	timeout := defaultTestTimeout
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	maxLines := defaultMaxOutputLines
	if in.MaxOutputLines > 0 {
		maxLines = in.MaxOutputLines
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	cmd.Dir = workingDir

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	start := time.Now()
	runErr := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	rawOutput := combined.String()
	truncatedOutput, truncated := truncateOutputLines(rawOutput, maxLines)

	exitCode := 0
	timedOut := false
	if runCtx.Err() == context.DeadlineExceeded {
		timedOut = true
		exitCode = -1
	} else if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 127
		}
	}

	parsed := parseTestOutput(rawOutput, exitCode == 0)
	if timedOut {
		parsed.Failed++
		parsed.TotalTests = max(parsed.TotalTests, parsed.Failed)
		parsed.Failures = append(parsed.Failures, testFailure{
			Name:    "timeout",
			Message: fmt.Sprintf("test command timed out after %s", timeout),
		})
	}
	if runErr != nil && !timedOut && exitCode == 127 && len(parsed.Failures) == 0 {
		message := strings.TrimSpace(rawOutput)
		if message == "" {
			message = runErr.Error()
		}
		parsed.Failed++
		parsed.TotalTests = max(parsed.TotalTests, parsed.Failed)
		parsed.Failures = append(parsed.Failures, testFailure{
			Name:    "command",
			Message: message,
		})
	}

	if parsed.TotalTests == 0 {
		if exitCode == 0 && !timedOut {
			parsed.Passed = 1
			parsed.TotalTests = 1
		} else if parsed.Failed > 0 {
			parsed.TotalTests = parsed.Failed
		}
	}

	out := testRunOutput{
		Success:     exitCode == 0 && !timedOut,
		ExitCode:    exitCode,
		TotalTests:  parsed.TotalTests,
		Passed:      parsed.Passed,
		Failed:      parsed.Failed,
		Failures:    parsed.Failures,
		Summary:     fmt.Sprintf("%d passed, %d failed", parsed.Passed, parsed.Failed),
		Command:     command,
		Output:      truncatedOutput,
		Truncated:   truncated,
		DurationMs:  durationMs,
		WorkingDir:  workingDir,
		TimedOut:    timedOut,
		EmptyOutput: strings.TrimSpace(rawOutput) == "",
	}

	outputJSON, _ := json.Marshal(out)
	result := Result{
		Output: string(outputJSON),
		Metadata: map[string]any{
			"success":     out.Success,
			"exit_code":   out.ExitCode,
			"total_tests": out.TotalTests,
			"passed":      out.Passed,
			"failed":      out.Failed,
			"failures":    out.Failures,
			"summary":     out.Summary,
			"command":     out.Command,
		},
		IsPartial: truncated,
	}

	switch {
	case timedOut:
		result.Error = fmt.Sprintf("test command timed out after %s", timeout)
	case runErr != nil:
		result.Error = fmt.Sprintf("exit code %d", exitCode)
	}

	return result, nil
}

func detectTestCommand(workingDir string) string {
	type candidate struct {
		name string
		fn   func(string) bool
		cmd  string
	}

	candidates := []candidate{
		{name: "go.mod", fn: fileExists, cmd: "go test ./..."},
		{name: "package.json", fn: hasNPMTestScript, cmd: "npm test"},
		{name: "Cargo.toml", fn: fileExists, cmd: "cargo test"},
		{name: "Makefile", fn: hasMakeTestTarget, cmd: "make test"},
	}

	for _, c := range candidates {
		if c.fn(filepath.Join(workingDir, c.name)) {
			return c.cmd
		}
	}
	return ""
}

type parsedTestOutput struct {
	TotalTests int
	Passed     int
	Failed     int
	Failures   []testFailure
}

func parseTestOutput(output string, success bool) parsedTestOutput {
	lines := scanLines(output)
	failures := parseGoFailures(lines)
	if len(failures) == 0 {
		failures = parseGenericFailures(lines)
	}

	result := parsedTestOutput{
		Failed:   len(failures),
		Failures: failures,
	}

	if len(failures) > 0 {
		result.TotalTests = len(failures)
	}

	if success {
		passCount := countLinesContaining(lines, "PASS")
		if passCount == 0 {
			passCount = 1
		}
		result.Passed = passCount
		result.TotalTests += passCount
	}

	return result
}

func parseGoFailures(lines []string) []testFailure {
	var failures []testFailure
	var current *testFailure

	for _, line := range lines {
		if match := goFailLineRE.FindStringSubmatch(line); len(match) == 2 {
			if current != nil {
				failures = append(failures, finalizeFailure(*current))
			}
			current = &testFailure{Name: match[1]}
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "--- PASS:") || strings.HasPrefix(line, "--- FAIL:") || strings.HasPrefix(line, "FAIL") {
			failures = append(failures, finalizeFailure(*current))
			current = nil
			if strings.HasPrefix(line, "--- FAIL:") {
				if match := goFailLineRE.FindStringSubmatch(line); len(match) == 2 {
					current = &testFailure{Name: match[1]}
				}
			}
			continue
		}

		if match := goFileLineRE.FindStringSubmatch(line); len(match) == 3 {
			current.File = match[1]
			appendMessage(current, match[2])
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			appendMessage(current, trimmed)
		}
	}

	if current != nil {
		failures = append(failures, finalizeFailure(*current))
	}

	return failures
}

func parseGenericFailures(lines []string) []testFailure {
	var failures []testFailure
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !isFailureLine(trimmed) {
			continue
		}
		failures = append(failures, finalizeFailure(testFailure{
			Name:    "failure",
			Message: trimmed,
		}))
	}
	return failures
}

func isFailureLine(line string) bool {
	upper := strings.ToUpper(line)
	return strings.Contains(upper, "FAIL") ||
		strings.Contains(upper, "FAILED") ||
		strings.Contains(line, "Error:") ||
		strings.Contains(strings.ToLower(line), "assertion failed")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasNPMTestScript(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	script, ok := pkg.Scripts["test"]
	return ok && strings.TrimSpace(script) != ""
}

func hasMakeTestTarget(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return makeTestLineRE.Match(data)
}

func truncateOutputLines(output string, maxLines int) (string, bool) {
	if maxLines <= 0 {
		maxLines = defaultMaxOutputLines
	}
	lines := scanLines(output)
	if len(lines) <= maxLines {
		return util.TruncateStr(output, maxOutputSize), len(output) > maxOutputSize
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	if output != "" {
		truncated += fmt.Sprintf("\n[truncated to %d lines]", maxLines)
	}
	return util.TruncateStr(truncated, maxOutputSize), true
}

func scanLines(s string) []string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	lines := make([]string, 0, 32)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func countLinesContaining(lines []string, needle string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, needle) {
			count++
		}
	}
	return count
}

func appendMessage(f *testFailure, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	if f.Message == "" {
		f.Message = msg
	} else {
		f.Message += "\n" + msg
	}
}

func finalizeFailure(f testFailure) testFailure {
	if f.Name == "" {
		f.Name = "failure"
	}
	if f.Message == "" {
		f.Message = "test failed"
	}
	f.Message = util.TruncateStr(f.Message, maxFailureMessageLen)
	return f
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

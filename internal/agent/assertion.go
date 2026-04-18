package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

var stderrErrorKeywords = []string{
	"error", "fatal", "panic", "not found", "permission denied",
	"segfault", "traceback", "exception",
}

func generateAssertions(obs Observation) []AssertionResult {
	if obs.Denied {
		return nil
	}
	switch obs.ToolName {
	case "bash":
		return bashAssertions(obs)
	case "http":
		return httpAssertions(obs)
	case "file_write", "file_edit":
		return fileWriteAssertions(obs)
	default:
		return nil
	}
}

type bashOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Status   string `json:"status"`
}

func bashAssertions(obs Observation) []AssertionResult {
	var out bashOutput
	if err := json.Unmarshal([]byte(obs.Output), &out); err != nil {
		return []AssertionResult{{
			Check:  "output is valid JSON",
			Passed: false,
			Actual: truncate(obs.Output, 200),
		}}
	}

	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "exit_code == 0",
		Passed: out.ExitCode == 0,
		Actual: fmt.Sprintf("exit_code = %d", out.ExitCode),
	})

	stderrLower := strings.ToLower(out.Stderr)
	var matched []string
	for _, kw := range stderrErrorKeywords {
		if strings.Contains(stderrLower, kw) {
			matched = append(matched, kw)
		}
	}
	results = append(results, AssertionResult{
		Check:  "stderr has no error keywords",
		Passed: len(matched) == 0,
		Actual: stderrActual(out.Stderr, matched),
	})

	return results
}

func stderrActual(stderr string, matched []string) string {
	if len(matched) == 0 {
		return "stderr clean"
	}
	return fmt.Sprintf("found [%s] in stderr: %s",
		strings.Join(matched, ", "), truncate(stderr, 120))
}

type httpOutput struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

func httpAssertions(obs Observation) []AssertionResult {
	var out httpOutput
	if err := json.Unmarshal([]byte(obs.Output), &out); err != nil {
		return []AssertionResult{{
			Check:  "output is valid JSON",
			Passed: false,
			Actual: truncate(obs.Output, 200),
		}}
	}

	return []AssertionResult{{
		Check:  "status_code < 400",
		Passed: out.StatusCode < 400,
		Actual: fmt.Sprintf("status_code = %d", out.StatusCode),
	}}
}

func fileWriteAssertions(obs Observation) []AssertionResult {
	if obs.Error == "" {
		return []AssertionResult{{
			Check:  "file operation succeeded",
			Passed: true,
			Actual: "no error",
		}}
	}
	return []AssertionResult{{
		Check:  "file operation succeeded",
		Passed: false,
		Actual: truncate(obs.Error, 200),
	}}
}


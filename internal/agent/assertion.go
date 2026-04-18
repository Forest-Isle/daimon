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
	switch {
	case obs.ToolName == "bash":
		return bashAssertions(obs)
	case obs.ToolName == "http":
		return httpAssertions(obs)
	case obs.ToolName == "file_write" || obs.ToolName == "file_edit":
		return fileWriteAssertions(obs)
	case obs.ToolName == "file_read":
		return fileReadAssertions(obs)
	case obs.ToolName == "browser_search":
		return browserSearchAssertions(obs)
	case obs.ToolName == "browser_extract":
		return browserExtractAssertions(obs)
	case strings.HasPrefix(obs.ToolName, "mcp_"):
		return mcpAssertions(obs)
	case strings.HasPrefix(obs.ToolName, "skill_") || obs.ToolName == "read_skill":
		return skillAssertions(obs)
	case strings.HasPrefix(obs.ToolName, "memory_"):
		return memoryAssertions(obs)
	default:
		return genericAssertions(obs)
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
		Passed: out.StatusCode >= 100 && out.StatusCode < 400,
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

func fileReadAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "file read succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		results = append(results, AssertionResult{
			Check:  "file content is non-empty",
			Passed: len(strings.TrimSpace(obs.Output)) > 0,
			Actual: fmt.Sprintf("output length = %d", len(obs.Output)),
		})
	}

	return results
}

type browserSearchOutput struct {
	Results []json.RawMessage `json:"results"`
	Error   string            `json:"error"`
}

func browserSearchAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "search succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		var out browserSearchOutput
		if err := json.Unmarshal([]byte(obs.Output), &out); err == nil {
			results = append(results, AssertionResult{
				Check:  "search returned results",
				Passed: len(out.Results) > 0 && out.Error == "",
				Actual: fmt.Sprintf("results=%d, error=%q", len(out.Results), out.Error),
			})
		}
	}

	return results
}

type browserExtractOutput struct {
	Content string `json:"content"`
	Error   string `json:"error"`
}

func browserExtractAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "extract succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		var out browserExtractOutput
		if err := json.Unmarshal([]byte(obs.Output), &out); err == nil {
			results = append(results, AssertionResult{
				Check:  "extracted content is non-empty",
				Passed: len(strings.TrimSpace(out.Content)) > 0 && out.Error == "",
				Actual: fmt.Sprintf("content_len=%d, error=%q", len(out.Content), out.Error),
			})
		}
	}

	return results
}

type mcpOutput struct {
	Error  string `json:"error"`
	Result json.RawMessage `json:"result"`
}

func mcpAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "MCP tool succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" && obs.Output != "" {
		var out mcpOutput
		if err := json.Unmarshal([]byte(obs.Output), &out); err == nil && out.Error != "" {
			results = append(results, AssertionResult{
				Check:  "MCP response has no error field",
				Passed: false,
				Actual: truncate(out.Error, 200),
			})
		}
	}

	return results
}

func skillAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "skill execution succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		results = append(results, AssertionResult{
			Check:  "skill produced output",
			Passed: len(strings.TrimSpace(obs.Output)) > 0,
			Actual: fmt.Sprintf("output length = %d", len(obs.Output)),
		})
	}

	return results
}

func memoryAssertions(obs Observation) []AssertionResult {
	return []AssertionResult{{
		Check:  "memory operation succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	}}
}

// genericAssertions provides a minimal check for any tool not covered by
// a specific handler: if the observation carries an error it fails.
func genericAssertions(obs Observation) []AssertionResult {
	if obs.Error == "" {
		return nil
	}
	return []AssertionResult{{
		Check:  "tool execution succeeded",
		Passed: false,
		Actual: truncate(obs.Error, 200),
	}}
}

func errorOrOK(errMsg string) string {
	if errMsg == "" {
		return "no error"
	}
	return truncate(errMsg, 200)
}


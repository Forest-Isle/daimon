package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/util"
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
	case obs.ToolName == "file_list":
		return fileListAssertions(obs)
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
			Actual: util.TruncateStr(obs.Output, 200),
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

	// Content-level assertions: detect wrong output despite exit_code=0
	results = append(results, bashOutputContentAssertions(out)...)

	return results
}

// bashOutputContentAssertions detects common patterns of incorrect bash output:
// empty stdout when output is expected, or stdout containing error-like patterns
// despite a zero exit code (command ran but produced wrong output).
func bashOutputContentAssertions(out bashOutput) []AssertionResult {
	var results []AssertionResult

	// Flag empty stdout on successful exit — likely command ran but produced nothing.
	if out.ExitCode == 0 && strings.TrimSpace(out.Stdout) == "" && strings.TrimSpace(out.Stderr) == "" {
		results = append(results, AssertionResult{
			Check:  "bash stdout is non-empty on success",
			Passed: false,
			Actual: "exit_code=0 but stdout and stderr are both empty — verify command produced expected output",
		})
	}

	// Flag stdout that contains error-like patterns despite exit_code=0.
	// This catches commands that print errors to stdout (non-standard but common).
	if out.ExitCode == 0 && out.Stdout != "" {
		stdoutLower := strings.ToLower(out.Stdout)
		errorPatterns := []string{"error:", "fatal:", "failed:", "exception:", "traceback", "command not found"}
		for _, pat := range errorPatterns {
			if strings.Contains(stdoutLower, pat) {
				results = append(results, AssertionResult{
					Check:  "bash stdout does not contain error patterns",
					Passed: false,
					Actual: fmt.Sprintf("stdout contains '%s' despite exit_code=0: %s", pat, util.TruncateStr(out.Stdout, 100)),
				})
				break
			}
		}
	}

	return results
}

func stderrActual(stderr string, matched []string) string {
	if len(matched) == 0 {
		return "stderr clean"
	}
	return fmt.Sprintf("found [%s] in stderr: %s",
		strings.Join(matched, ", "), util.TruncateStr(stderr, 120))
}

func httpAssertions(obs Observation) []AssertionResult {
	statusCode := 0

	// Prefer structured metadata from tool.Result.Metadata (always available
	// when the HTTP tool populates it).
	if sc, ok := metadataInt(obs.Metadata, "status_code"); ok {
		statusCode = sc
	} else {
		// Fallback: parse "HTTP 200 OK\n..." plain-text output format.
		if _, err := fmt.Sscanf(obs.Output, "HTTP %d ", &statusCode); err != nil {
			// Last resort: try legacy JSON envelope.
			var legacy struct {
				StatusCode int `json:"status_code"`
			}
			if jsonErr := json.Unmarshal([]byte(obs.Output), &legacy); jsonErr == nil {
				statusCode = legacy.StatusCode
			}
		}
	}

	if statusCode == 0 {
		return []AssertionResult{{
			Check:  "HTTP response received",
			Passed: obs.Error == "",
			Actual: errorOrOK(obs.Error),
		}}
	}

	return []AssertionResult{{
		Check:  "status_code < 400",
		Passed: statusCode >= 100 && statusCode < 400,
		Actual: fmt.Sprintf("status_code = %d", statusCode),
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
		Actual: util.TruncateStr(obs.Error, 200),
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
		trimmed := strings.TrimSpace(obs.Output)
		results = append(results, AssertionResult{
			Check:  "file content is non-empty",
			Passed: len(trimmed) > 0,
			Actual: fmt.Sprintf("output length = %d", len(obs.Output)),
		})

		// For file_read: detect if content looks like an error or is suspiciously short for a code/config file
		if len(trimmed) > 0 && len(trimmed) < 10 {
			results = append(results, AssertionResult{
				Check:  "file content is substantive",
				Passed: false,
				Actual: fmt.Sprintf("file content is very short (%d chars): '%s'", len(trimmed), trimmed),
			})
		}
	}

	return results
}

func browserSearchAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "search succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		resultCount := -1

		// Prefer metadata from tool.Result.Metadata.
		if rc, ok := metadataInt(obs.Metadata, "result_count"); ok {
			resultCount = rc
		} else {
			// Tool output is a bare JSON array of search results.
			var arr []json.RawMessage
			if err := json.Unmarshal([]byte(obs.Output), &arr); err == nil {
				resultCount = len(arr)
			} else {
				// Legacy: try object with "results" field.
				var legacy struct {
					Results []json.RawMessage `json:"results"`
				}
				if jsonErr := json.Unmarshal([]byte(obs.Output), &legacy); jsonErr == nil {
					resultCount = len(legacy.Results)
				}
			}
		}

		if resultCount >= 0 {
			results = append(results, AssertionResult{
				Check:  "search returned results",
				Passed: resultCount > 0,
				Actual: fmt.Sprintf("result_count=%d", resultCount),
			})
		}
	}

	return results
}

func browserExtractAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "extract succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		// Tool output is raw Markdown content (not a JSON envelope).
		content := strings.TrimSpace(obs.Output)
		results = append(results, AssertionResult{
			Check:  "extracted content is non-empty",
			Passed: len(content) > 0,
			Actual: fmt.Sprintf("content_len=%d", len(content)),
		})
	}

	return results
}

func mcpAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "MCP tool succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		// MCP adapter joins TextContent blocks into plain text.
		// Also check for a JSON error field if the output happens to be JSON.
		hasError := false
		if obs.Output != "" {
			var envelope struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(obs.Output), &envelope); err == nil && envelope.Error != "" {
				hasError = true
				results = append(results, AssertionResult{
					Check:  "MCP response has no error field",
					Passed: false,
					Actual: util.TruncateStr(envelope.Error, 200),
				})
			}
		}
		if !hasError {
			results = append(results, AssertionResult{
				Check:  "MCP tool produced output",
				Passed: len(strings.TrimSpace(obs.Output)) > 0,
				Actual: fmt.Sprintf("output_len=%d", len(obs.Output)),
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
		Actual: util.TruncateStr(obs.Error, 200),
	}}
}

func fileListAssertions(obs Observation) []AssertionResult {
	results := make([]AssertionResult, 0, 2)

	results = append(results, AssertionResult{
		Check:  "file list succeeded",
		Passed: obs.Error == "",
		Actual: errorOrOK(obs.Error),
	})

	if obs.Error == "" {
		results = append(results, AssertionResult{
			Check:  "listing is non-empty",
			Passed: len(strings.TrimSpace(obs.Output)) > 0,
			Actual: fmt.Sprintf("output_len=%d", len(obs.Output)),
		})
	}

	return results
}

// metadataInt extracts an integer value from an Observation's Metadata map.
// Handles both int and float64 (JSON number unmarshalling).
func metadataInt(meta map[string]any, key string) (int, bool) {
	if meta == nil {
		return 0, false
	}
	v, ok := meta[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func errorOrOK(errMsg string) string {
	if errMsg == "" {
		return "no error"
	}
	return util.TruncateStr(errMsg, 200)
}


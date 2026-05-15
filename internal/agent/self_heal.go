package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

type ErrorCategory int

const (
	ErrorFixable ErrorCategory = iota
	ErrorNeedsReplan
	ErrorNeedsHuman
)

func (c ErrorCategory) String() string {
	switch c {
	case ErrorFixable:
		return "fixable"
	case ErrorNeedsReplan:
		return "needs_replan"
	case ErrorNeedsHuman:
		return "needs_human"
	default:
		return "unknown"
	}
}

type HealAction struct {
	ToolName    string
	ToolInput   string
	Description string
	IsRetry     bool
}

type HealResult struct {
	OriginalError string
	Category      ErrorCategory
	HealAttempted bool
	HealAction    string
	HealOutput    string
	HealError     string
	RetryResult   *Observation
	Resolved      bool
	EscalatedTo   string
}

type HealPattern struct {
	Name        string
	Match       func(errStr string) bool
	BuildAction func(errStr, toolName, originalInput string) *HealAction
}

type SelfHealEngine struct {
	toolRegistry *tool.Registry
	maxAttempts  int
	patterns     []HealPattern
}

func NewSelfHealEngine(reg *tool.Registry) *SelfHealEngine {
	return &SelfHealEngine{
		toolRegistry: reg,
		maxAttempts:  1,
		patterns:     builtinPatterns(),
	}
}

func (e *SelfHealEngine) ProcessFailures(ctx context.Context, obsResult *ObservationResult, plan *TaskPlan) int {
	if e == nil || e.toolRegistry == nil || obsResult == nil {
		return 0
	}
	_ = plan

	healedCount := 0
	remainingFailures := make([]FailureContext, 0, len(obsResult.Failures))

	for _, failure := range obsResult.Failures {
		if e.maxAttempts > 0 && failure.AttemptCount >= e.maxAttempts {
			remainingFailures = append(remainingFailures, failure)
			continue
		}

		if e.ClassifyError(failure.ErrorMsg) != ErrorFixable {
			remainingFailures = append(remainingFailures, failure)
			continue
		}

		var originalObs *Observation
		for i := range obsResult.Observations {
			if obsResult.Observations[i].SubTaskID == failure.SubTaskID {
				originalObs = &obsResult.Observations[i]
				break
			}
		}
		if originalObs == nil {
			remainingFailures = append(remainingFailures, failure)
			continue
		}

		healResult := e.heal(ctx, failure.ErrorMsg, originalObs.ToolName, originalObs.Input)
		slog.Info("self-heal: processed failure",
			"subtask", failure.SubTaskID,
			"tool", failure.ToolName,
			"category", healResult.Category.String(),
			"attempted", healResult.HealAttempted,
			"resolved", healResult.Resolved,
			"escalated_to", healResult.EscalatedTo,
		)

		if !healResult.Resolved || healResult.RetryResult == nil {
			remainingFailures = append(remainingFailures, failure)
			continue
		}

		healedCount++
		for i := range obsResult.Observations {
			if obsResult.Observations[i].SubTaskID == failure.SubTaskID {
				retryObs := *healResult.RetryResult
				retryObs.SubTaskID = failure.SubTaskID
				obsResult.Observations[i] = retryObs
				break
			}
		}
	}

	if healedCount > 0 {
		if obsResult.FailureCount >= healedCount {
			obsResult.FailureCount -= healedCount
		} else {
			obsResult.FailureCount = 0
		}
		obsResult.SuccessCount += healedCount
		obsResult.Failures = remainingFailures
	}

	return healedCount
}

func (e *SelfHealEngine) HealToolError(ctx context.Context, toolName, toolInput, errStr string) *HealResult {
	if e == nil || e.toolRegistry == nil {
		return &HealResult{
			OriginalError: errStr,
			Category:      ErrorNeedsReplan,
			EscalatedTo:   "engine_unavailable",
		}
	}
	return e.heal(ctx, errStr, toolName, toolInput)
}

func (e *SelfHealEngine) ClassifyError(errStr string) ErrorCategory {
	if e == nil {
		return ErrorNeedsReplan
	}
	for _, p := range e.patterns {
		if p.Match != nil && p.Match(errStr) {
			return ErrorFixable
		}
	}

	lower := strings.ToLower(errStr)
	needsHumanKeywords := []string{
		"auth",
		"unauthorized",
		"forbidden",
		"rate limit",
		"quota",
		"disk full",
		"out of memory",
		"permission denied (publickey)",
		"access denied",
	}
	for _, kw := range needsHumanKeywords {
		if strings.Contains(lower, kw) {
			return ErrorNeedsHuman
		}
	}
	return ErrorNeedsReplan
}

func (e *SelfHealEngine) heal(ctx context.Context, errStr, toolName, originalInput string) (result *HealResult) {
	result = &HealResult{
		OriginalError: errStr,
		Category:      e.ClassifyError(errStr),
	}
	defer func() {
		if r := recover(); r != nil {
			result.HealError = fmt.Sprintf("panic: %v", r)
			result.Resolved = false
			result.EscalatedTo = "panic"
		}
	}()

	if e == nil || e.toolRegistry == nil {
		result.Category = ErrorNeedsReplan
		result.EscalatedTo = "engine_unavailable"
		return result
	}
	if result.Category != ErrorFixable {
		result.EscalatedTo = result.Category.String()
		return result
	}

	var action *HealAction
	for _, p := range e.patterns {
		if p.Match != nil && p.Match(errStr) {
			action = p.BuildAction(errStr, toolName, originalInput)
			break
		}
	}
	if action == nil {
		result.Category = ErrorNeedsReplan
		result.EscalatedTo = "no_pattern_match"
		return result
	}

	result.HealAttempted = true
	result.HealAction = action.Description

	healTool, getErr := e.toolRegistry.Get(action.ToolName)
	if getErr != nil {
		result.HealError = getErr.Error()
		result.EscalatedTo = "heal_tool_missing"
		return result
	}

	healCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	healExecResult, healErr := healTool.Execute(healCtx, []byte(action.ToolInput))
	if healErr != nil {
		result.HealError = healErr.Error()
		result.EscalatedTo = "heal_execution_failed"
		return result
	}
	result.HealOutput = healExecResult.Output
	if healExecResult.Error != "" {
		result.HealError = healExecResult.Error
		result.EscalatedTo = "heal_execution_failed"
		return result
	}

	if action.IsRetry {
		origTool, getOrigErr := e.toolRegistry.Get(toolName)
		if getOrigErr != nil {
			result.HealError = getOrigErr.Error()
			result.EscalatedTo = "original_tool_missing"
			return result
		}

		retryCtx, retryCancel := context.WithTimeout(ctx, 30*time.Second)
		defer retryCancel()

		retryExecResult, retryErr := origTool.Execute(retryCtx, []byte(originalInput))
		if retryErr != nil {
			result.HealError = retryErr.Error()
			result.EscalatedTo = "retry_failed"
			return result
		}

		obs := &Observation{
			ToolName:   toolName,
			Input:      originalInput,
			Output:     retryExecResult.Output,
			DurationMs: 0,
			Metadata:   retryExecResult.Metadata,
		}
		if retryExecResult.Error != "" {
			obs.Error = retryExecResult.Error
			result.EscalatedTo = "retry_still_errored"
			result.RetryResult = obs
			return result
		}

		result.RetryResult = obs
		result.Resolved = true
		result.EscalatedTo = ""
		return result
	}

	result.Resolved = true
	return result
}

func builtinPatterns() []HealPattern {
	return []HealPattern{
		{
			Name: "no_such_file",
			Match: func(s string) bool {
				return strings.Contains(strings.ToLower(s), "no such file or directory")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				path := extractPath(errStr)
				if path == "" {
					path = extractPath(originalInput)
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("mkdir -p $(dirname %q)", path)),
					Description: fmt.Sprintf("mkdir -p for %q", path),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "permission_denied",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "permission denied") && !strings.Contains(s, "publickey")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				path := extractPath(errStr)
				if path == "" {
					path = extractPath(originalInput)
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("chmod +x %q 2>/dev/null; chmod +w %q 2>/dev/null; chmod u+rwx %q 2>/dev/null", path, path, path)),
					Description: fmt.Sprintf("chmod on %q", path),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "command_not_found",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "command not found") || (strings.Contains(s, "exit code") && strings.Contains(s, "127"))
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				cmd := extractCommand(originalInput)
				if cmd == "" {
					cmd = "unknown"
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("(which %[1]q 2>/dev/null && echo 'found') || (apt-get update -qq && apt-get install -y %[1]q 2>/dev/null) || (brew install %[1]q 2>/dev/null) || (pip install %[1]q 2>/dev/null) || (npm install -g %[1]q 2>/dev/null)", cmd)),
					Description: fmt.Sprintf("try to install %q", cmd),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "connection_refused",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "connection refused") || strings.Contains(s, "cannot connect")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput("sleep 2 && echo 'retry_ready'"),
					Description: "wait 2s before retry",
					IsRetry:     true,
				}
			},
		},
		{
			Name: "timeout",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "timed out") || strings.Contains(s, "deadline exceeded") ||
					strings.Contains(s, "context deadline") || strings.Contains(s, "i/o timeout")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput("echo 'retrying after timeout'"),
					Description: "retry after timeout",
					IsRetry:     true,
				}
			},
		},
		{
			Name: "is_a_directory",
			Match: func(s string) bool {
				return strings.Contains(strings.ToLower(s), "is a directory")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				path := extractPath(errStr)
				if path == "" {
					path = extractPath(originalInput)
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("ls -la %q 2>/dev/null | head -5", path)),
					Description: fmt.Sprintf("list directory %q instead of reading", path),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "not_a_directory",
			Match: func(s string) bool {
				return strings.Contains(strings.ToLower(s), "not a directory")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				path := extractPath(errStr)
				if path == "" {
					path = extractPath(originalInput)
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("mkdir -p %q", filepathDir(path))),
					Description: fmt.Sprintf("mkdir -p parent of %q", path),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "cannot_create_file",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "cannot create") && strings.Contains(s, "file")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				path := extractPath(errStr)
				if path == "" {
					path = extractPath(originalInput)
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("mkdir -p $(dirname %q)", path)),
					Description: fmt.Sprintf("mkdir -p for %q", path),
					IsRetry:     true,
				}
			},
		},
		{
			Name: "http_404",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "404") && (strings.Contains(s, "not found") || strings.Contains(s, "http"))
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput("echo 'HTTP 404: resource does not exist at this URL'"),
					Description: "report 404, no auto-fix possible",
					IsRetry:     false,
				}
			},
		},
		{
			Name: "syntax_error",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "syntax error") || strings.Contains(s, "unexpected token") ||
					strings.Contains(s, "parse error")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				cmd := extractRawCommand(originalInput)
				if cmd == "" {
					cmd = originalInput
				}
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput(fmt.Sprintf("echo 'Attempting fixed command' && bash -lc %s", shellQuote(cmd))),
					Description: "retry with shell quoting",
					IsRetry:     true,
				}
			},
		},
		{
			Name: "no_space",
			Match: func(s string) bool {
				s = strings.ToLower(s)
				return strings.Contains(s, "no space left") || strings.Contains(s, "disk full") ||
					strings.Contains(s, "disk quota")
			},
			BuildAction: func(errStr, toolName, originalInput string) *HealAction {
				return &HealAction{
					ToolName:    "bash",
					ToolInput:   bashToolInput("df -h && du -sh /tmp 2>/dev/null && rm -rf /tmp/* 2>/dev/null; echo 'cleaned tmp'"),
					Description: "clean /tmp to free space",
					IsRetry:     true,
				}
			},
		},
	}
}

func extractPath(s string) string {
	re := regexp.MustCompile(`['"]?(/[^\s'"]+)['"]?`)
	matches := re.FindStringSubmatch(s)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func extractCommand(input string) string {
	cmd := extractRawCommand(input)
	if cmd == "" {
		cmd = input
	}
	cmd = strings.TrimSpace(cmd)
	parts := strings.Fields(cmd)
	if len(parts) > 0 {
		first := parts[0]
		if first == "sudo" && len(parts) > 1 {
			return parts[1]
		}
		if first == "env" {
			for _, part := range parts[1:] {
				if !strings.Contains(part, "=") {
					return part
				}
			}
		}
		return first
	}
	return ""
}

func filepathDir(path string) string {
	path = strings.TrimRight(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "."
	}
	if idx == 0 {
		return "/"
	}
	return path[:idx]
}

func shellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

func bashToolInput(command string) string {
	payload, _ := json.Marshal(map[string]string{"command": command})
	return string(payload)
}

func extractRawCommand(input string) string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err == nil && payload.Command != "" {
		return payload.Command
	}
	return ""
}

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const verifyReadLimitBytes int64 = 1 << 20

var (
	verifyRedirectRE = regexp.MustCompile(`(?:^|[;&|]\s*|\s)(?:>|>>)\s*([^\s;&|]+)`)
	verifyTeeRE      = regexp.MustCompile(`\btee\s+(-a\s+)?([^\s|;&]+)`)
	verifySedInPlace = regexp.MustCompile(`\bsed\s+-i(?:\s+['"][^'"]*['"])?\s+[^\n]*?\s+([^\s;&|]+)`)
	verifyTouchRE    = regexp.MustCompile(`\btouch\s+([^\s;&|]+)`)
	verifyMkdirRE    = regexp.MustCompile(`\bmkdir(?:\s+-p)?\s+([^\s;&|]+)`)
	verifyTruncateRE = regexp.MustCompile(`\btruncate\b(?:\s+\S+)*\s+([^\s;&|]+)`)
	verifyInstallRE  = regexp.MustCompile(`\binstall\b(?:\s+\S+)*\s+([^\s;&|]+)$`)
	verifyCpRE       = regexp.MustCompile(`\bcp\b(?:\s+\S+)*\s+([^\s;&|]+)$`)
	verifyMvRE       = regexp.MustCompile(`\bmv\b(?:\s+\S+)*\s+([^\s;&|]+)$`)
)

type verifyMetadata struct {
	DiffSummary   string `json:"diff_summary,omitempty"`
	FileReadable  bool   `json:"file_readable"`
	FileSizeBytes int64  `json:"file_size_bytes,omitempty"`
}

type VerifyInterceptor struct {
	workingDir string
	planStore  PlanStore
	logger     *slog.Logger
}

func NewVerifyInterceptor(workingDir string, planStore PlanStore) *VerifyInterceptor {
	return &VerifyInterceptor{
		workingDir: workingDir,
		planStore:  planStore,
		logger:     slog.Default(),
	}
}

func (v *VerifyInterceptor) Name() string { return "verify" }

func (v *VerifyInterceptor) Intercept(
	ctx context.Context,
	call *ToolCall,
	next InterceptorFunc,
) (*ToolResult, error) {
	result, err := next(ctx, call)
	if err != nil || result == nil || result.Error != "" || !shouldVerifyToolCall(call) {
		return result, err
	}

	targetPath, warnings := extractVerifyPath(call.ToolName, call.Input, v.workingDir)
	verify := verifyMetadata{}

	if targetPath == "" {
		warnings = append(warnings, "verification skipped: could not determine modified file path")
	} else {
		info, statErr := os.Stat(targetPath)
		switch {
		case statErr != nil:
			warnings = append(warnings, fmt.Sprintf("verification read failed: %v", statErr))
		case info.IsDir():
			warnings = append(warnings, "verification skipped: target path is a directory")
		default:
			verify.FileSizeBytes = info.Size()
			if info.Size() > verifyReadLimitBytes {
				warnings = append(warnings, "verification skipped: file exceeds 1MB read limit")
			} else if _, readErr := os.ReadFile(targetPath); readErr != nil {
				warnings = append(warnings, fmt.Sprintf("verification read failed: %v", readErr))
			} else {
				verify.FileReadable = true
			}
		}
	}

	diffSummary, diffWarning := v.gitDiffStat(ctx, targetPath)
	if diffSummary != "" {
		verify.DiffSummary = diffSummary
	} else {
		warnings = append(warnings, "verification warning: git diff returned no output")
	}
	if diffWarning != "" {
		warnings = append(warnings, diffWarning)
	}

	if result.Metadata == nil {
		result.Metadata = make(map[string]string)
	}
	if payload, marshalErr := json.Marshal(verify); marshalErr == nil {
		result.Metadata["verify"] = string(payload)
	} else {
		warnings = append(warnings, fmt.Sprintf("verification warning: marshal metadata failed: %v", marshalErr))
	}
	if len(warnings) > 0 {
		if payload, marshalErr := json.Marshal(warnings); marshalErr == nil {
			result.Metadata["verify_warnings"] = string(payload)
		}
	}

	v.logger.Debug("tool verification completed",
		"tool", call.ToolName,
		"path", targetPath,
		"diff_summary", verify.DiffSummary,
		"file_readable", verify.FileReadable,
		"warnings", warnings,
	)

	// Plan-aware verification: if there's an active plan with an in-progress step
	// whose success criteria mentions verification, append a hint to the tool result.
	if v.planStore != nil && result != nil && result.Error == "" {
		if hint := v.buildPlanVerifyHint(call.SessionID); hint != "" {
			if result.Metadata == nil {
				result.Metadata = make(map[string]string)
			}
			result.Metadata["plan_verify_hint"] = hint
		}
	}

	return result, err
}

// buildPlanVerifyHint checks the active plan for an in-progress step with
// verifiable success criteria. If found, returns a hint string for the model.
func (v *VerifyInterceptor) buildPlanVerifyHint(sessionID string) string {
	raw, err := v.planStore.GetPlan(sessionID)
	if err != nil || raw == "" {
		return ""
	}

	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return ""
	}

	var hints []string
	for _, step := range plan.Steps {
		if step.Status != "in_progress" || step.Criteria == "" {
			continue
		}
		criteria := strings.ToLower(step.Criteria)
		if strings.Contains(criteria, "test_run") || strings.Contains(criteria, "test") ||
			strings.Contains(criteria, "go test") || strings.Contains(criteria, "go vet") ||
			strings.Contains(criteria, "lint") || strings.Contains(criteria, "build") {
			hints = append(hints, fmt.Sprintf("Step %q (%s) is in progress with criteria: %s",
				step.Description, step.Status, step.Criteria))
		}
	}

	if len(hints) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"[Plan Verification] %d step(s) need verification:\n%s\n\nConsider running test_run or the appropriate verification command.",
		len(hints), strings.Join(hints, "\n"),
	)
}

func shouldVerifyToolCall(call *ToolCall) bool {
	if call == nil {
		return false
	}
	switch call.ToolName {
	case "file_write", "file_edit", "file_patch":
		return true
	case "bash":
		return bashLikelyWritesJSON(call.Input)
	default:
		return false
	}
}

func bashLikelyWritesJSON(input string) bool {
	if input == "" {
		return false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return false
	}

	cmd, _ := payload["cmd"].(string)
	if cmd == "" {
		if alt, ok := payload["command"].(string); ok {
			cmd = alt
		}
	}
	cmd = strings.ToLower(cmd)
	if cmd == "" {
		return false
	}

	writeHints := []string{
		">", ">>", "tee ", "touch ", "mkdir ", "rm ", "mv ", "cp ",
		"sed -i", "perl -pi", "truncate ", "install ", "cat <<", "apply_patch",
		"git commit", "git merge", "git cherry-pick", "git rebase", "git worktree add",
	}
	for _, hint := range writeHints {
		if strings.Contains(cmd, hint) {
			return true
		}
	}
	return false
}

func extractVerifyPath(toolName, input, workingDir string) (string, []string) {
	switch toolName {
	case "file_write", "file_edit", "file_patch":
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(input), &payload); err != nil {
			return "", []string{fmt.Sprintf("verification input parse failed: %v", err)}
		}
		if payload.Path == "" {
			return "", []string{"verification input missing path"}
		}
		return resolveVerifyPath(workingDir, payload.Path), nil
	case "bash":
		var payload map[string]any
		if err := json.Unmarshal([]byte(input), &payload); err != nil {
			return "", []string{fmt.Sprintf("verification input parse failed: %v", err)}
		}
		cmd, _ := payload["cmd"].(string)
		if cmd == "" {
			cmd, _ = payload["command"].(string)
		}
		if cmd == "" {
			return "", []string{"verification input missing command"}
		}
		path := extractPathFromCommand(cmd)
		if path == "" {
			return "", nil
		}
		return resolveVerifyPath(workingDir, path), nil
	default:
		return "", nil
	}
}

func resolveVerifyPath(workingDir, p string) string {
	p = strings.Trim(p, `"'`)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if workingDir == "" {
		workingDir = "."
	}
	return filepath.Clean(filepath.Join(workingDir, p))
}

func extractPathFromCommand(cmd string) string {
	matchers := []*regexp.Regexp{
		verifyRedirectRE,
		verifyTeeRE,
		verifySedInPlace,
		verifyTouchRE,
		verifyMkdirRE,
		verifyTruncateRE,
		verifyInstallRE,
		verifyCpRE,
		verifyMvRE,
	}
	for _, re := range matchers {
		matches := re.FindStringSubmatch(cmd)
		if len(matches) == 0 {
			continue
		}
		return matches[len(matches)-1]
	}
	return ""
}

func (v *VerifyInterceptor) gitDiffStat(ctx context.Context, targetPath string) (string, string) {
	if v.workingDir == "" {
		return "", "verification warning: working directory is not configured"
	}

	relPath := targetPath
	if targetPath != "" {
		if rel, err := filepath.Rel(v.workingDir, targetPath); err == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
			relPath = rel
		}
	}

	// Try git diff --stat first (for tracked + modified files)
	if summary, ok := runGitDiffStat(ctx, v.workingDir, []string{"diff", "--stat", "--", relPath}); ok {
		return summary, ""
	}

	// Fallback: check git diff --cached --stat (for staged files)
	if summary, ok := runGitDiffStat(ctx, v.workingDir, []string{"diff", "--cached", "--stat", "--", relPath}); ok {
		return summary, ""
	}

	// Fallback: check git status for untracked/new files
	if summary, ok := runGitDiffStat(ctx, v.workingDir, []string{"status", "--porcelain", "--", relPath}); ok && summary != "" {
		return summary, ""
	}

	// If git is unavailable or repo is not valid, return appropriate warning
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = v.workingDir
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.Error); ok && ee.Err != nil {
			return "", fmt.Sprintf("verification warning: git unavailable: %v", ee.Err)
		}
		return "", "verification warning: working directory is not a git repository"
	}

	return "", ""
}

func runGitDiffStat(ctx context.Context, dir string, args []string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err == nil && trimmed != "" {
		return trimmed, true
	}
	return "", false
}

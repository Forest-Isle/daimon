package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

// SandboxInterceptor dispatches tool calls to appropriate sandbox checks:
// bash → Docker session, file_* → FileGuard, http → NetworkPolicy.
type SandboxInterceptor struct {
	dockerMgr     *sandbox.DockerSessionManager
	fileGuard     *sandbox.FileGuard
	networkPolicy *sandbox.NetworkPolicy
	enabled       bool
}

func NewSandboxInterceptor(
	dockerMgr *sandbox.DockerSessionManager,
	fileGuard *sandbox.FileGuard,
	networkPolicy *sandbox.NetworkPolicy,
	enabled bool,
) *SandboxInterceptor {
	return &SandboxInterceptor{
		dockerMgr:     dockerMgr,
		fileGuard:     fileGuard,
		networkPolicy: networkPolicy,
		enabled:       enabled,
	}
}

func (s *SandboxInterceptor) Name() string { return "sandbox" }

func (s *SandboxInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if !s.enabled {
		return next(ctx, call)
	}
	switch {
	case call.ToolName == "bash":
		return s.interceptBash(ctx, call, next)
	case strings.HasPrefix(call.ToolName, "file"):
		return s.interceptFile(ctx, call, next)
	case call.ToolName == "http":
		return s.interceptHTTP(ctx, call, next)
	default:
		return next(ctx, call)
	}
}

func (s *SandboxInterceptor) interceptBash(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if s.dockerMgr == nil || !s.dockerMgr.Available() {
		return next(ctx, call)
	}
	session, err := s.dockerMgr.GetOrCreate(ctx, call.SessionID)
	if err != nil {
		return next(ctx, call)
	}
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.Input), &parsed); err != nil {
		return &ToolResult{Error: "invalid bash input"}, nil
	}
	stdout, stderr, exitCode, duration, err := session.Exec(ctx, parsed.Command)
	if err != nil {
		return nil, fmt.Errorf("sandbox exec: %w", err)
	}
	output := formatBashSandboxResult(stdout, stderr, exitCode, duration.Milliseconds())
	return &ToolResult{Output: output}, nil
}

func (s *SandboxInterceptor) interceptFile(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if s.fileGuard == nil {
		return next(ctx, call)
	}
	var parsed struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(call.Input), &parsed) != nil || parsed.Path == "" {
		return next(ctx, call)
	}
	isWrite := call.ToolName == "file_write" || call.ToolName == "file_edit" || call.ToolName == "file_patch"
	if err := s.fileGuard.ValidateAccess(parsed.Path, isWrite); err != nil {
		return &ToolResult{Error: fmt.Sprintf("sandbox: %s", err)}, nil
	}
	return next(ctx, call)
}

func (s *SandboxInterceptor) interceptHTTP(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if s.networkPolicy == nil || s.networkPolicy.Mode() == "none" {
		return next(ctx, call)
	}
	var parsed struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(call.Input), &parsed) == nil && parsed.URL != "" {
		if err := s.networkPolicy.CheckURL(parsed.URL); err != nil {
			return &ToolResult{Error: fmt.Sprintf("sandbox: %s", err)}, nil
		}
	}
	return next(ctx, call)
}

func formatBashSandboxResult(stdout, stderr string, exitCode int, durationMs int64) string {
	result := map[string]any{
		"stdout":      stdout,
		"stderr":      stderr,
		"exit_code":   exitCode,
		"duration_ms": durationMs,
		"status":      "success",
		"sandbox":     true,
	}
	if exitCode != 0 {
		result["status"] = "error"
	}
	b, _ := json.Marshal(result)
	return string(b)
}

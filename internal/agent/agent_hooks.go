package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// AgentHookFunc is the function signature for agent lifecycle hooks.
type AgentHookFunc func(ctx context.Context, hctx *AgentHookContext) error

// AgentHookContext provides context to lifecycle hook functions.
type AgentHookContext struct {
	AgentID   string
	AgentName string
	ParentID  string
	Task      string
	Result    *AgentResult  // only available in OnComplete/OnError
	Error     error         // only available in OnError
	ToolName  string        // only available in OnToolCall
	Duration  time.Duration // only available in OnComplete/OnError/OnTimeout
}

// AgentHooks defines lifecycle hooks for a sub-agent.
// Each slice can hold multiple hooks that are executed in order.
// Hook failures are logged but do not block agent execution.
type AgentHooks struct {
	OnStart    []AgentHookFunc
	OnComplete []AgentHookFunc
	OnError    []AgentHookFunc
	OnTimeout  []AgentHookFunc
	OnToolCall []AgentHookFunc
}

// AgentHookConfig is the YAML-serializable configuration for agent hooks.
type AgentHookConfig struct {
	OnStart    []AgentHookEntry `yaml:"on_start"`
	OnComplete []AgentHookEntry `yaml:"on_complete"`
	OnError    []AgentHookEntry `yaml:"on_error"`
	OnTimeout  []AgentHookEntry `yaml:"on_timeout"`
	OnToolCall []AgentHookEntry `yaml:"on_tool_call"`
}

// AgentHookEntry is a single hook configuration entry.
type AgentHookEntry struct {
	Type    string `yaml:"type"`    // "log" | "exec"
	Message string `yaml:"message"` // for log type
	Command string `yaml:"command"` // for exec type
}

// AgentHookRunner executes lifecycle hooks for a specific agent.
type AgentHookRunner struct {
	hooks AgentHooks
}

// NewAgentHookRunner creates a runner from hooks configuration.
func NewAgentHookRunner(hooks AgentHooks) *AgentHookRunner {
	return &AgentHookRunner{hooks: hooks}
}

// BuildAgentHooks converts YAML hook config into executable AgentHooks.
func BuildAgentHooks(cfg AgentHookConfig) AgentHooks {
	return AgentHooks{
		OnStart:    buildHookFuncs(cfg.OnStart),
		OnComplete: buildHookFuncs(cfg.OnComplete),
		OnError:    buildHookFuncs(cfg.OnError),
		OnTimeout:  buildHookFuncs(cfg.OnTimeout),
		OnToolCall: buildHookFuncs(cfg.OnToolCall),
	}
}

func buildHookFuncs(entries []AgentHookEntry) []AgentHookFunc {
	var funcs []AgentHookFunc
	for _, entry := range entries {
		switch entry.Type {
		case "log":
			msg := entry.Message
			funcs = append(funcs, func(ctx context.Context, hctx *AgentHookContext) error {
				slog.Info("agent hook",
					"agent", hctx.AgentName,
					"agent_id", hctx.AgentID,
					"message", msg,
				)
				return nil
			})
		case "exec":
			cmd := entry.Command
			funcs = append(funcs, func(ctx context.Context, hctx *AgentHookContext) error {
				execCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				out, err := exec.CommandContext(execCtx, "sh", "-c", cmd).CombinedOutput()
				if err != nil {
					return fmt.Errorf("exec hook %q: %w (output: %s)", cmd, err, string(out))
				}
				return nil
			})
		default:
			slog.Warn("unknown agent hook type", "type", entry.Type)
		}
	}
	return funcs
}

// RunOnStart executes all OnStart hooks.
func (r *AgentHookRunner) RunOnStart(ctx context.Context, hctx *AgentHookContext) {
	r.runHooks(ctx, "OnStart", r.hooks.OnStart, hctx)
}

// RunOnComplete executes all OnComplete hooks.
func (r *AgentHookRunner) RunOnComplete(ctx context.Context, hctx *AgentHookContext) {
	r.runHooks(ctx, "OnComplete", r.hooks.OnComplete, hctx)
}

// RunOnError executes all OnError hooks.
func (r *AgentHookRunner) RunOnError(ctx context.Context, hctx *AgentHookContext) {
	r.runHooks(ctx, "OnError", r.hooks.OnError, hctx)
}

// RunOnTimeout executes all OnTimeout hooks.
func (r *AgentHookRunner) RunOnTimeout(ctx context.Context, hctx *AgentHookContext) {
	r.runHooks(ctx, "OnTimeout", r.hooks.OnTimeout, hctx)
}

// RunOnToolCall executes all OnToolCall hooks.
func (r *AgentHookRunner) RunOnToolCall(ctx context.Context, hctx *AgentHookContext) {
	r.runHooks(ctx, "OnToolCall", r.hooks.OnToolCall, hctx)
}

// HasHooks returns true if any hooks are configured.
func (r *AgentHookRunner) HasHooks() bool {
	return len(r.hooks.OnStart) > 0 ||
		len(r.hooks.OnComplete) > 0 ||
		len(r.hooks.OnError) > 0 ||
		len(r.hooks.OnTimeout) > 0 ||
		len(r.hooks.OnToolCall) > 0
}

func (r *AgentHookRunner) runHooks(ctx context.Context, phase string, hooks []AgentHookFunc, hctx *AgentHookContext) {
	for i, hook := range hooks {
		if err := hook(ctx, hctx); err != nil {
			slog.Warn("agent lifecycle hook failed",
				"phase", phase,
				"hook_index", i,
				"agent", hctx.AgentName,
				"err", err,
			)
			// Hook failures don't block agent execution
		}
	}
}

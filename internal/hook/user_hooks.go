package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HookEventType string

const (
	HookPreToolUse    HookEventType = "pre_tool_use"
	HookPostToolUse   HookEventType = "post_tool_use"
	HookOnUserMessage HookEventType = "on_user_message"
	HookPreCompact    HookEventType = "pre_compact"
	HookOnStop        HookEventType = "on_stop"
	HookNotification  HookEventType = "notification"
)

type UserHook struct {
	Name     string
	Path     string
	Event    HookEventType
	Priority int
	Timeout  time.Duration
}

type HookResult struct {
	HookName string
	Event    HookEventType
	Success  bool
	ExitCode int
	Output   string
	Error    string
	Duration time.Duration
}

type UserHookManager struct {
	hooksDir string
	hooks    map[HookEventType][]UserHook
	mu       sync.RWMutex
	timeout  time.Duration
}

var validHookEvents = []HookEventType{
	HookPreToolUse,
	HookPostToolUse,
	HookOnUserMessage,
	HookPreCompact,
	HookOnStop,
	HookNotification,
}

func NewUserHookManager(hooksDir string, timeout time.Duration) *UserHookManager {
	if hooksDir == "" {
		hooksDir = defaultHooksDir()
	}

	m := &UserHookManager{
		hooksDir: hooksDir,
		hooks:    make(map[HookEventType][]UserHook),
		timeout:  timeout,
	}
	if err := m.ReloadHooks(); err != nil {
		slog.Warn("hook: failed to load user hooks", "dir", hooksDir, "err", err)
	}
	return m
}

func (m *UserHookManager) ReloadHooks() error {
	entries, err := os.ReadDir(m.hooksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			m.mu.Lock()
			m.hooks = make(map[HookEventType][]UserHook)
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("read hooks dir: %w", err)
	}

	discovered := make(map[HookEventType][]UserHook)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(m.hooksDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			slog.Warn("hook: failed to stat user hook", "path", path, "err", err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}

		hook, ok := parseUserHook(path, entry.Name())
		if !ok {
			continue
		}
		discovered[hook.Event] = append(discovered[hook.Event], hook)
	}

	for event := range discovered {
		slices.SortFunc(discovered[event], func(a, b UserHook) int {
			if a.Priority != b.Priority {
				return a.Priority - b.Priority
			}
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return 0
		})
	}

	m.mu.Lock()
	m.hooks = discovered
	m.mu.Unlock()
	return nil
}

func (m *UserHookManager) RunHooks(ctx context.Context, event HookEventType, payload any) []HookResult {
	hooks := m.hooksForEvent(event)
	if len(hooks) == 0 {
		return nil
	}

	input, err := json.Marshal(payload)
	if err != nil {
		results := make([]HookResult, 0, len(hooks))
		for _, hook := range hooks {
			results = append(results, HookResult{
				HookName: hook.Name,
				Event:    event,
				ExitCode: -1,
				Error:    fmt.Sprintf("marshal payload: %v", err),
			})
		}
		return results
	}

	results := make([]HookResult, 0, len(hooks))
	for _, hook := range hooks {
		results = append(results, m.runHook(ctx, hook, input))
	}
	return results
}

func (m *UserHookManager) ListHooks() []UserHook {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var hooks []UserHook
	for _, event := range validHookEvents {
		hooks = append(hooks, m.hooks[event]...)
	}
	return hooks
}

func (m *UserHookManager) HasHooks(event HookEventType) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks[event]) > 0
}

func (m *UserHookManager) hooksForEvent(event HookEventType) []UserHook {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hooks := m.hooks[event]
	if len(hooks) == 0 {
		return nil
	}
	return append([]UserHook(nil), hooks...)
}

func (m *UserHookManager) runHook(parent context.Context, hook UserHook, input []byte) HookResult {
	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = m.timeout
	}

	runCtx := parent
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(parent, timeout)
	}
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(runCtx, hook.Path)
	cmd.Stdin = bytes.NewReader(input)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := HookResult{
		HookName: hook.Name,
		Event:    hook.Event,
		ExitCode: exitCode(err),
		Output:   strings.TrimSpace(stdout.String()),
		Error:    strings.TrimSpace(stderr.String()),
		Duration: time.Since(start),
	}

	if err == nil {
		result.Success = true
		return result
	}

	if result.Error == "" {
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.Error = "hook execution timed out"
		case errors.Is(runCtx.Err(), context.Canceled):
			result.Error = "hook execution canceled"
		default:
			result.Error = err.Error()
		}
	}

	slog.Warn("hook: user hook execution failed",
		"hook", hook.Name,
		"event", hook.Event,
		"exit_code", result.ExitCode,
		"duration", result.Duration,
		"err", result.Error,
	)
	return result
}

func parseUserHook(path, name string) (UserHook, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return UserHook{}, false
	}

	priority, err := strconv.Atoi(parts[0])
	if err != nil {
		return UserHook{}, false
	}

	rest := parts[1]
	for _, event := range validHookEvents {
		prefix := string(event) + "_"
		if strings.HasPrefix(rest, prefix) {
			if len(rest) == len(prefix) {
				return UserHook{}, false
			}
			return UserHook{
				Name:     name,
				Path:     path,
				Event:    event,
				Priority: priority,
			}, true
		}
	}

	return UserHook{}, false
}

func defaultHooksDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".ironclaw", "hooks")
	}
	return filepath.Join(home, ".ironclaw", "hooks")
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return -1
	}

	return -1
}

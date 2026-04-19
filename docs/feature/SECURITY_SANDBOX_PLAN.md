# 安全沙箱与执行隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在工具执行路径引入拦截器链架构，实现四级权限、Docker 会话容器沙箱、文件路径守卫、网络策略。

**Architecture:** 中间件链模式 — `PermissionInterceptor → HookInterceptor → SandboxInterceptor → Tool.Execute()`。拦截器通过统一的 `ToolInterceptor` 接口组合，各安全层独立可配置。Docker 会话容器按 session 绑定，优雅降级到宿主机执行。

**Tech Stack:** Go 1.22+, Docker CLI (`os/exec`), `net/url`, `path/filepath`, CGO_ENABLED=1 -tags fts5

---

## File Structure

### New files

| File | Responsibility |
|---|---|
| `internal/tool/interceptor.go` | `ToolInterceptor`, `InterceptorFunc`, `ToolCall`, `ToolResult`, `InterceptorChain` |
| `internal/tool/interceptor_permission.go` | `PermissionInterceptor`, `ToolNotifier`, `ToolApprover` |
| `internal/tool/interceptor_sandbox.go` | `SandboxInterceptor` (dispatches by tool type) |
| `internal/tool/interceptor_hook.go` | `HookInterceptor` (wraps existing hook logic) |
| `internal/sandbox/file_guard.go` | `FileGuard` path whitelist validator |
| `internal/sandbox/network_policy.go` | `NetworkPolicy` URL filter |
| `internal/sandbox/docker_session.go` | `DockerSessionManager`, `DockerSession` |
| `internal/sandbox/docker_probe.go` | Docker daemon availability probe |
| `internal/tool/interceptor_test.go` | Interceptor chain unit tests |
| `internal/tool/interceptor_permission_test.go` | Permission interceptor tests |
| `internal/tool/interceptor_sandbox_test.go` | Sandbox interceptor tests |
| `internal/sandbox/file_guard_test.go` | FileGuard tests |
| `internal/sandbox/network_policy_test.go` | NetworkPolicy tests |
| `internal/sandbox/docker_session_test.go` | Docker session tests (build tag: docker) |

### Modified files

| File | Change |
|---|---|
| `internal/tool/permissions.go` | Add `PermissionNone`/`PermissionNotify`, backward-compat mapping |
| `internal/config/config.go` | Add `SandboxConfig` struct, `Sandbox` field in `Config`, defaults |
| `internal/gateway/init_tools.go` | Initialize sandbox components, build interceptor chain |
| `internal/gateway/gateway.go` | Add `dockerSessionMgr` field, cleanup in `Stop()` |
| `internal/agent/runtime.go` | Add `interceptorChain` field, `SetInterceptorChain()` setter |
| `internal/agent/concurrent.go` | Refactor `executeToolCall` to use interceptor chain |
| `internal/agent/act.go` | Refactor `executeSubTask` to use interceptor chain |
| `configs/ironclaw.example.yaml` | Add `sandbox:` config section |

---

### Task 1: Interceptor Chain Foundation

**Files:**
- Create: `internal/tool/interceptor.go`
- Test: `internal/tool/interceptor_test.go`

- [ ] **Step 1: Write failing tests for InterceptorChain**

```go
// internal/tool/interceptor_test.go
package tool

import (
	"context"
	"testing"
)

func TestInterceptorChain_Empty(t *testing.T) {
	chain := NewInterceptorChain(nil)
	called := false
	_, err := chain.Execute(context.Background(), &ToolCall{ToolName: "bash", Input: `{"command":"ls"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("final func should be called when chain is empty")
	}
}

func TestInterceptorChain_Order(t *testing.T) {
	var order []string
	ic1 := &testInterceptor{name: "first", fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
		order = append(order, "first-before")
		res, err := next(ctx, call)
		order = append(order, "first-after")
		return res, err
	}}
	ic2 := &testInterceptor{name: "second", fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
		order = append(order, "second-before")
		res, err := next(ctx, call)
		order = append(order, "second-after")
		return res, err
	}}
	chain := NewInterceptorChain([]ToolInterceptor{ic1, ic2})
	_, err := chain.Execute(context.Background(), &ToolCall{ToolName: "test"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		order = append(order, "final")
		return &ToolResult{Output: "done"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"first-before", "second-before", "final", "second-after", "first-after"}
	if len(order) != len(expected) {
		t.Fatalf("order mismatch: got %v, want %v", order, expected)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("order[%d] = %q, want %q", i, order[i], expected[i])
		}
	}
}

func TestInterceptorChain_ShortCircuit(t *testing.T) {
	blocker := &testInterceptor{name: "blocker", fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
		return &ToolResult{Error: "blocked"}, nil
	}}
	finalCalled := false
	chain := NewInterceptorChain([]ToolInterceptor{blocker})
	res, err := chain.Execute(context.Background(), &ToolCall{ToolName: "test"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		finalCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finalCalled {
		t.Fatal("final func should not be called when interceptor short-circuits")
	}
	if res.Error != "blocked" {
		t.Fatalf("expected error 'blocked', got %q", res.Error)
	}
}

type testInterceptor struct {
	name string
	fn   func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
}

func (ti *testInterceptor) Name() string { return ti.name }
func (ti *testInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	return ti.fn(ctx, call, next)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestInterceptorChain ./internal/tool/ -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement interceptor types**

```go
// internal/tool/interceptor.go
package tool

import "context"

// InterceptorFunc is the function signature for the next step in the chain.
type InterceptorFunc func(ctx context.Context, call *ToolCall) (*ToolResult, error)

// ToolInterceptor wraps tool execution with additional behavior.
type ToolInterceptor interface {
	Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
	Name() string
}

// ToolCall carries all information about a pending tool invocation.
type ToolCall struct {
	ToolName  string
	Input     string
	SessionID string
	Metadata  map[string]string
}

// ToolResult wraps the output of a tool execution through the interceptor chain.
type ToolResult struct {
	Output   string
	Error    string
	Metadata map[string]string
}

// InterceptorChain composes interceptors into an ordered execution pipeline.
type InterceptorChain struct {
	interceptors []ToolInterceptor
}

// NewInterceptorChain creates a chain from the given interceptors.
func NewInterceptorChain(interceptors []ToolInterceptor) *InterceptorChain {
	return &InterceptorChain{interceptors: interceptors}
}

// Execute runs the interceptor chain, ending with the final function.
func (c *InterceptorChain) Execute(ctx context.Context, call *ToolCall, final InterceptorFunc) (*ToolResult, error) {
	if len(c.interceptors) == 0 {
		return final(ctx, call)
	}
	handler := final
	for i := len(c.interceptors) - 1; i >= 0; i-- {
		ic := c.interceptors[i]
		next := handler
		handler = func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
			return ic.Intercept(ctx, call, next)
		}
	}
	return handler(ctx, call)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestInterceptorChain ./internal/tool/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/tool/interceptor.go internal/tool/interceptor_test.go
git commit -m "feat(sandbox): add interceptor chain foundation types"
```

---

### Task 2: Permission System Upgrade (none/notify + backward compat)

**Files:**
- Modify: `internal/tool/permissions.go`
- Modify: `internal/config/config.go`
- Test: `internal/tool/permissions_test.go` (existing, add new cases)

- [ ] **Step 1: Write failing tests for new permission actions**

Add to existing test file or create new test cases:

```go
// in internal/tool/permissions_test.go — add these tests
func TestPermissionAction_NoneAndNotify(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "file_read", Action: "none"},
		{Tool: "bash", Pattern: "ls *", Action: "notify"},
		{Tool: "bash", Pattern: "rm *", Action: "approve"},
	}
	pe := NewPermissionEngine(rules, "approve", nil)

	// file_read → none
	r := pe.Evaluate("file_read", `{"path":"/tmp/test"}`, ToolCapabilities{})
	if r.Action != PermissionNone {
		t.Errorf("file_read: got %v, want none", r.Action)
	}

	// bash ls → notify
	r = pe.Evaluate("bash", `{"command":"ls -la"}`, ToolCapabilities{})
	if r.Action != PermissionNotify {
		t.Errorf("bash ls: got %v, want notify", r.Action)
	}

	// bash rm → approve
	r = pe.Evaluate("bash", `{"command":"rm -rf /tmp/foo"}`, ToolCapabilities{})
	if r.Action != PermissionApprove {
		t.Errorf("bash rm: got %v, want approve", r.Action)
	}
}

func TestPermissionAction_BackwardCompat(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "bash", Action: "allow"},  // legacy → none
		{Tool: "http", Action: "ask"},    // legacy → approve
	}
	pe := NewPermissionEngine(rules, "ask", nil)

	r := pe.Evaluate("bash", `{"command":"echo hi"}`, ToolCapabilities{})
	if r.Action != PermissionNone {
		t.Errorf("allow should map to none, got %v", r.Action)
	}

	r = pe.Evaluate("http", `{"url":"http://example.com"}`, ToolCapabilities{})
	if r.Action != PermissionApprove {
		t.Errorf("ask should map to approve, got %v", r.Action)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestPermissionAction ./internal/tool/ -v`
Expected: FAIL — `PermissionNone` and `PermissionNotify` not defined

- [ ] **Step 3: Update permissions.go with new actions**

In `internal/tool/permissions.go`, change the constants and `parseAction` logic:

Replace the existing const block:
```go
const (
	PermissionAllow  PermissionAction = "allow"
	PermissionDeny   PermissionAction = "deny"
	PermissionAsk    PermissionAction = "ask"
)
```

With:
```go
const (
	PermissionNone    PermissionAction = "none"
	PermissionNotify  PermissionAction = "notify"
	PermissionApprove PermissionAction = "approve"
	PermissionDeny    PermissionAction = "deny"

	// Deprecated aliases for backward compatibility
	PermissionAllow PermissionAction = "none"
	PermissionAsk   PermissionAction = "approve"
)
```

Add a `parseAction` helper and update `NewPermissionEngine` and `Evaluate` to use it:

```go
func parseAction(s string) PermissionAction {
	switch s {
	case "none", "allow":
		return PermissionNone
	case "notify":
		return PermissionNotify
	case "approve", "ask":
		return PermissionApprove
	case "deny":
		return PermissionDeny
	default:
		return PermissionApprove
	}
}
```

Replace the inline switch blocks in `NewPermissionEngine` and the rule evaluation loop with calls to `parseAction`.

- [ ] **Step 4: Update config.go PermissionsConfig comment**

In `internal/config/config.go`, update the comment on `PermissionsConfig.Default`:

```go
type PermissionsConfig struct {
	Default string           `yaml:"default"` // "none", "notify", "approve", "deny" (default: "approve"; legacy "allow"/"ask" accepted)
	Rules   []PermissionRule `yaml:"rules"`
}
```

And update the `PermissionRule.Action` comment:

```go
type PermissionRule struct {
	Tool        string `yaml:"tool"`
	Pattern     string `yaml:"pattern"`
	PathPattern string `yaml:"path_pattern"`
	Action      string `yaml:"action"` // "none", "notify", "approve", "deny" (legacy "allow"/"ask" accepted)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestPermission ./internal/tool/ -v`
Expected: PASS (all permission tests including new ones)

- [ ] **Step 6: Commit**

```bash
git add internal/tool/permissions.go internal/tool/permissions_test.go internal/config/config.go
git commit -m "feat(sandbox): upgrade permission actions to none/notify/approve/deny with backward compat"
```

---

### Task 3: Permission Interceptor

**Files:**
- Create: `internal/tool/interceptor_permission.go`
- Test: `internal/tool/interceptor_permission_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/tool/interceptor_permission_test.go
package tool

import (
	"context"
	"testing"
)

type mockNotifier struct{ called bool; lastCall *ToolCall }

func (n *mockNotifier) NotifyToolExecution(_ context.Context, call *ToolCall) error {
	n.called = true
	n.lastCall = call
	return nil
}

type mockApprover struct{ approve bool }

func (a *mockApprover) RequestApproval(_ context.Context, _ *ToolCall) (bool, error) {
	return a.approve, nil
}

func TestPermissionInterceptor_None(t *testing.T) {
	rules := []PermissionRule{{Tool: "file_read", Action: "none"}}
	pe := NewPermissionEngine(rules, "approve", nil)
	notifier := &mockNotifier{}
	pi := NewPermissionInterceptor(pe, notifier, &mockApprover{approve: true})

	called := false
	_, err := pi.Intercept(context.Background(), &ToolCall{ToolName: "file_read", Input: `{"path":"/tmp"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("next should be called for none action")
	}
	if notifier.called {
		t.Fatal("notifier should not be called for none action")
	}
}

func TestPermissionInterceptor_Notify(t *testing.T) {
	rules := []PermissionRule{{Tool: "bash", Pattern: "ls *", Action: "notify"}}
	pe := NewPermissionEngine(rules, "approve", nil)
	notifier := &mockNotifier{}
	pi := NewPermissionInterceptor(pe, notifier, &mockApprover{})

	called := false
	_, err := pi.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: `{"command":"ls -la"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("next should be called for notify action")
	}
	if !notifier.called {
		t.Fatal("notifier should be called for notify action")
	}
}

func TestPermissionInterceptor_Deny(t *testing.T) {
	rules := []PermissionRule{{Tool: "bash", Pattern: "rm *", Action: "deny"}}
	pe := NewPermissionEngine(rules, "approve", nil)
	pi := NewPermissionInterceptor(pe, &mockNotifier{}, &mockApprover{})

	called := false
	res, err := pi.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: `{"command":"rm -rf /"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("next should NOT be called for deny action")
	}
	if res.Error == "" {
		t.Fatal("deny should return error in result")
	}
}

func TestPermissionInterceptor_ApproveGranted(t *testing.T) {
	rules := []PermissionRule{{Tool: "http", Action: "approve"}}
	pe := NewPermissionEngine(rules, "approve", nil)
	pi := NewPermissionInterceptor(pe, &mockNotifier{}, &mockApprover{approve: true})

	called := false
	_, err := pi.Intercept(context.Background(), &ToolCall{ToolName: "http", Input: `{"url":"http://example.com"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("next should be called when approval is granted")
	}
}

func TestPermissionInterceptor_ApproveDenied(t *testing.T) {
	rules := []PermissionRule{{Tool: "http", Action: "approve"}}
	pe := NewPermissionEngine(rules, "approve", nil)
	pi := NewPermissionInterceptor(pe, &mockNotifier{}, &mockApprover{approve: false})

	called := false
	res, err := pi.Intercept(context.Background(), &ToolCall{ToolName: "http", Input: `{"url":"http://example.com"}`}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("next should NOT be called when approval is denied")
	}
	if res.Error == "" {
		t.Fatal("denied approval should return error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestPermissionInterceptor ./internal/tool/ -v`
Expected: FAIL — `NewPermissionInterceptor` not defined

- [ ] **Step 3: Implement PermissionInterceptor**

```go
// internal/tool/interceptor_permission.go
package tool

import (
	"context"
	"fmt"
)

// ToolNotifier sends non-blocking notifications when tools execute.
type ToolNotifier interface {
	NotifyToolExecution(ctx context.Context, call *ToolCall) error
}

// ToolApprover blocks until the user approves or denies a tool execution.
type ToolApprover interface {
	RequestApproval(ctx context.Context, call *ToolCall) (approved bool, err error)
}

// PermissionInterceptor checks permissions before tool execution.
type PermissionInterceptor struct {
	engine   *PermissionEngine
	notifier ToolNotifier
	approver ToolApprover
}

// NewPermissionInterceptor creates a permission interceptor.
func NewPermissionInterceptor(engine *PermissionEngine, notifier ToolNotifier, approver ToolApprover) *PermissionInterceptor {
	return &PermissionInterceptor{engine: engine, notifier: notifier, approver: approver}
}

func (p *PermissionInterceptor) Name() string { return "permission" }

func (p *PermissionInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if p.engine == nil {
		return next(ctx, call)
	}

	result := p.engine.Evaluate(call.ToolName, call.Input, ToolCapabilities{})

	switch result.Action {
	case PermissionNone:
		return next(ctx, call)

	case PermissionNotify:
		if p.notifier != nil {
			_ = p.notifier.NotifyToolExecution(ctx, call)
		}
		return next(ctx, call)

	case PermissionApprove:
		if p.approver != nil {
			approved, err := p.approver.RequestApproval(ctx, call)
			if err != nil || !approved {
				return &ToolResult{Error: "execution denied by user"}, nil
			}
		}
		return next(ctx, call)

	case PermissionDeny:
		reason := result.Reason
		if reason == "" {
			reason = "policy"
		}
		return &ToolResult{Error: fmt.Sprintf("tool %s denied by %s", call.ToolName, reason)}, nil
	}

	return next(ctx, call)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestPermissionInterceptor ./internal/tool/ -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/tool/interceptor_permission.go internal/tool/interceptor_permission_test.go
git commit -m "feat(sandbox): add permission interceptor with none/notify/approve/deny support"
```

---

### Task 4: SandboxConfig in Config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add SandboxConfig types**

Add after the existing `PermissionsConfig` block in `internal/config/config.go`:

```go
// SandboxConfig configures the security sandbox system.
type SandboxConfig struct {
	Enabled             bool              `yaml:"enabled"`
	AllowedDirectories  []string          `yaml:"allowed_directories"`
	ReadonlyDirectories []string          `yaml:"readonly_directories"`
	Bash                BashSandboxConfig `yaml:"bash"`
	Network             NetworkConfig     `yaml:"network"`
}

// BashSandboxConfig configures bash tool execution backend.
type BashSandboxConfig struct {
	Backend string              `yaml:"backend"` // "docker" | "host"
	Docker  DockerSandboxConfig `yaml:"docker"`
}

// DockerSandboxConfig configures the Docker session container.
type DockerSandboxConfig struct {
	Image       string        `yaml:"image"`
	Network     string        `yaml:"network"`      // "none" | "bridge" | "host"
	MemoryLimit string        `yaml:"memory_limit"`
	CPULimit    string        `yaml:"cpu_limit"`
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

// NetworkConfig configures network access policy for HTTP tools.
type NetworkConfig struct {
	Mode      string   `yaml:"mode"` // "none" | "blacklist" | "whitelist"
	Blacklist []string `yaml:"blacklist"`
	Whitelist []string `yaml:"whitelist"`
}
```

- [ ] **Step 2: Add Sandbox field to Config struct**

```go
type Config struct {
	// ... existing fields ...
	Permissions PermissionsConfig    `yaml:"permissions"`
	Sandbox     SandboxConfig        `yaml:"sandbox"`
	Hooks       HooksConfig          `yaml:"hooks"`
	// ...
}
```

- [ ] **Step 3: Add defaults in defaultConfig()**

Add inside the `defaultConfig()` return block:

```go
Sandbox: SandboxConfig{
	Enabled: false,
	Bash: BashSandboxConfig{
		Backend: "host",
		Docker: DockerSandboxConfig{
			Image:       "ironclaw-sandbox:latest",
			Network:     "none",
			MemoryLimit: "512m",
			CPULimit:    "1.0",
			IdleTimeout: 30 * time.Minute,
		},
	},
	Network: NetworkConfig{
		Mode: "blacklist",
	},
},
```

- [ ] **Step 4: Run existing tests to verify nothing breaks**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(sandbox): add SandboxConfig types and defaults"
```

---

### Task 5: FileGuard

**Files:**
- Create: `internal/sandbox/file_guard.go`
- Test: `internal/sandbox/file_guard_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/sandbox/file_guard_test.go
package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileGuard_AllowedPath(t *testing.T) {
	dir := t.TempDir()
	fg, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "subdir", "file.txt")
	if err := fg.ValidateAccess(target, true); err != nil {
		t.Errorf("should allow path inside allowed dir: %v", err)
	}
}

func TestFileGuard_DeniedPath(t *testing.T) {
	dir := t.TempDir()
	fg, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := fg.ValidateAccess("/etc/passwd", false); err == nil {
		t.Error("should deny path outside allowed dir")
	}
}

func TestFileGuard_ReadonlyWrite(t *testing.T) {
	dir := t.TempDir()
	fg, err := NewFileGuard([]string{dir}, []string{dir})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "file.txt")
	if err := fg.ValidateAccess(target, false); err != nil {
		t.Error("read should be allowed in readonly dir")
	}
	if err := fg.ValidateAccess(target, true); err == nil {
		t.Error("write should be denied in readonly dir")
	}
}

func TestFileGuard_TraversalPrevention(t *testing.T) {
	dir := t.TempDir()
	fg, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	traversal := filepath.Join(dir, "..", "..", "etc", "passwd")
	if err := fg.ValidateAccess(traversal, false); err == nil {
		t.Error("should deny path traversal")
	}
}

func TestFileGuard_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()

	outsideFile := filepath.Join(outsideDir, "secret.txt")
	os.WriteFile(outsideFile, []byte("secret"), 0644)

	link := filepath.Join(dir, "escape")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skip("symlinks not supported")
	}

	fg, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(link, "secret.txt")
	if err := fg.ValidateAccess(target, false); err == nil {
		t.Error("should deny symlink pointing outside allowed dir")
	}
}

func TestFileGuard_Empty_NoRestriction(t *testing.T) {
	fg, err := NewFileGuard(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fg.ValidateAccess("/any/path", true); err != nil {
		t.Error("empty config should not restrict")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestFileGuard ./internal/sandbox/ -v`
Expected: FAIL — package/types not defined

- [ ] **Step 3: Implement FileGuard**

```go
// internal/sandbox/file_guard.go
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileGuard validates file paths against allowed directories.
type FileGuard struct {
	allowedDirs  []string
	readonlyDirs []string
}

// NewFileGuard creates a FileGuard. Empty allowed dirs means no restriction.
func NewFileGuard(allowed, readonly []string) (*FileGuard, error) {
	resolved := make([]string, 0, len(allowed))
	for _, d := range allowed {
		abs, err := resolveDir(d)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed dir %q: %w", d, err)
		}
		resolved = append(resolved, abs)
	}
	resolvedRO := make([]string, 0, len(readonly))
	for _, d := range readonly {
		abs, err := resolveDir(d)
		if err != nil {
			return nil, fmt.Errorf("resolve readonly dir %q: %w", d, err)
		}
		resolvedRO = append(resolvedRO, abs)
	}
	return &FileGuard{allowedDirs: resolved, readonlyDirs: resolvedRO}, nil
}

// ValidateAccess checks if a path is within allowed directories.
func (g *FileGuard) ValidateAccess(path string, write bool) error {
	if len(g.allowedDirs) == 0 {
		return nil
	}

	cleaned := filepath.Clean(path)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	// Resolve symlinks to prevent escape
	resolved, err := resolvePathSafe(abs)
	if err != nil {
		resolved = abs
	}

	for _, dir := range g.allowedDirs {
		if isSubPath(dir, resolved) {
			if write {
				for _, ro := range g.readonlyDirs {
					if isSubPath(ro, resolved) {
						return fmt.Errorf("write denied: %s is in readonly directory %s", path, ro)
					}
				}
			}
			return nil
		}
	}

	return fmt.Errorf("access denied: %s is outside allowed directories", path)
}

// AllowedDirs returns the resolved allowed directories (used by Docker volume mounts).
func (g *FileGuard) AllowedDirs() []string  { return g.allowedDirs }
func (g *FileGuard) ReadonlyDirs() []string { return g.readonlyDirs }

func resolveDir(d string) (string, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(abs), nil
}

func resolvePathSafe(abs string) (string, error) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// If path doesn't exist yet, resolve parent
		parent := filepath.Dir(abs)
		resolvedParent, pErr := filepath.EvalSymlinks(parent)
		if pErr != nil {
			return "", pErr
		}
		return filepath.Join(resolvedParent, filepath.Base(abs)), nil
	}
	return resolved, nil
}

func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestFileGuard ./internal/sandbox/ -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/file_guard.go internal/sandbox/file_guard_test.go
git commit -m "feat(sandbox): add FileGuard with path whitelist and symlink protection"
```

---

### Task 6: NetworkPolicy

**Files:**
- Create: `internal/sandbox/network_policy.go`
- Test: `internal/sandbox/network_policy_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/sandbox/network_policy_test.go
package sandbox

import "testing"

func TestNetworkPolicy_BlacklistBlock(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, []string{"evil.com"})
	if err := np.CheckURL("http://evil.com/path"); err == nil {
		t.Error("should block blacklisted host")
	}
	if err := np.CheckURL("http://safe.com/path"); err != nil {
		t.Errorf("should allow non-blacklisted host: %v", err)
	}
}

func TestNetworkPolicy_DefaultBlacklist(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, nil)
	if err := np.CheckURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("should block metadata endpoint by default")
	}
	if err := np.CheckURL("http://localhost:8080/api"); err == nil {
		t.Error("should block localhost by default")
	}
	if err := np.CheckURL("http://127.0.0.1:3000/"); err == nil {
		t.Error("should block 127.0.0.1 by default")
	}
}

func TestNetworkPolicy_WhitelistAllow(t *testing.T) {
	np := NewNetworkPolicy("whitelist", []string{"api.example.com"}, nil)
	if err := np.CheckURL("http://api.example.com/v1/data"); err != nil {
		t.Errorf("should allow whitelisted host: %v", err)
	}
	if err := np.CheckURL("http://other.com/"); err == nil {
		t.Error("should block non-whitelisted host")
	}
}

func TestNetworkPolicy_NoneMode(t *testing.T) {
	np := NewNetworkPolicy("none", nil, nil)
	if err := np.CheckURL("http://anything.com/"); err != nil {
		t.Errorf("none mode should allow all: %v", err)
	}
}

func TestNetworkPolicy_InvalidURL(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, nil)
	if err := np.CheckURL("not-a-url"); err == nil {
		t.Error("should reject invalid URL")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestNetworkPolicy ./internal/sandbox/ -v`
Expected: FAIL — `NewNetworkPolicy` not defined

- [ ] **Step 3: Implement NetworkPolicy**

```go
// internal/sandbox/network_policy.go
package sandbox

import (
	"fmt"
	"net/url"
	"strings"
)

var defaultBlacklist = []string{
	"169.254.169.254",
	"metadata.google.internal",
	"127.0.0.1",
	"localhost",
	"0.0.0.0",
	"[::1]",
}

// NetworkPolicy validates URLs against whitelist/blacklist rules.
type NetworkPolicy struct {
	mode      string
	whitelist map[string]bool
	blacklist map[string]bool
}

// NewNetworkPolicy creates a network policy. User blacklist entries are appended to defaults.
func NewNetworkPolicy(mode string, whitelist, blacklist []string) *NetworkPolicy {
	np := &NetworkPolicy{
		mode:      mode,
		whitelist: make(map[string]bool),
		blacklist: make(map[string]bool),
	}
	for _, h := range defaultBlacklist {
		np.blacklist[strings.ToLower(h)] = true
	}
	for _, h := range blacklist {
		np.blacklist[strings.ToLower(h)] = true
	}
	for _, h := range whitelist {
		np.whitelist[strings.ToLower(h)] = true
	}
	return np
}

// CheckURL validates a URL against the policy.
func (p *NetworkPolicy) CheckURL(rawURL string) error {
	if p.mode == "none" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL: %s", rawURL)
	}

	host := strings.ToLower(u.Hostname())

	switch p.mode {
	case "blacklist":
		if p.blacklist[host] {
			return fmt.Errorf("blocked by network policy: host %s is blacklisted", host)
		}
		return nil

	case "whitelist":
		if p.whitelist[host] {
			return nil
		}
		return fmt.Errorf("blocked by network policy: host %s is not whitelisted", host)

	default:
		return nil
	}
}

// Mode returns the current policy mode.
func (p *NetworkPolicy) Mode() string { return p.mode }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestNetworkPolicy ./internal/sandbox/ -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network_policy.go internal/sandbox/network_policy_test.go
git commit -m "feat(sandbox): add NetworkPolicy with URL blacklist/whitelist and default SSRF protection"
```

---

### Task 7: Docker Session Manager

**Files:**
- Create: `internal/sandbox/docker_probe.go`
- Create: `internal/sandbox/docker_session.go`
- Test: `internal/sandbox/docker_session_test.go`

- [ ] **Step 1: Implement Docker probe**

```go
// internal/sandbox/docker_probe.go
package sandbox

import (
	"context"
	"os/exec"
	"time"
)

// ProbeDocker checks if Docker daemon is available by running `docker info`.
func ProbeDocker(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	return cmd.Run() == nil
}
```

- [ ] **Step 2: Write failing tests for DockerSessionManager**

```go
// internal/sandbox/docker_session_test.go
package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestDockerSessionManager_Unavailable(t *testing.T) {
	cfg := DockerSessionConfig{
		Image:       "ironclaw-sandbox:latest",
		NetworkMode: "none",
		IdleTimeout: 30 * time.Minute,
	}
	mgr := NewDockerSessionManager(cfg, false)
	if mgr.Available() {
		t.Error("should report unavailable when Docker is not available")
	}
}

func TestDockerSessionManager_Available(t *testing.T) {
	cfg := DockerSessionConfig{
		Image:       "ironclaw-sandbox:latest",
		NetworkMode: "none",
		IdleTimeout: 30 * time.Minute,
	}
	mgr := NewDockerSessionManager(cfg, true)
	if !mgr.Available() {
		t.Error("should report available when probe succeeded")
	}
}

func TestDockerSessionManager_GetOrCreate_Unavailable(t *testing.T) {
	cfg := DockerSessionConfig{
		Image:       "ironclaw-sandbox:latest",
		NetworkMode: "none",
		IdleTimeout: 30 * time.Minute,
	}
	mgr := NewDockerSessionManager(cfg, false)
	_, err := mgr.GetOrCreate(context.Background(), "test-session")
	if err == nil {
		t.Error("should return error when Docker is unavailable")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestDockerSessionManager ./internal/sandbox/ -v`
Expected: FAIL — `NewDockerSessionManager` not defined

- [ ] **Step 4: Implement DockerSessionManager**

```go
// internal/sandbox/docker_session.go
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DockerSessionConfig holds configuration for Docker session containers.
type DockerSessionConfig struct {
	Image        string
	NetworkMode  string
	MemoryLimit  string
	CPULimit     string
	AllowedDirs  []string
	ReadonlyDirs []string
	IdleTimeout  time.Duration
}

// DockerSession represents a running container bound to an agent session.
type DockerSession struct {
	containerID string
	sessionID   string
	createdAt   time.Time
	lastUsedAt  time.Time
}

// DockerSessionManager manages per-session Docker containers.
type DockerSessionManager struct {
	mu        sync.Mutex
	sessions  map[string]*DockerSession
	config    DockerSessionConfig
	available bool
	stopCh    chan struct{}
	stopped   bool
}

// NewDockerSessionManager creates a session manager.
func NewDockerSessionManager(cfg DockerSessionConfig, dockerAvailable bool) *DockerSessionManager {
	mgr := &DockerSessionManager{
		sessions:  make(map[string]*DockerSession),
		config:    cfg,
		available: dockerAvailable,
		stopCh:    make(chan struct{}),
	}
	if dockerAvailable {
		go mgr.idleReaper()
	}
	return mgr
}

// Available returns whether Docker is available.
func (m *DockerSessionManager) Available() bool { return m.available }

// GetOrCreate returns an existing session or creates a new container.
func (m *DockerSessionManager) GetOrCreate(ctx context.Context, sessionID string) (*DockerSession, error) {
	if !m.available {
		return nil, fmt.Errorf("docker sandbox unavailable")
	}

	m.mu.Lock()
	if s, ok := m.sessions[sessionID]; ok {
		s.lastUsedAt = time.Now()
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	containerID, err := m.createContainer(ctx, sessionID)
	if err != nil {
		m.available = false
		return nil, fmt.Errorf("create container: %w", err)
	}

	session := &DockerSession{
		containerID: containerID,
		sessionID:   sessionID,
		createdAt:   time.Now(),
		lastUsedAt:  time.Now(),
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	slog.Info("sandbox: container created", "session", sessionID, "container", containerID[:12])
	return session, nil
}

// Exec runs a command in the session's container.
func (s *DockerSession) Exec(ctx context.Context, command string) (stdout, stderr string, exitCode int, duration time.Duration, err error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "docker", "exec", s.containerID, "bash", "-c", command)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	execErr := cmd.Run()
	duration = time.Since(start)
	s.lastUsedAt = time.Now()

	exitCode = 0
	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, duration, fmt.Errorf("docker exec: %w", execErr)
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, duration, nil
}

func (m *DockerSessionManager) createContainer(ctx context.Context, sessionID string) (string, error) {
	name := fmt.Sprintf("ironclaw-sandbox-%s", sessionID)
	args := []string{
		"create", "--name", name,
		"--label", "ironclaw=sandbox",
		"--network", m.config.NetworkMode,
	}

	if m.config.MemoryLimit != "" {
		args = append(args, "--memory", m.config.MemoryLimit)
	}
	if m.config.CPULimit != "" {
		args = append(args, "--cpus", m.config.CPULimit)
	}

	for _, dir := range m.config.AllowedDirs {
		args = append(args, "-v", fmt.Sprintf("%s:%s", dir, dir))
	}
	for _, dir := range m.config.ReadonlyDirs {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", dir, dir))
	}

	args = append(args, m.config.Image, "sleep", "infinity")

	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", out.String(), err)
	}

	containerID := strings.TrimSpace(out.String())

	startCmd := exec.CommandContext(ctx, "docker", "start", containerID)
	if err := startCmd.Run(); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return containerID, nil
}

// Remove stops and removes a session's container.
func (m *DockerSessionManager) Remove(sessionID string) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "docker", "rm", "-f", s.containerID).Run()
		slog.Info("sandbox: container removed", "session", sessionID)
	}
}

// CleanupAll removes all managed containers.
func (m *DockerSessionManager) CleanupAll() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		close(m.stopCh)
	}
	ids := make([]string, 0, len(m.sessions))
	for sid := range m.sessions {
		ids = append(ids, sid)
	}
	m.mu.Unlock()

	for _, sid := range ids {
		m.Remove(sid)
	}
}

// CleanupOrphans removes any leftover ironclaw-sandbox containers from previous runs.
func CleanupOrphans(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "label=ironclaw=sandbox", "--format", "{{.ID}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return
	}
	for _, id := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if id != "" {
			exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
			slog.Info("sandbox: orphan container cleaned", "container", id)
		}
	}
}

func (m *DockerSessionManager) idleReaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

func (m *DockerSessionManager) reapIdle() {
	m.mu.Lock()
	var toRemove []string
	for sid, s := range m.sessions {
		if time.Since(s.lastUsedAt) > m.config.IdleTimeout {
			toRemove = append(toRemove, sid)
		}
	}
	m.mu.Unlock()

	for _, sid := range toRemove {
		slog.Info("sandbox: reaping idle container", "session", sid)
		m.Remove(sid)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestDockerSessionManager ./internal/sandbox/ -v`
Expected: PASS (3 tests — these don't require Docker)

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/docker_probe.go internal/sandbox/docker_session.go internal/sandbox/docker_session_test.go
git commit -m "feat(sandbox): add DockerSessionManager with session containers, idle reaping, and orphan cleanup"
```

---

### Task 8: Sandbox Interceptor

**Files:**
- Create: `internal/tool/interceptor_sandbox.go`
- Test: `internal/tool/interceptor_sandbox_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/tool/interceptor_sandbox_test.go
package tool

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

func TestSandboxInterceptor_FileBlocked(t *testing.T) {
	fg, _ := sandbox.NewFileGuard([]string{"/tmp/allowed"}, nil)
	si := NewSandboxInterceptor(nil, fg, nil, true)

	res, err := si.Intercept(context.Background(), &ToolCall{
		ToolName: "file_write",
		Input:    `{"path":"/etc/passwd","content":"hacked"}`,
	}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		t.Fatal("next should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Error("should return error for path outside allowed dirs")
	}
}

func TestSandboxInterceptor_FileAllowed(t *testing.T) {
	fg, _ := sandbox.NewFileGuard([]string{"/tmp"}, nil)
	si := NewSandboxInterceptor(nil, fg, nil, true)

	called := false
	_, err := si.Intercept(context.Background(), &ToolCall{
		ToolName: "file_write",
		Input:    `{"path":"/tmp/test.txt","content":"ok"}`,
	}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("next should be called for allowed path")
	}
}

func TestSandboxInterceptor_HTTPBlocked(t *testing.T) {
	np := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})
	si := NewSandboxInterceptor(nil, nil, np, true)

	res, err := si.Intercept(context.Background(), &ToolCall{
		ToolName: "http",
		Input:    `{"method":"GET","url":"http://evil.com/api"}`,
	}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		t.Fatal("next should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Error("should block blacklisted URL")
	}
}

func TestSandboxInterceptor_Disabled(t *testing.T) {
	si := NewSandboxInterceptor(nil, nil, nil, false)
	called := false
	_, err := si.Intercept(context.Background(), &ToolCall{
		ToolName: "bash",
		Input:    `{"command":"rm -rf /"}`,
	}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("disabled sandbox should pass through")
	}
}

func TestSandboxInterceptor_UnknownTool(t *testing.T) {
	fg, _ := sandbox.NewFileGuard([]string{"/tmp"}, nil)
	si := NewSandboxInterceptor(nil, fg, nil, true)

	called := false
	_, err := si.Intercept(context.Background(), &ToolCall{
		ToolName: "memory_manage",
		Input:    `{"action":"list"}`,
	}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("unknown tool type should pass through")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSandboxInterceptor ./internal/tool/ -v`
Expected: FAIL — `NewSandboxInterceptor` not defined

- [ ] **Step 3: Implement SandboxInterceptor**

```go
// internal/tool/interceptor_sandbox.go
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

// SandboxInterceptor routes tool calls through appropriate sandbox checks.
type SandboxInterceptor struct {
	dockerMgr     *sandbox.DockerSessionManager
	fileGuard     *sandbox.FileGuard
	networkPolicy *sandbox.NetworkPolicy
	enabled       bool
}

// NewSandboxInterceptor creates a sandbox interceptor.
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

	path := extractFilePathFromInput(call.Input)
	if path == "" {
		return next(ctx, call)
	}

	isWrite := call.ToolName == "file_write" || call.ToolName == "file_edit"
	if err := s.fileGuard.ValidateAccess(path, isWrite); err != nil {
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
	if err := json.Unmarshal([]byte(call.Input), &parsed); err == nil && parsed.URL != "" {
		if err := s.networkPolicy.CheckURL(parsed.URL); err != nil {
			return &ToolResult{Error: fmt.Sprintf("sandbox: %s", err)}, nil
		}
	}

	return next(ctx, call)
}

func extractFilePathFromInput(input string) string {
	var parsed struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(input), &parsed) == nil {
		return parsed.Path
	}
	return ""
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSandboxInterceptor ./internal/tool/ -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/tool/interceptor_sandbox.go internal/tool/interceptor_sandbox_test.go
git commit -m "feat(sandbox): add SandboxInterceptor dispatching bash/file/http to appropriate guards"
```

---

### Task 9: Hook Interceptor

**Files:**
- Create: `internal/tool/interceptor_hook.go`
- Test: `internal/tool/interceptor_hook_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/tool/interceptor_hook_test.go
package tool

import (
	"context"
	"testing"
)

func TestHookInterceptor_NoHookManager(t *testing.T) {
	hi := NewHookInterceptor(nil)
	called := false
	_, err := hi.Intercept(context.Background(), &ToolCall{ToolName: "bash"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("should pass through when no hook manager")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestHookInterceptor ./internal/tool/ -v`
Expected: FAIL

- [ ] **Step 3: Implement HookInterceptor**

```go
// internal/tool/interceptor_hook.go
package tool

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/hook"
)

// HookInterceptor fires pre-tool-use hooks before execution.
type HookInterceptor struct {
	hookMgr *hook.Manager
}

// NewHookInterceptor creates a hook interceptor.
func NewHookInterceptor(hookMgr *hook.Manager) *HookInterceptor {
	return &HookInterceptor{hookMgr: hookMgr}
}

func (h *HookInterceptor) Name() string { return "hook" }

func (h *HookInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if h.hookMgr == nil || !h.hookMgr.HasPreToolUseHandlers() {
		return next(ctx, call)
	}

	hookResult, hookErr := h.hookMgr.FirePreToolUse(ctx, hook.PreToolUseEvent{
		ToolName: call.ToolName,
		Input:    call.Input,
	})

	if hookErr == nil {
		switch hookResult.Action {
		case "deny":
			return &ToolResult{Error: "denied by hook: " + hookResult.Reason}, nil
		}
	}

	return next(ctx, call)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestHookInterceptor ./internal/tool/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tool/interceptor_hook.go internal/tool/interceptor_hook_test.go
git commit -m "feat(sandbox): add HookInterceptor wrapping pre_tool_use hooks"
```

---

### Task 10: Gateway Integration

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/init_tools.go`
- Modify: `internal/agent/runtime.go`
- Modify: `internal/agent/concurrent.go`
- Modify: `internal/agent/act.go`

- [ ] **Step 1: Add fields to Gateway struct**

In `internal/gateway/gateway.go`, add to the `Gateway` struct:

```go
dockerSessionMgr *sandbox.DockerSessionManager
interceptorChain *tool.InterceptorChain
```

Add import: `"github.com/Forest-Isle/IronClaw/internal/sandbox"`

- [ ] **Step 2: Add Docker cleanup to Gateway.Stop()**

In `internal/gateway/gateway.go`, add before `_ = gw.db.Close()` in `Stop()`:

```go
if gw.dockerSessionMgr != nil {
	gw.dockerSessionMgr.CleanupAll()
}
```

- [ ] **Step 3: Update init_tools.go to build interceptor chain**

Replace the end of `initToolsAndHooks()` in `internal/gateway/init_tools.go` with sandbox initialization:

```go
func (gw *Gateway) initToolsAndHooks() error {
	// ... existing tool registration (unchanged) ...

	// ... existing hook initialization (unchanged) ...

	// Permission engine (unchanged)
	permRules := make([]tool.PermissionRule, len(gw.cfg.Permissions.Rules))
	for i, r := range gw.cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	gw.permEngine = tool.NewPermissionEngine(permRules, gw.cfg.Permissions.Default, policy)

	// Sandbox components
	var fileGuard *sandbox.FileGuard
	var networkPolicy *sandbox.NetworkPolicy
	sandboxEnabled := gw.cfg.Sandbox.Enabled

	if sandboxEnabled {
		var err error
		if len(gw.cfg.Sandbox.AllowedDirectories) > 0 {
			fileGuard, err = sandbox.NewFileGuard(gw.cfg.Sandbox.AllowedDirectories, gw.cfg.Sandbox.ReadonlyDirectories)
			if err != nil {
				slog.Warn("sandbox: FileGuard init failed, disabled", "err", err)
			}
		}
		networkPolicy = sandbox.NewNetworkPolicy(
			gw.cfg.Sandbox.Network.Mode,
			gw.cfg.Sandbox.Network.Whitelist,
			gw.cfg.Sandbox.Network.Blacklist,
		)
		if gw.cfg.Sandbox.Bash.Backend == "docker" {
			sandbox.CleanupOrphans(context.Background())
			available := sandbox.ProbeDocker(context.Background())
			if !available {
				slog.Warn("sandbox: Docker not available, bash will run on host")
			}
			gw.dockerSessionMgr = sandbox.NewDockerSessionManager(sandbox.DockerSessionConfig{
				Image:        gw.cfg.Sandbox.Bash.Docker.Image,
				NetworkMode:  gw.cfg.Sandbox.Bash.Docker.Network,
				MemoryLimit:  gw.cfg.Sandbox.Bash.Docker.MemoryLimit,
				CPULimit:     gw.cfg.Sandbox.Bash.Docker.CPULimit,
				AllowedDirs:  gw.cfg.Sandbox.AllowedDirectories,
				ReadonlyDirs: gw.cfg.Sandbox.ReadonlyDirectories,
				IdleTimeout:  gw.cfg.Sandbox.Bash.Docker.IdleTimeout,
			}, available)
		}
	}

	// Build interceptor chain: permission → hook → sandbox
	interceptors := []tool.ToolInterceptor{
		tool.NewPermissionInterceptor(gw.permEngine, nil, nil),
		tool.NewHookInterceptor(gw.hookMgr),
		tool.NewSandboxInterceptor(gw.dockerSessionMgr, fileGuard, networkPolicy, sandboxEnabled),
	}
	gw.interceptorChain = tool.NewInterceptorChain(interceptors)

	slog.Info("sandbox system initialized", "enabled", sandboxEnabled)
	return nil
}
```

Add imports: `"context"`, `"github.com/Forest-Isle/IronClaw/internal/sandbox"`

- [ ] **Step 4: Add SetInterceptorChain to Runtime**

In `internal/agent/runtime.go`, add a field and setter:

```go
// In Runtime struct:
interceptorChain *tool.InterceptorChain

// Setter:
func (r *Runtime) SetInterceptorChain(chain *tool.InterceptorChain) {
	r.interceptorChain = chain
}
```

- [ ] **Step 5: Add SetInterceptorChain to Executor**

In `internal/agent/act.go`, add a field and setter:

```go
// In Executor struct:
interceptorChain *tool.InterceptorChain

// Setter:
func (e *Executor) SetInterceptorChain(chain *tool.InterceptorChain) {
	e.interceptorChain = chain
}
```

- [ ] **Step 6: Wire interceptor chain in gateway init_agent.go**

Find where `runtime.SetPermissionEngine` is called and add after it:

```go
runtime.SetInterceptorChain(gw.interceptorChain)
```

Do the same for the Executor in cognitive agent init.

- [ ] **Step 7: Run all existing tests to verify nothing breaks**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/gateway/ ./internal/agent/ ./internal/tool/ -v -count=1`
Expected: PASS (existing tests unchanged)

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/init_tools.go internal/agent/runtime.go internal/agent/act.go
git commit -m "feat(sandbox): wire interceptor chain through gateway → runtime → executor"
```

---

### Task 11: Refactor executeToolCall to Use Interceptor Chain

**Files:**
- Modify: `internal/agent/concurrent.go`
- Modify: `internal/agent/act.go`

This task refactors the permission/hook/execute logic in both `concurrent.go` (simple mode) and `act.go` (cognitive mode) to use the interceptor chain when available, falling back to existing logic when not set (backward compat).

- [ ] **Step 1: Refactor concurrent.go executeToolCall**

In `internal/agent/concurrent.go`, add a new method and modify `executeToolCall` to try the interceptor chain first:

At the beginning of `executeToolCall`, after the speculative execution check, add:

```go
// If interceptor chain is configured, use it for permission + sandbox + execution
if r.interceptorChain != nil {
	return r.executeToolCallViaChain(ctx, ch, sess, target, tc, t)
}
```

Add the new method:

```go
func (r *Runtime) executeToolCallViaChain(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	tc ToolUseBlock,
	t tool.Tool,
) toolResult {
	call := &tool.ToolCall{
		ToolName:  tc.Name,
		Input:     tc.Input,
		SessionID: sess.ID,
	}

	start := time.Now()
	res, err := r.interceptorChain.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		result, execErr := t.Execute(ctx, []byte(call.Input))
		if execErr != nil {
			return &tool.ToolResult{Error: execErr.Error()}, nil
		}
		return &tool.ToolResult{Output: result.Output, Error: result.Error, Metadata: result.Metadata}, nil
	})
	duration := time.Since(start).Milliseconds()

	if err != nil {
		return toolResult{toolUseID: tc.ID, output: "error: " + err.Error(), status: "error", duration: duration, toolName: tc.Name, toolInput: tc.Input}
	}

	if res.Error != "" {
		return toolResult{toolUseID: tc.ID, output: res.Error, status: "denied", duration: duration, toolName: tc.Name, toolInput: tc.Input}
	}

	output := res.Output
	if r.resultStore != nil && r.resultStore.ShouldPersist(output) {
		if stored, storeErr := r.resultStore.Store(sess.ID, tc.ID, output); storeErr == nil {
			output = stored.Preview
		}
	}
	if r.compressor != nil {
		output = r.compressor.CompressToolResult(output)
	}

	// PostToolUse hooks (fires after chain completes)
	if r.hookMgr != nil && r.hookMgr.HasPostToolUseHandlers() {
		postResult, _ := r.hookMgr.FirePostToolUse(ctx, hook.PostToolUseEvent{
			ToolName:   tc.Name,
			Input:      tc.Input,
			Output:     output,
			Status:     "success",
			DurationMs: duration,
			SessionID:  sess.ID,
		})
		if postResult.ModifiedOutput != nil {
			output = *postResult.ModifiedOutput
		}
	}

	return toolResult{toolUseID: tc.ID, output: output, status: "success", duration: duration, toolName: tc.Name, toolInput: tc.Input}
}
```

- [ ] **Step 2: Apply similar refactor to act.go executeSubTask**

In `internal/agent/act.go`, add a check at the start of `executeSubTask` (after tool lookup and cache check):

```go
if e.interceptorChain != nil {
	return e.executeSubTaskViaChain(ctx, ch, sess, target, subtask, allTasks, taskCtx, plan, rlState, collector, t)
}
```

Add the new method following the same pattern as `executeToolCallViaChain`, but preserving the cognitive agent's RL recording, TaskContext, and session message tracking.

- [ ] **Step 3: Run all tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ ./internal/tool/ -v -count=1`
Expected: PASS (all existing tests pass, new chain paths are only active when chain is set)

- [ ] **Step 4: Commit**

```bash
git add internal/agent/concurrent.go internal/agent/act.go
git commit -m "feat(sandbox): route tool execution through interceptor chain in both simple and cognitive modes"
```

---

### Task 12: Update Example Config

**Files:**
- Modify: `configs/ironclaw.example.yaml`

- [ ] **Step 1: Add sandbox section to example config**

Append the following after the `permissions:` section:

```yaml
# Security Sandbox — execution isolation for tools
sandbox:
  enabled: false                     # set true to enable sandbox system

  allowed_directories:               # file tools + Docker volumes; empty = no restriction
    # - "${WORKSPACE_DIR}"
    # - "/tmp/ironclaw"
  readonly_directories:              # read-only access (both file tools and Docker)
    # - "${HOME}/.ssh"

  bash:
    backend: host                    # "docker" (container isolation) | "host" (direct execution)
    docker:
      image: "ironclaw-sandbox:latest"
      network: none                  # none | bridge | host
      memory_limit: "512m"
      cpu_limit: "1.0"
      idle_timeout: 30m

  network:
    mode: blacklist                  # none | blacklist | whitelist
    blacklist: []                    # appended to built-in SSRF blocklist
    whitelist: []                    # only used in whitelist mode
```

- [ ] **Step 2: Verify config loads with new section**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/config/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add configs/ironclaw.example.yaml
git commit -m "docs: add sandbox configuration section to example config"
```

---

### Task 13: Full Integration Test

**Files:**
- Create: `internal/tool/interceptor_integration_test.go`

- [ ] **Step 1: Write integration test**

```go
// internal/tool/interceptor_integration_test.go
package tool

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

func TestInterceptorChain_FullPipeline(t *testing.T) {
	// Setup: permission deny for "rm" commands, file guard for /tmp only
	rules := []PermissionRule{
		{Tool: "bash", Pattern: "rm *", Action: "deny"},
		{Tool: "bash", Action: "none"},
		{Tool: "file_write", Action: "none"},
		{Tool: "http", Action: "none"},
	}
	pe := NewPermissionEngine(rules, "approve", nil)
	fg, _ := sandbox.NewFileGuard([]string{"/tmp"}, nil)
	np := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})

	chain := NewInterceptorChain([]ToolInterceptor{
		NewPermissionInterceptor(pe, &mockNotifier{}, &mockApprover{approve: true}),
		NewHookInterceptor(nil),
		NewSandboxInterceptor(nil, fg, np, true),
	})

	exec := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "executed: " + call.ToolName}, nil
	}

	tests := []struct {
		name     string
		call     *ToolCall
		wantExec bool
		wantErr  bool
	}{
		{
			name:     "bash ls allowed",
			call:     &ToolCall{ToolName: "bash", Input: `{"command":"ls -la"}`},
			wantExec: true,
		},
		{
			name:     "bash rm denied by permission",
			call:     &ToolCall{ToolName: "bash", Input: `{"command":"rm -rf /"}`},
			wantExec: false,
			wantErr:  true,
		},
		{
			name:     "file_write inside /tmp allowed",
			call:     &ToolCall{ToolName: "file_write", Input: `{"path":"/tmp/test.txt","content":"ok"}`},
			wantExec: true,
		},
		{
			name:     "file_write outside /tmp denied",
			call:     &ToolCall{ToolName: "file_write", Input: `{"path":"/etc/test.txt","content":"ok"}`},
			wantExec: false,
			wantErr:  true,
		},
		{
			name:     "http to safe host allowed",
			call:     &ToolCall{ToolName: "http", Input: `{"method":"GET","url":"http://safe.com/"}`},
			wantExec: true,
		},
		{
			name:     "http to evil host denied",
			call:     &ToolCall{ToolName: "http", Input: `{"method":"GET","url":"http://evil.com/"}`},
			wantExec: false,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := chain.Execute(context.Background(), tt.call, exec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			executed := res.Error == ""
			if executed != tt.wantExec {
				t.Errorf("executed=%v, want %v (error=%q)", executed, tt.wantExec, res.Error)
			}
		})
	}
}
```

- [ ] **Step 2: Run integration test**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestInterceptorChain_FullPipeline ./internal/tool/ -v`
Expected: PASS (6 sub-tests)

- [ ] **Step 3: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
Expected: PASS (all existing + new tests)

- [ ] **Step 4: Commit**

```bash
git add internal/tool/interceptor_integration_test.go
git commit -m "test(sandbox): add full pipeline integration test for interceptor chain"
```

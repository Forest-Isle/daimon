# Feature Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a Feature Registry that manages feature lifecycle (register, detect, enable/disable) and slash commands for runtime control, replacing scattered `if cfg.XXX.Enabled` gates.

**Architecture:** New `internal/feature/` package provides `Registry` with topological dependency resolution and auto-detection. Gateway uses `Registry.IsEnabled()` instead of config flags. New slash commands (`/feature`, `/config`, `/compact`, `/model`) added to gateway + TUI.

**Tech Stack:** Go `sync.RWMutex`, Kahn's algorithm for topo sort, existing gateway command interception pattern.

---

## File Map

| File | Change |
|------|--------|
| `internal/feature/feature.go` | Create: Feature struct, Phase enum, DetectResult, FeatureInfo |
| `internal/feature/registry.go` | Create: Registry with register, resolve, enable/disable, list |
| `internal/feature/registry_test.go` | Create: Unit tests for registry |
| `internal/gateway/gateway.go` | Modify: add `features` field, add command interception for `/feature`, `/config`, `/compact`, `/model` |
| `internal/gateway/features.go` | Create: `registerFeatures()`, `configToOverrides()` |
| `internal/gateway/command_feature.go` | Create: command handlers |
| `internal/channel/tui/commands.go` | Modify: register new commands in `commandRegistry` |

---

## Worktree A: Feature Registry Core (`feature/feature-registry-core`)

### Task A1: Feature and DetectResult types

**Files:**
- Create: `internal/feature/feature.go`

- [ ] **Step 1: Create the feature package with core types**

```go
package feature

import "context"

type Phase int

const (
	PhaseConstruct  Phase = iota
	PhaseStart
	PhaseBackground
)

func (p Phase) String() string {
	switch p {
	case PhaseConstruct:
		return "construct"
	case PhaseStart:
		return "start"
	case PhaseBackground:
		return "background"
	default:
		return "unknown"
	}
}

type DetectResult struct {
	Available bool
	Reason    string
}

type Feature struct {
	Name         string
	Description  string
	Default      bool
	Phase        Phase
	Dependencies []string
	AutoDetect   func(ctx context.Context) DetectResult
	OnEnable     func(ctx context.Context) error
	OnDisable    func(ctx context.Context) error
}

type FeatureInfo struct {
	Name        string
	Description string
	Enabled     bool
	Reason      string
	Phase       Phase
	Dependencies []string
}
```

- [ ] **Step 2: Verify it compiles**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/feature/
```

Expected: success (no errors).

- [ ] **Step 3: Commit**

```bash
git add internal/feature/feature.go
git commit -m "feat(feature): add Feature, Phase, DetectResult core types"
```

---

### Task A2: Registry — register and query

**Files:**
- Create: `internal/feature/registry.go`
- Create: `internal/feature/registry_test.go`

- [ ] **Step 1: Write failing test for Register + IsEnabled**

```go
package feature

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndIsEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name:    "test_feature",
		Default: true,
	})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.True(t, r.IsEnabled("test_feature"))
	assert.False(t, r.IsEnabled("nonexistent"))
}

func TestRegisterDefaultFalse(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name:    "opt_in",
		Default: false,
	})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.False(t, r.IsEnabled("opt_in"))
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestRegister ./internal/feature/ -v
```

Expected: FAIL — `NewRegistry` not defined.

- [ ] **Step 3: Implement Registry with Register, ResolveAndInit, IsEnabled**

```go
package feature

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

type featureState struct {
	Feature
	enabled  bool
	reason   string
	initDone bool
}

type Registry struct {
	mu       sync.RWMutex
	features map[string]*featureState
	order    []string
	resolved bool
}

func NewRegistry() *Registry {
	return &Registry{
		features: make(map[string]*featureState),
	}
}

func (r *Registry) Register(f Feature) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[f.Name] = &featureState{Feature: f}
}

func (r *Registry) ApplyOverrides(overrides map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, enabled := range overrides {
		if fs, ok := r.features[name]; ok {
			fs.enabled = enabled
			fs.reason = "config override"
		}
	}
}

func (r *Registry) ResolveAndInit(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	order, err := r.topoSort()
	if err != nil {
		return fmt.Errorf("dependency resolution: %w", err)
	}
	r.order = order

	for _, name := range order {
		fs := r.features[name]

		if fs.reason != "config override" {
			fs.enabled = fs.Default
			if fs.enabled {
				fs.reason = "default"
			} else {
				fs.reason = "default (opt-in)"
			}
		}

		if fs.AutoDetect != nil {
			result := fs.AutoDetect(ctx)
			if !result.Available {
				fs.enabled = false
				fs.reason = fmt.Sprintf("auto-detect: %s", result.Reason)
				slog.Info("feature auto-detect disabled", "feature", name, "reason", result.Reason)
				continue
			}
		}

		if !fs.enabled {
			continue
		}

		if !r.depsEnabled(fs.Dependencies) {
			fs.enabled = false
			fs.reason = "dependency not available"
			continue
		}

		if fs.OnEnable != nil {
			if err := fs.OnEnable(ctx); err != nil {
				slog.Warn("feature init failed, disabling", "feature", name, "err", err)
				fs.enabled = false
				fs.reason = fmt.Sprintf("init error: %s", err)
				continue
			}
		}
		fs.initDone = true
	}

	r.resolved = true
	return nil
}

func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if fs, ok := r.features[name]; ok {
		return fs.enabled
	}
	return false
}

func (r *Registry) depsEnabled(deps []string) bool {
	for _, dep := range deps {
		if fs, ok := r.features[dep]; !ok || !fs.enabled {
			return false
		}
	}
	return true
}

func (r *Registry) topoSort() ([]string, error) {
	inDegree := make(map[string]int)
	for name := range r.features {
		inDegree[name] = 0
	}
	for _, fs := range r.features {
		for _, dep := range fs.Dependencies {
			if _, ok := r.features[dep]; !ok {
				return nil, fmt.Errorf("feature %q depends on unknown feature %q", fs.Name, dep)
			}
			inDegree[fs.Name]++
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var order []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)

		for other, fs := range r.features {
			for _, dep := range fs.Dependencies {
				if dep == name {
					inDegree[other]--
					if inDegree[other] == 0 {
						queue = append(queue, other)
					}
				}
			}
		}
	}

	if len(order) != len(r.features) {
		return nil, fmt.Errorf("circular dependency detected")
	}
	return order, nil
}
```

- [ ] **Step 4: Run tests**

```bash
CGO_ENABLED=1 go test -tags fts5 -run TestRegister ./internal/feature/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/feature/registry.go internal/feature/registry_test.go
git commit -m "feat(feature): implement Registry with register, resolve, topo sort"
```

---

### Task A3: Override, dependency resolution, and auto-detect tests

**Files:**
- Modify: `internal/feature/registry_test.go`

- [ ] **Step 1: Write tests for overrides and dependencies**

```go
func TestApplyOverrides(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "a", Default: false})
	r.ApplyOverrides(map[string]bool{"a": true})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.True(t, r.IsEnabled("a"))
}

func TestOverrideDisablesDefault(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "a", Default: true})
	r.ApplyOverrides(map[string]bool{"a": false})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.False(t, r.IsEnabled("a"))
}

func TestDependencyOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "base", Default: true})
	r.Register(Feature{Name: "child", Default: true, Dependencies: []string{"base"}})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.True(t, r.IsEnabled("base"))
	assert.True(t, r.IsEnabled("child"))
}

func TestDependencyDisabledCascade(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "base", Default: false})
	r.Register(Feature{Name: "child", Default: true, Dependencies: []string{"base"}})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.False(t, r.IsEnabled("base"))
	assert.False(t, r.IsEnabled("child"))
}

func TestCircularDependency(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "a", Default: true, Dependencies: []string{"b"}})
	r.Register(Feature{Name: "b", Default: true, Dependencies: []string{"a"}})
	err := r.ResolveAndInit(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circular")
}

func TestAutoDetectDisables(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name:    "docker_feature",
		Default: true,
		AutoDetect: func(ctx context.Context) DetectResult {
			return DetectResult{Available: false, Reason: "Docker not found"}
		},
	})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.False(t, r.IsEnabled("docker_feature"))
}

func TestAutoDetectAllows(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name:    "docker_feature",
		Default: true,
		AutoDetect: func(ctx context.Context) DetectResult {
			return DetectResult{Available: true}
		},
	})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.True(t, r.IsEnabled("docker_feature"))
}

func TestOnEnableError(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name:    "broken",
		Default: true,
		OnEnable: func(ctx context.Context) error {
			return fmt.Errorf("init failed")
		},
	})
	err := r.ResolveAndInit(context.Background())
	require.NoError(t, err)
	assert.False(t, r.IsEnabled("broken"))
}
```

- [ ] **Step 2: Run all tests**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/feature/ -v
```

Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add internal/feature/registry_test.go
git commit -m "test(feature): add override, dependency, auto-detect, and error tests"
```

---

### Task A4: Runtime Enable/Disable and List

**Files:**
- Modify: `internal/feature/registry.go`
- Modify: `internal/feature/registry_test.go`

- [ ] **Step 1: Write failing tests for Enable, Disable, List**

```go
func TestRuntimeEnable(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name: "lazy", Default: false,
		OnEnable: func(ctx context.Context) error { return nil },
	})
	require.NoError(t, r.ResolveAndInit(context.Background()))
	assert.False(t, r.IsEnabled("lazy"))

	require.NoError(t, r.Enable(context.Background(), "lazy"))
	assert.True(t, r.IsEnabled("lazy"))
}

func TestRuntimeDisable(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{
		Name: "active", Default: true,
		OnDisable: func(ctx context.Context) error { return nil },
	})
	require.NoError(t, r.ResolveAndInit(context.Background()))
	assert.True(t, r.IsEnabled("active"))

	require.NoError(t, r.Disable(context.Background(), "active"))
	assert.False(t, r.IsEnabled("active"))
}

func TestDisableBlockedByDependent(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "base", Default: true})
	r.Register(Feature{Name: "child", Default: true, Dependencies: []string{"base"}})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	err := r.Disable(context.Background(), "base")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "child")
}

func TestList(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "a", Description: "Feature A", Default: true})
	r.Register(Feature{Name: "b", Description: "Feature B", Default: false})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	list := r.List()
	assert.Len(t, list, 2)
}

func TestEnableUnknownFeature(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.ResolveAndInit(context.Background()))
	err := r.Enable(context.Background(), "nonexistent")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
CGO_ENABLED=1 go test -tags fts5 -run "TestRuntime|TestDisableBlocked|TestList|TestEnableUnknown" ./internal/feature/ -v
```

Expected: FAIL — `Enable`, `Disable`, `List` not defined.

- [ ] **Step 3: Implement Enable, Disable, List, EnabledNames**

Add to `internal/feature/registry.go`:

```go
func (r *Registry) Enable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	fs, ok := r.features[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	if fs.enabled {
		return nil
	}

	if fs.AutoDetect != nil {
		result := fs.AutoDetect(ctx)
		if !result.Available {
			return fmt.Errorf("cannot enable %q: %s", name, result.Reason)
		}
	}

	for _, dep := range fs.Dependencies {
		if depFS, ok := r.features[dep]; !ok || !depFS.enabled {
			return fmt.Errorf("cannot enable %q: dependency %q is not enabled", name, dep)
		}
	}

	if fs.OnEnable != nil {
		if err := fs.OnEnable(ctx); err != nil {
			return fmt.Errorf("enable %q: %w", name, err)
		}
	}
	fs.enabled = true
	fs.initDone = true
	fs.reason = "runtime enable"
	return nil
}

func (r *Registry) Disable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	fs, ok := r.features[name]
	if !ok {
		return fmt.Errorf("unknown feature %q", name)
	}
	if !fs.enabled {
		return nil
	}

	var dependents []string
	for otherName, otherFS := range r.features {
		if !otherFS.enabled {
			continue
		}
		for _, dep := range otherFS.Dependencies {
			if dep == name {
				dependents = append(dependents, otherName)
			}
		}
	}
	if len(dependents) > 0 {
		return fmt.Errorf("cannot disable %q: required by %v", name, dependents)
	}

	if fs.OnDisable != nil {
		if err := fs.OnDisable(ctx); err != nil {
			slog.Error("feature disable error", "feature", name, "err", err)
		}
	}
	fs.enabled = false
	fs.initDone = false
	fs.reason = "runtime disable"
	return nil
}

func (r *Registry) List() []FeatureInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]FeatureInfo, 0, len(r.features))
	order := r.order
	if len(order) == 0 {
		for name := range r.features {
			order = append(order, name)
		}
	}
	for _, name := range order {
		fs := r.features[name]
		list = append(list, FeatureInfo{
			Name:         fs.Name,
			Description:  fs.Description,
			Enabled:      fs.enabled,
			Reason:       fs.reason,
			Phase:        fs.Phase,
			Dependencies: fs.Dependencies,
		})
	}
	return list
}

func (r *Registry) EnabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, fs := range r.features {
		if fs.enabled {
			names = append(names, name)
		}
	}
	return names
}
```

- [ ] **Step 4: Run all tests**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/feature/ -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/feature/registry.go internal/feature/registry_test.go
git commit -m "feat(feature): add runtime Enable, Disable, List methods with dependency checks"
```

---

## Worktree B: Slash Commands + TUI Registration (`feature/slash-commands`)

### Task B1: Register new commands in TUI

**Files:**
- Modify: `internal/channel/tui/commands.go`

- [ ] **Step 1: Add new commands to commandRegistry**

In `internal/channel/tui/commands.go`, append these entries to the `commandRegistry` slice, before the closing `}`:

```go
	// Feature management
	{
		Name:        "feature",
		Description: "List, enable, or disable features. Usage: /feature [list|enable|disable] [name]",
		ArgHint:     "[list|enable|disable] [name]",
		Category:    "builtin",
	},

	// Config inspection
	{
		Name:        "config",
		Description: "Show current effective configuration",
		ArgHint:     "show",
		Category:    "builtin",
	},

	// Context compression
	{
		Name:        "compact",
		Description: "Manually trigger context compression",
		Category:    "builtin",
	},

	// Model switching
	{
		Name:        "model",
		Description: "Show or switch the current LLM model. Usage: /model [name]",
		ArgHint:     "[model_name]",
		Category:    "builtin",
	},
```

- [ ] **Step 2: Verify it compiles**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/channel/tui/
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/channel/tui/commands.go
git commit -m "feat(tui): register /feature, /config, /compact, /model commands"
```

---

### Task B2: Gateway command handlers

**Files:**
- Create: `internal/gateway/command_feature.go`

- [ ] **Step 1: Create command handler file**

```go
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

func (gw *Gateway) handleFeatureCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, args string) {
	args = strings.TrimSpace(args)

	if args == "" || args == "list" {
		gw.sendFeatureList(ctx, ch, msg)
		return
	}

	parts := strings.SplitN(args, " ", 2)
	action := parts[0]
	name := ""
	if len(parts) > 1 {
		name = strings.TrimSpace(parts[1])
	}

	switch action {
	case "enable":
		if name == "" {
			gw.sendReply(ctx, ch, msg, "Usage: /feature enable <name>")
			return
		}
		if gw.features == nil {
			gw.sendReply(ctx, ch, msg, "⚠️ Feature registry not initialized")
			return
		}
		if err := gw.features.Enable(ctx, name); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ Cannot enable %s: %s", name, err))
			return
		}
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Feature %s enabled", name))

	case "disable":
		if name == "" {
			gw.sendReply(ctx, ch, msg, "Usage: /feature disable <name>")
			return
		}
		if gw.features == nil {
			gw.sendReply(ctx, ch, msg, "⚠️ Feature registry not initialized")
			return
		}
		if err := gw.features.Disable(ctx, name); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ Cannot disable %s: %s", name, err))
			return
		}
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Feature %s disabled", name))

	default:
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("Unknown action %q. Usage: /feature [list|enable|disable] [name]", action))
	}
}

func (gw *Gateway) sendFeatureList(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	if gw.features == nil {
		gw.sendReply(ctx, ch, msg, "⚠️ Feature registry not initialized")
		return
	}

	list := gw.features.List()
	var sb strings.Builder
	sb.WriteString("📋 Feature Status:\n\n")

	for _, f := range list {
		icon := "❌"
		if f.Enabled {
			icon = "✅"
		}
		line := fmt.Sprintf("  %s %-20s %s", icon, f.Name, f.Description)
		if !f.Enabled && f.Reason != "" {
			line += fmt.Sprintf(" (%s)", f.Reason)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /feature enable <name> or /feature disable <name>")

	gw.sendReply(ctx, ch, msg, sb.String())
}

func (gw *Gateway) handleConfigCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	var sb strings.Builder
	sb.WriteString("⚙️ Current Configuration:\n\n")
	sb.WriteString(fmt.Sprintf("  Provider:    %s\n", gw.cfg.LLM.Provider))
	sb.WriteString(fmt.Sprintf("  Model:       %s\n", gw.cfg.LLM.Model))
	sb.WriteString(fmt.Sprintf("  Max Tokens:  %d\n", gw.cfg.LLM.MaxTokens))
	sb.WriteString(fmt.Sprintf("  Agent Mode:  %s\n", gw.currentMode.Load().(string)))
	sb.WriteString(fmt.Sprintf("  Max Iters:   %d\n", gw.cfg.Agent.MaxIterations))

	if gw.features != nil {
		enabled := gw.features.EnabledNames()
		sb.WriteString(fmt.Sprintf("  Features:    %d enabled\n", len(enabled)))
	}

	gw.sendReply(ctx, ch, msg, sb.String())
}

func (gw *Gateway) handleCompactCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	if gw.contextMgr == nil {
		gw.sendReply(ctx, ch, msg, "⚠️ Context manager not available")
		return
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		gw.sendReply(ctx, ch, msg, "⚠️ No active session")
		return
	}

	history := sess.Messages
	if len(history) == 0 {
		gw.sendReply(ctx, ch, msg, "ℹ️ No messages to compress")
		return
	}

	compressed, stats, err := gw.contextMgr.Compress(ctx, history)
	if err != nil {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("⚠️ Compression failed: %s", err))
		return
	}

	sess.Messages = compressed
	if err := gw.sessions.Save(ctx, sess); err != nil {
		slog.Error("failed to save compressed session", "err", err)
	}

	gw.sendReply(ctx, ch, msg, fmt.Sprintf("🗜️ Context compressed: %d→%d messages (%s)", len(history), len(compressed), stats))
}

func (gw *Gateway) handleModelCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, args string) {
	args = strings.TrimSpace(args)

	if args == "" {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("Current model: %s (provider: %s)", gw.cfg.LLM.Model, gw.cfg.LLM.Provider))
		return
	}

	oldModel := gw.cfg.LLM.Model
	gw.cfg.LLM.Model = args
	gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Model switched to %s (was: %s)", args, oldModel))
}

func (gw *Gateway) sendReply(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, text string) {
	_ = ch.Send(ctx, channel.OutboundMessage{
		Channel:   msg.Channel,
		ChannelID: msg.ChannelID,
		Text:      text,
	})
}
```

- [ ] **Step 2: Verify it compiles**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/gateway/
```

Expected: success (or fail if `features` field not on Gateway yet — that's OK, we'll add it in B3).

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/command_feature.go
git commit -m "feat(gateway): add /feature, /config, /compact, /model command handlers"
```

---

### Task B3: Wire commands into handleInbound

**Files:**
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Add `features` field to Gateway struct**

In `internal/gateway/gateway.go`, add to the `Gateway` struct:

```go
	features        *feature.Registry
```

Add import:
```go
	"github.com/Forest-Isle/IronClaw/internal/feature"
```

- [ ] **Step 2: Add command interception in handleInbound**

In the `handleInbound` method, after the `/mode` handler block and before the `/new` handler block, add:

```go
	// Handle /feature command — list, enable, or disable features
	if msg.Text == "/feature" || strings.HasPrefix(msg.Text, "/feature ") {
		args := strings.TrimPrefix(msg.Text, "/feature")
		gw.handleFeatureCommand(ctx, ch, msg, strings.TrimSpace(args))
		return
	}

	// Handle /config command — show current configuration
	if msg.Text == "/config" || msg.Text == "/config show" {
		gw.handleConfigCommand(ctx, ch, msg)
		return
	}

	// Handle /compact command — manually trigger context compression
	if msg.Text == "/compact" {
		gw.handleCompactCommand(ctx, ch, msg)
		return
	}

	// Handle /model command — show or switch LLM model
	if msg.Text == "/model" || strings.HasPrefix(msg.Text, "/model ") {
		args := strings.TrimPrefix(msg.Text, "/model")
		gw.handleModelCommand(ctx, ch, msg, strings.TrimSpace(args))
		return
	}
```

- [ ] **Step 3: Verify it compiles**

```bash
CGO_ENABLED=1 go build -tags fts5 ./internal/gateway/
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/gateway.go
git commit -m "feat(gateway): wire /feature, /config, /compact, /model into handleInbound"
```

---

### Task B4: Compile and verify full build

**Files:** (none — verification only)

- [ ] **Step 1: Build entire project**

```bash
CGO_ENABLED=1 go build -tags fts5 ./cmd/ironclaw/
```

Expected: success.

- [ ] **Step 2: Run existing tests**

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/gateway/ -v -count=1 2>&1 | tail -20
CGO_ENABLED=1 go test -tags fts5 ./internal/channel/tui/ -v -count=1 2>&1 | tail -20
```

Expected: existing tests still pass.

- [ ] **Step 3: Commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: resolve compilation issues from slash command integration"
```

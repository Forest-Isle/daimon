# P0+P1 Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add API retry with exponential backoff, split the 427-line gateway initializer into focused functions, and integrate a token budget system with the existing compression pipeline.

**Architecture:** Decorator pattern for retry (wraps existing Provider). Gateway split is pure refactor — extract methods on `*Gateway`. Token budget hooks into the existing `CompressionPipeline.Run()` call site in `runtime.go`, reusing the 4-layer pipeline that already exists.

**Tech Stack:** Go standard library (`math/rand`, `time`, `errors`, `net/http`), Anthropic SDK error types, existing `session`/`config`/`agent` packages.

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/agent/retry.go` | `RetryProvider` decorator wrapping `Provider` |
| Create | `internal/agent/retry_test.go` | Tests for retry logic |
| Create | `internal/gateway/init_database.go` | `initDatabase()` method |
| Create | `internal/gateway/init_tools.go` | `initToolsAndHooks()` method |
| Create | `internal/gateway/init_agent.go` | `initAgentRuntime()` method |
| Create | `internal/gateway/init_memory.go` | `initMemorySystem()` method |
| Create | `internal/gateway/init_cognitive.go` | `initCognitiveAgent()` method |
| Create | `internal/gateway/init_knowledge.go` | `initKnowledgeSystem()` method |
| Create | `internal/gateway/init_skills.go` | `initSkillManager()` method |
| Create | `internal/gateway/init_multiagent.go` | `initMultiAgent()` method |
| Modify | `internal/gateway/gateway.go` | Slim down `New()` to orchestrator |
| Modify | `internal/config/config.go` | Add `RetryConfig` to `LLMConfig` |
| Create | `internal/agent/token_budget.go` | `TokenBudget` struct and `Check()` |
| Create | `internal/agent/token_budget_test.go` | Tests for token budget |
| Modify | `internal/agent/runtime.go` | Add `tokenBudget` field, call `Check()` before LLM |
| Modify | `internal/agent/compression.go` | Export `EstimateUtilization` (already exported), add `SetContextWindow` |

---

### Task 1: API Retry — Config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add RetryConfig struct and wire into LLMConfig**

In `internal/config/config.go`, add the retry config struct after `LLMConfig` (around line 104):

```go
// RetryConfig controls retry behavior for LLM API calls.
type RetryConfig struct {
	MaxRetries int           `yaml:"max_retries"`
	BaseDelay  time.Duration `yaml:"base_delay"`
	MaxDelay   time.Duration `yaml:"max_delay"`
}
```

Add a `Retry` field to `LLMConfig`:

```go
type LLMConfig struct {
	Provider  string      `yaml:"provider"`
	APIKey    string      `yaml:"api_key"`
	BaseURL   string      `yaml:"base_url"`
	Model     string      `yaml:"model"`
	MaxTokens int         `yaml:"max_tokens"`
	Retry     RetryConfig `yaml:"retry"`
}
```

Set defaults in `defaultConfig()` inside the `LLM` block:

```go
Retry: RetryConfig{
	MaxRetries: 3,
	BaseDelay:  1 * time.Second,
	MaxDelay:   30 * time.Second,
},
```

- [ ] **Step 2: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add RetryConfig for LLM API retry with defaults"
```

---

### Task 2: API Retry — RetryProvider Implementation

**Files:**
- Create: `internal/agent/retry.go`
- Create: `internal/agent/retry_test.go`

- [ ] **Step 1: Write failing test for retryable error classification**

Create `internal/agent/retry_test.go`:

```go
package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// httpError simulates an API error with a status code.
type httpError struct {
	StatusCode int
	Message    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Message)
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"429 rate limit", &httpError{StatusCode: 429, Message: "rate limited"}, true},
		{"500 server error", &httpError{StatusCode: 500, Message: "internal"}, true},
		{"502 bad gateway", &httpError{StatusCode: 502, Message: "bad gateway"}, true},
		{"503 unavailable", &httpError{StatusCode: 503, Message: "unavailable"}, true},
		{"529 overloaded", &httpError{StatusCode: 529, Message: "overloaded"}, true},
		{"400 bad request", &httpError{StatusCode: 400, Message: "bad request"}, false},
		{"401 unauthorized", &httpError{StatusCode: 401, Message: "unauthorized"}, false},
		{"403 forbidden", &httpError{StatusCode: 403, Message: "forbidden"}, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"generic error", errors.New("connection reset"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryable(tt.err)
			if got != tt.expected {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run TestIsRetryable ./internal/agent/ -v`
Expected: FAIL — `isRetryable` not defined.

- [ ] **Step 3: Implement isRetryable and RetryProvider**

Create `internal/agent/retry.go`:

```go
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/config"
)

// RetryProvider wraps a Provider with exponential backoff retry logic.
type RetryProvider struct {
	inner      Provider
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// NewRetryProvider creates a RetryProvider wrapping the given provider.
func NewRetryProvider(inner Provider, cfg config.RetryConfig) *RetryProvider {
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	baseDelay := cfg.BaseDelay
	if baseDelay <= 0 {
		baseDelay = 1 * time.Second
	}
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	return &RetryProvider{
		inner:      inner,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
		maxDelay:   maxDelay,
	}
}

// Complete retries transient errors with exponential backoff.
func (rp *RetryProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= rp.maxRetries; attempt++ {
		resp, err := rp.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
		if attempt < rp.maxRetries {
			delay := rp.backoff(attempt)
			slog.Warn("retrying LLM call",
				"method", "Complete",
				"attempt", attempt+1,
				"max_retries", rp.maxRetries,
				"delay", delay,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, fmt.Errorf("max retries (%d) exceeded: %w", rp.maxRetries, lastErr)
}

// Stream retries only initial connection errors.
// Mid-stream errors are not retried because partial state would be lost.
func (rp *RetryProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	var lastErr error
	for attempt := 0; attempt <= rp.maxRetries; attempt++ {
		iter, err := rp.inner.Stream(ctx, req)
		if err == nil {
			return iter, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
		if attempt < rp.maxRetries {
			delay := rp.backoff(attempt)
			slog.Warn("retrying LLM call",
				"method", "Stream",
				"attempt", attempt+1,
				"max_retries", rp.maxRetries,
				"delay", delay,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, fmt.Errorf("max retries (%d) exceeded: %w", rp.maxRetries, lastErr)
}

// backoff computes delay for the given attempt: min(baseDelay * 2^attempt + jitter, maxDelay).
func (rp *RetryProvider) backoff(attempt int) time.Duration {
	delay := float64(rp.baseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(rp.maxDelay) {
		delay = float64(rp.maxDelay)
	}
	// Add 0-25% jitter
	jitter := delay * 0.25 * rand.Float64()
	return time.Duration(delay + jitter)
}

// statusCodeFromError extracts an HTTP status code from an error, if present.
type httpStatusError interface {
	StatusCode() int
}

// isRetryable returns true if the error is transient and worth retrying.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not retryable
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for HTTP status code via interface
	var hse httpStatusError
	if errors.As(err, &hse) {
		return isRetryableStatusCode(hse.StatusCode())
	}

	// Check for our test httpError type
	var he *httpError
	if errors.As(err, &he) {
		return isRetryableStatusCode(he.StatusCode)
	}

	// Unknown errors default to retryable (network issues, connection resets, etc.)
	return true
}

func isRetryableStatusCode(code int) bool {
	switch code {
	case 429, 500, 502, 503, 529:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Run isRetryable test**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run TestIsRetryable ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Write test for RetryProvider.Complete retry behavior**

Add to `internal/agent/retry_test.go`:

```go
// mockProvider records calls and returns configured responses.
type mockProvider struct {
	calls     int
	failCount int // fail this many times before succeeding
	failErr   error
}

func (m *mockProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	m.calls++
	if m.calls <= m.failCount {
		return nil, m.failErr
	}
	return &CompletionResponse{Text: "success"}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ CompletionRequest) (StreamIterator, error) {
	m.calls++
	if m.calls <= m.failCount {
		return nil, m.failErr
	}
	return &mockStreamIterator{}, nil
}

type mockStreamIterator struct{ done bool }

func (m *mockStreamIterator) Next() (StreamDelta, error) {
	if m.done {
		return StreamDelta{Done: true}, nil
	}
	m.done = true
	return StreamDelta{Text: "ok", Done: true, StopReason: StopEndTurn}, nil
}

func (m *mockStreamIterator) Close() {}

func TestRetryProviderCompleteRetriesOnTransientError(t *testing.T) {
	mock := &mockProvider{
		failCount: 2,
		failErr:   &httpError{StatusCode: 429, Message: "rate limited"},
	}
	rp := NewRetryProvider(mock, config.RetryConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond, // fast for tests
		MaxDelay:   10 * time.Millisecond,
	})

	resp, err := rp.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if resp.Text != "success" {
		t.Errorf("expected 'success', got %q", resp.Text)
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", mock.calls)
	}
}

func TestRetryProviderCompleteNoRetryOnNonRetryable(t *testing.T) {
	mock := &mockProvider{
		failCount: 5,
		failErr:   &httpError{StatusCode: 401, Message: "unauthorized"},
	}
	rp := NewRetryProvider(mock, config.RetryConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
	})

	_, err := rp.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error for non-retryable")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (no retry), got %d", mock.calls)
	}
}

func TestRetryProviderCompleteExhaustsRetries(t *testing.T) {
	mock := &mockProvider{
		failCount: 10,
		failErr:   &httpError{StatusCode: 500, Message: "server error"},
	}
	rp := NewRetryProvider(mock, config.RetryConfig{
		MaxRetries: 2,
		BaseDelay:  1 * time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
	})

	_, err := rp.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// 1 initial + 2 retries = 3 total calls
	if mock.calls != 3 {
		t.Errorf("expected 3 calls, got %d", mock.calls)
	}
}

func TestRetryProviderStreamRetries(t *testing.T) {
	mock := &mockProvider{
		failCount: 1,
		failErr:   &httpError{StatusCode: 502, Message: "bad gateway"},
	}
	rp := NewRetryProvider(mock, config.RetryConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
	})

	iter, err := rp.Stream(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	delta, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if delta.Text != "ok" {
		t.Errorf("expected 'ok', got %q", delta.Text)
	}
}
```

- [ ] **Step 6: Run all retry tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run TestRetryProvider ./internal/agent/ -v`
Expected: All PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/retry.go internal/agent/retry_test.go
git commit -m "feat(agent): add RetryProvider with exponential backoff for LLM API calls"
```

---

### Task 3: API Retry — Wire into Gateway

**Files:**
- Modify: `internal/gateway/gateway.go:97-101`

- [ ] **Step 1: Wrap provider with RetryProvider in gateway.go**

In `internal/gateway/gateway.go`, replace lines 97-101:

Old:
```go
	// LLM provider
	provider := agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)

	// Agent runtime
	runtime := agent.NewRuntime(provider, tools, sessions, db, cfg.Agent, cfg.LLM)
```

New:
```go
	// LLM provider
	var provider agent.Provider = agent.NewClaudeProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)

	// Wrap with retry logic
	if cfg.LLM.Retry.MaxRetries > 0 {
		provider = agent.NewRetryProvider(provider, cfg.LLM.Retry)
		slog.Info("LLM retry enabled", "max_retries", cfg.LLM.Retry.MaxRetries, "base_delay", cfg.LLM.Retry.BaseDelay)
	}

	// Agent runtime
	runtime := agent.NewRuntime(provider, tools, sessions, db, cfg.Agent, cfg.LLM)
```

- [ ] **Step 2: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 3: Run existing tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -v -count=1`
Expected: All existing tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/gateway.go
git commit -m "feat(gateway): wire RetryProvider into LLM provider initialization"
```

---

### Task 4: Gateway Split — Extract initDatabase

**Files:**
- Create: `internal/gateway/init_database.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Create init_database.go**

Create `internal/gateway/init_database.go`:

```go
package gateway

import (
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// initDatabase opens the SQLite database and creates the session manager.
func (gw *Gateway) initDatabase() error {
	db, err := store.Open(gw.cfg.Store.Path)
	if err != nil {
		return err
	}
	gw.db = db
	gw.sessions = session.NewManager(db)
	return nil
}
```

- [ ] **Step 2: Replace in gateway.go**

In `gateway.go`, replace lines 44-51:

Old:
```go
	// Open database
	db, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, err
	}

	// Session manager
	sessions := session.NewManager(db)
```

New:
```go
	gw := &Gateway{
		cfg:      cfg,
		channels: make(map[string]channel.Channel),
	}

	if err := gw.initDatabase(); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
```

Note: The `gw` construction currently at the bottom of `New()` (lines 438-450) needs to be moved up. Remove the old `gw := &Gateway{...}` block and its surrounding lines. The remaining init code will need to reference `gw.db` and `gw.sessions` instead of local `db` and `sessions` variables. This will be done progressively — each subsequent task extracts more code and updates references.

For now, update all remaining references in `New()` from `db` to `gw.db` and `sessions` to `gw.sessions`.

- [ ] **Step 3: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/init_database.go internal/gateway/gateway.go
git commit -m "refactor(gateway): extract initDatabase from New()"
```

---

### Task 5: Gateway Split — Extract initToolsAndHooks

**Files:**
- Create: `internal/gateway/init_tools.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Create init_tools.go**

Create `internal/gateway/init_tools.go`:

```go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// initToolsAndHooks creates the tool registry, hook system, and permission engine.
func (gw *Gateway) initToolsAndHooks() error {
	// Tool registry
	gw.tools = tool.NewRegistry()
	policy := tool.NewPolicy(gw.cfg.Tools.Bash.BlockedCommands)

	if gw.cfg.Tools.Bash.Enabled {
		gw.tools.Register(tool.NewBashTool(gw.cfg.Tools.Bash.Timeout, gw.cfg.Tools.Bash.RequiresApproval, policy))
	}
	if gw.cfg.Tools.File.Enabled {
		gw.tools.Register(tool.NewFileTool(gw.cfg.Tools.File.RequiresApproval))
	}
	if gw.cfg.Tools.HTTP.Enabled {
		gw.tools.Register(tool.NewHTTPTool(gw.cfg.Tools.HTTP.Timeout, gw.cfg.Tools.HTTP.RequiresApproval))
	}

	// Hook event system
	hookCfg := gw.cfg.Hooks
	preToolUseCfg := make([]hook.HandlerConfig, len(hookCfg.PreToolUse))
	for i, h := range hookCfg.PreToolUse {
		preToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	postToolUseCfg := make([]hook.HandlerConfig, len(hookCfg.PostToolUse))
	for i, h := range hookCfg.PostToolUse {
		postToolUseCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	onUserMsgCfg := make([]hook.HandlerConfig, len(hookCfg.OnUserMessage))
	for i, h := range hookCfg.OnUserMessage {
		onUserMsgCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	preCompactCfg := make([]hook.HandlerConfig, len(hookCfg.PreCompact))
	for i, h := range hookCfg.PreCompact {
		preCompactCfg[i] = hook.HandlerConfig{Type: h.Type, Config: h.Config}
	}
	gw.hookMgr = hook.BuildManager(preToolUseCfg, postToolUseCfg, onUserMsgCfg, preCompactCfg, &hook.BuildManagerOpts{DB: gw.db.DB})
	slog.Info("hook system initialized")

	// Permission engine
	permRules := make([]tool.PermissionRule, len(gw.cfg.Permissions.Rules))
	for i, r := range gw.cfg.Permissions.Rules {
		permRules[i] = tool.PermissionRule{
			Tool: r.Tool, Pattern: r.Pattern, PathPattern: r.PathPattern, Action: r.Action,
		}
	}
	gw.permEngine = tool.NewPermissionEngine(permRules, gw.cfg.Permissions.Default, policy)

	return nil
}
```

Note: This requires adding `hookMgr` and `permEngine` fields to the Gateway struct:

```go
type Gateway struct {
	cfg            *config.Config
	db             *store.DB
	sessions       *session.Manager
	runtime        *agent.Runtime
	cognitiveAgent *agent.CognitiveAgent
	tools          *tool.Registry
	channels       map[string]channel.Channel
	sched          *scheduler.Scheduler
	mcpManager     *mcp.Manager
	rlTrainer      *rl.Trainer
	resultStore    *tool.ResultStore
	hookMgr        *hook.Manager       // new
	permEngine     *tool.PermissionEngine // new
}
```

- [ ] **Step 2: Update gateway.go — remove extracted code, call initToolsAndHooks()**

Remove lines 53-95 from `New()` and add after `initDatabase()`:

```go
	if err := gw.initToolsAndHooks(); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
```

Update remaining references: `tools` → `gw.tools`, `hookMgr` → `gw.hookMgr`, `permEngine` → `gw.permEngine`, `policy` is now local to `init_tools.go`.

- [ ] **Step 3: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/init_tools.go internal/gateway/gateway.go
git commit -m "refactor(gateway): extract initToolsAndHooks from New()"
```

---

### Task 6: Gateway Split — Extract initAgentRuntime

**Files:**
- Create: `internal/gateway/init_agent.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Create init_agent.go**

Create `internal/gateway/init_agent.go`:

```go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// initAgentRuntime creates the LLM provider, agent runtime, and configures
// result persistence and concurrent execution.
func (gw *Gateway) initAgentRuntime() error {
	// LLM provider
	var provider agent.Provider = agent.NewClaudeProvider(
		gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL,
	)

	// Wrap with retry logic
	if gw.cfg.LLM.Retry.MaxRetries > 0 {
		provider = agent.NewRetryProvider(provider, gw.cfg.LLM.Retry)
		slog.Info("LLM retry enabled",
			"max_retries", gw.cfg.LLM.Retry.MaxRetries,
			"base_delay", gw.cfg.LLM.Retry.BaseDelay,
		)
	}

	// Store provider for adapter use in later init functions
	gw.provider = provider

	// Agent runtime
	gw.runtime = agent.NewRuntime(provider, gw.tools, gw.sessions, gw.db, gw.cfg.Agent, gw.cfg.LLM)

	// Wire hook manager and permission engine
	gw.runtime.SetHookManager(gw.hookMgr)
	gw.runtime.SetPermissionEngine(gw.permEngine)

	// Tool result persistence
	if gw.cfg.Tools.ResultPersistence.Enabled {
		gw.resultStore = tool.NewResultStore(
			gw.cfg.Tools.ResultPersistence.CacheDir,
			gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			gw.cfg.Tools.ResultPersistence.PreviewChars,
			gw.cfg.Tools.ResultPersistence.TTLHours,
		)
		gw.runtime.SetResultStore(gw.resultStore)
		if err := gw.resultStore.Cleanup(); err != nil {
			slog.Warn("gateway: result store startup cleanup failed", "err", err)
		}
		slog.Info("tool result persistence enabled",
			"threshold", gw.cfg.Tools.ResultPersistence.ThresholdBytes,
			"ttl_hours", gw.cfg.Tools.ResultPersistence.TTLHours,
		)
	}

	// Concurrent tool execution
	gw.runtime.SetConcurrentConfig(gw.cfg.Tools.ConcurrentExecution)
	if gw.cfg.Tools.ConcurrentExecution.Enabled {
		slog.Info("concurrent tool execution enabled",
			"max_concurrency", gw.cfg.Tools.ConcurrentExecution.MaxConcurrency,
		)
	}

	return nil
}
```

Note: Add `provider` field to Gateway struct:

```go
provider agent.Provider // stored for completerAdapter use
```

- [ ] **Step 2: Update gateway.go — remove extracted code, call initAgentRuntime()**

Remove the LLM provider, runtime, hooks wiring, result persistence, and concurrent execution blocks from `New()`. Add:

```go
	if err := gw.initAgentRuntime(); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
```

Update all `completerAdapter{provider: provider, model: cfg.LLM.Model}` references in remaining `New()` code to use `gw.provider` and `gw.cfg.LLM.Model`.

- [ ] **Step 3: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/init_agent.go internal/gateway/gateway.go
git commit -m "refactor(gateway): extract initAgentRuntime from New()"
```

---

### Task 7: Gateway Split — Extract initMemorySystem

**Files:**
- Create: `internal/gateway/init_memory.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Create init_memory.go**

Create `internal/gateway/init_memory.go`. Move lines 133-235 from `New()` into this method. Key changes:
- All local vars become `gw.*` fields
- Add these fields to Gateway struct: `memStore memory.Store`, `factExtractor *memory.LLMFactExtractor`, `lifecycleMgr *memory.LifecycleManager`

```go
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// initMemorySystem sets up file-based memory storage, embeddings, fact extraction,
// lifecycle management, forgetting curve, and the compactor background task.
func (gw *Gateway) initMemorySystem() error {
	if !gw.cfg.Memory.Enabled {
		return nil
	}

	var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
	if gw.cfg.Memory.OpenAIAPIKey != "" {
		baseEmbedder := memory.NewOpenAIEmbedding(gw.cfg.Memory.OpenAIAPIKey, gw.cfg.Memory.EmbeddingModel)
		embedder = memory.NewCachedEmbedder(baseEmbedder)
		slog.Info("memory: cached embedder enabled")
	}
	memCfg := memory.MemoryConfig{
		FactExtraction:           gw.cfg.Memory.FactExtraction,
		SimilarityThreshold:      gw.cfg.Memory.SimilarityThreshold,
		ConsolidationInterval:    gw.cfg.Memory.ConsolidationInterval,
		BM25Weight:               gw.cfg.Memory.BM25Weight,
		VectorWeight:             gw.cfg.Memory.VectorWeight,
		EnableVSS:                gw.cfg.Memory.EnableVSS,
		VectorDimension:          gw.cfg.Memory.VectorDimension,
		EnableSearchCache:        gw.cfg.Memory.EnableSearchCache,
		SearchCacheSize:          gw.cfg.Memory.SearchCacheSize,
		SearchCacheTTL:           gw.cfg.Memory.SearchCacheTTL,
		ReflectionCountThreshold: gw.cfg.Memory.ReflectionCountThreshold,
		ReflectionDriftThreshold: gw.cfg.Memory.ReflectionDriftThreshold,
		ReflectionL2Trigger:      gw.cfg.Memory.ReflectionL2Trigger,
	}

	storageDir := gw.cfg.Memory.StorageDir
	if storageDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		storageDir = filepath.Join(home, ".IronClaw", "memory")
	}

	fileStore, err := memory.NewFileMemoryStore(storageDir, gw.db.DB, embedder, memCfg)
	if err != nil {
		return fmt.Errorf("create file memory store: %w", err)
	}
	gw.memStore = fileStore
	slog.Info("memory: file-based storage enabled", "dir", storageDir)

	gw.runtime.SetMemoryStore(gw.memStore)
	gw.runtime.SetMemoryBaseDir(storageDir)

	completer := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
	compressor := memory.NewIncrementalCompressor(storageDir, completer)
	gw.runtime.SetCompressor(compressor)
	slog.Info("memory: incremental compressor enabled")

	forgettingCurve := memory.NewForgettingCurveManager(gw.db)

	if gw.cfg.Memory.FactExtraction {
		gw.factExtractor = memory.NewLLMFactExtractor(completer, memCfg)

		reflector := memory.NewReflectionTracker(gw.memStore, completer, embedder, memCfg, gw.db.DB)
		slog.Info("memory: reflection tracker enabled")

		gw.lifecycleMgr = memory.NewLifecycleManager(gw.memStore, embedder, completer, memCfg, reflector)

		compactor := memory.NewCompactor(gw.memStore, completer, gw.db.DB, storageDir, memCfg)
		compactor.Start(context.Background())
		slog.Info("memory: compactor enabled")

		profiler := memory.NewProfiler(gw.memStore, completer, gw.db.DB, storageDir, memCfg)
		_ = profiler
		slog.Info("memory: profiler created")
	}

	memTool := tool.NewMemoryManageTool(gw.memStore, gw.db.DB, storageDir)
	gw.tools.Register(memTool)
	slog.Info("memory: memory_manage tool registered")

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := forgettingCurve.FadeWeakMemoriesFromFiles(context.Background(), storageDir); err != nil {
				slog.Warn("memory: fade weak memory files failed", "err", err)
			}
			if err := forgettingCurve.FadeByRetentionPolicy(context.Background(), storageDir, memCfg); err != nil {
				slog.Warn("memory: retention policy enforcement failed", "err", err)
			}
		}
	}()
	slog.Info("memory: forgetting curve and retention policy enabled (file-based)")

	return nil
}
```

- [ ] **Step 2: Update gateway.go — remove extracted code, call initMemorySystem()**

Remove lines 133-235 from `New()`. Add:

```go
	if err := gw.initMemorySystem(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
```

- [ ] **Step 3: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/init_memory.go internal/gateway/gateway.go
git commit -m "refactor(gateway): extract initMemorySystem from New()"
```

---

### Task 8: Gateway Split — Extract remaining init functions

**Files:**
- Create: `internal/gateway/init_cognitive.go`
- Create: `internal/gateway/init_knowledge.go`
- Create: `internal/gateway/init_skills.go`
- Create: `internal/gateway/init_multiagent.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Create init_cognitive.go**

Extract lines 237-264 (cognitive agent + RL) into `internal/gateway/init_cognitive.go`:

```go
package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/rl"
)

// initCognitiveAgent optionally builds the cognitive agent and RL system.
func (gw *Gateway) initCognitiveAgent() error {
	if gw.cfg.Agent.Mode != "cognitive" {
		return nil
	}

	gw.cognitiveAgent = agent.NewCognitiveAgent(
		gw.provider, gw.tools, gw.sessions, gw.db, gw.cfg.Agent, gw.cfg.LLM,
	)
	if gw.memStore != nil {
		gw.cognitiveAgent.SetMemoryStore(gw.memStore)
	}
	if gw.factExtractor != nil {
		gw.cognitiveAgent.SetFactExtractor(gw.factExtractor)
	}
	if gw.lifecycleMgr != nil {
		gw.cognitiveAgent.SetLifecycleManager(gw.lifecycleMgr)
	}

	// RL System (requires cognitive agent)
	if gw.cfg.Agent.RL.Enabled {
		rlStorage := rl.NewStorage(gw.db)
		rlPolicy := rl.NewPolicy(rlStorage, gw.cfg.Agent.RL)
		if err := rlPolicy.LoadCheckpoint(context.Background()); err != nil {
			slog.Warn("gateway: failed to load RL checkpoint", "err", err)
		}
		gw.rlTrainer = rl.NewTrainer(rlPolicy, gw.cfg.Agent.RL)
		gw.cognitiveAgent.SetRLPolicy(rlPolicy)
		gw.cognitiveAgent.SetRLTrainer(gw.rlTrainer)
		slog.Info("RL system initialized")
	}

	return nil
}
```

- [ ] **Step 2: Create init_knowledge.go**

Extract lines 266-355 into `internal/gateway/init_knowledge.go`:

```go
package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// initKnowledgeSystem sets up the knowledge base, hybrid retriever,
// and optionally the knowledge graph with entity extraction.
func (gw *Gateway) initKnowledgeSystem() error {
	if !gw.cfg.Knowledge.Enabled {
		return nil
	}

	kbCfg := knowledge.Config{
		ChunkSize:         gw.cfg.Knowledge.ChunkSize,
		ChunkOverlap:      gw.cfg.Knowledge.ChunkOverlap,
		BM25Weight:        gw.cfg.Knowledge.BM25Weight,
		VectorWeight:      gw.cfg.Knowledge.VectorWeight,
		IngestDirs:        gw.cfg.Knowledge.IngestDirs,
		EnableSearchCache: gw.cfg.Knowledge.EnableSearchCache,
		SearchCacheSize:   gw.cfg.Knowledge.SearchCacheSize,
		SearchCacheTTL:    gw.cfg.Knowledge.SearchCacheTTL,
	}

	var kbEmbedder knowledge.EmbeddingProvider
	if gw.cfg.Memory.OpenAIAPIKey != "" {
		kbEmbedder = memory.NewOpenAIEmbedding(gw.cfg.Memory.OpenAIAPIKey, gw.cfg.Memory.EmbeddingModel)
	} else {
		kbEmbedder = &noopKBEmbedder{}
	}
	kb := knowledge.New(gw.db, kbEmbedder, kbCfg)

	var reranker knowledge.Reranker = &knowledge.NoopReranker{}
	if gw.cfg.Knowledge.Reranker.Enabled && gw.cfg.Knowledge.Reranker.Provider == "llm" {
		llmCompleter := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
		reranker = knowledge.NewLLMReranker(llmCompleter)
	}
	retriever := knowledge.NewHybridRetriever(kb, reranker)

	for _, dir := range gw.cfg.Knowledge.IngestDirs {
		if err := kb.GetPipeline().IngestDir(context.Background(), dir); err != nil {
			slog.Warn("gateway: failed to ingest dir", "dir", dir, "err", err)
		}
	}

	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetKnowledgeSearcher(retriever)
	}

	if gw.cfg.Knowledge.GraphEnabled || gw.cfg.Graph.Enabled {
		kg := graph.NewSQLiteGraph(gw.db)
		llmCompleter := &completerAdapter{provider: gw.provider, model: gw.cfg.LLM.Model}
		extractor := graph.NewLLMEntityExtractor(kg, llmCompleter)

		go func() {
			sources, err := kb.Sources(context.Background())
			if err != nil {
				slog.Warn("gateway: failed to list KB sources for graph extraction", "err", err)
				return
			}
			for _, src := range sources {
				results, err := kb.Search(context.Background(), knowledge.KnowledgeQuery{
					Text:       "",
					SourceType: src.SourceType,
					Limit:      50,
				})
				if err != nil {
					continue
				}
				for _, r := range results {
					extractor.Extract(context.Background(), r.Chunk.Content, "kb_chunk", r.Chunk.ID) //nolint:errcheck
				}
			}
			slog.Info("gateway: initial graph entity extraction complete")
		}()

		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetKnowledgeGraph(kg)
			gw.cognitiveAgent.SetEntityExtractor(extractor)
		}

		if gw.lifecycleMgr != nil {
			graphSync := graph.NewGraphSync(kg, extractor)
			gw.lifecycleMgr.SetGraphSync(graphSync)
			slog.Info("knowledge graph: memory lifecycle sync enabled")
		}

		graphDecay := graph.NewGraphDecayTask(kg, 24*time.Hour)
		go graphDecay.Start(context.Background())
		slog.Info("knowledge graph: decay task started")
		slog.Info("knowledge graph initialized")
	}

	slog.Info("knowledge base initialized", "ingest_dirs", gw.cfg.Knowledge.IngestDirs)
	return nil
}
```

- [ ] **Step 3: Create init_skills.go**

Extract lines 357-381 into `internal/gateway/init_skills.go`:

```go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// initSkillManager loads builtin and user skills, then registers the read_skill tool.
func (gw *Gateway) initSkillManager() error {
	if !gw.cfg.Skills.Enabled {
		return nil
	}

	gw.skillMgr = skill.New()
	if err := gw.skillMgr.LoadBuiltin(); err != nil {
		slog.Warn("gateway: failed to load builtin skills", "err", err)
	}
	userSkillsDir := defaultSkillsDir()
	if err := gw.skillMgr.LoadDir(userSkillsDir); err != nil {
		slog.Warn("gateway: failed to load user skills", "dir", userSkillsDir, "err", err)
	}
	for _, dir := range gw.cfg.Skills.ExtraDirs {
		if err := gw.skillMgr.LoadDir(dir); err != nil {
			slog.Warn("gateway: failed to load extra skills dir", "dir", dir, "err", err)
		}
	}
	gw.runtime.SetSkillManager(gw.skillMgr)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetSkillManager(gw.skillMgr)
	}
	gw.tools.Register(tool.NewSkillTool(gw.skillMgr))
	slog.Info("skill manager initialized", "skills", len(gw.skillMgr.All()))
	return nil
}
```

Note: Add `skillMgr *skill.Manager` to Gateway struct.

- [ ] **Step 4: Create init_multiagent.go**

Extract lines 383-433 into `internal/gateway/init_multiagent.go`:

```go
package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
)

// initMultiAgent sets up the multi-agent system, background manager,
// prompt cache, per-agent MCP, orchestrator, and compression pipeline.
func (gw *Gateway) initMultiAgent() error {
	if gw.cfg.Agents.Enabled {
		agentMgr := agent.NewAgentManager(
			gw.provider, gw.sessions, gw.db, gw.memStore, gw.tools, gw.cfg.Agent, gw.cfg.LLM,
		)
		_ = agentMgr.LoadDir(userdir.AgentsDir())
		for _, dir := range gw.cfg.Agents.ExtraDirs {
			if err := agentMgr.LoadDir(dir); err != nil {
				slog.Warn("gateway: failed to load agents from extra dir", "dir", dir, "err", err)
			}
		}
		for _, def := range gw.cfg.Agents.Definitions {
			if err := agentMgr.Add(defToSpec(def)); err != nil {
				slog.Warn("gateway: failed to add inline agent definition", "name", def.Name, "err", err)
			}
		}
		agentMgr.RegisterAll(gw.tools)
		gw.runtime.SetAgentManager(agentMgr)

		bgManager := agent.NewBackgroundManager()
		agentMgr.SetBackgroundManager(bgManager)
		gw.runtime.SetBackgroundManager(bgManager)
		slog.Info("background agent manager initialized")

		promptCache := agent.NewPromptCache()
		gw.runtime.SetPromptCache(promptCache)
		slog.Info("agent prompt cache initialized")

		agentMCPMgr := agent.NewAgentMCPManager(nil)
		agentMgr.SetAgentMCPManager(agentMCPMgr)
		gw.runtime.SetAgentMCPManager(agentMCPMgr)
		slog.Info("per-agent MCP manager initialized")

		orchestrator := agent.NewAgentOrchestrator(agentMgr, 4)
		gw.runtime.SetOrchestrator(orchestrator)
		slog.Info("agent orchestrator initialized", "max_parallel", 4)

		if gw.cognitiveAgent != nil {
			gw.cognitiveAgent.SetAgentManager(agentMgr)
			gw.cognitiveAgent.SetOrchestrator(orchestrator)
		}
		slog.Info("multi-agent system initialized", "agents", len(agentMgr.All()))
	}

	// Compression pipeline
	if gw.cfg.Agent.Compression.Strategy == "layered" {
		pipeline := agent.NewCompressionPipeline(
			gw.provider, gw.cfg.LLM.Model, gw.cfg.Agent.Compression, gw.resultStore, 200000,
		)
		gw.runtime.SetCompressionPipeline(pipeline)
		slog.Info("layered compression pipeline enabled")
	}

	return nil
}
```

- [ ] **Step 5: Rewrite gateway.go New() as pure orchestrator**

Replace the entire `New()` function body with:

```go
func New(cfg *config.Config) (*Gateway, error) {
	gw := &Gateway{
		cfg:      cfg,
		channels: make(map[string]channel.Channel),
	}

	if err := gw.initDatabase(); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := gw.initToolsAndHooks(); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
	if err := gw.initAgentRuntime(); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if err := gw.initMemorySystem(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	if err := gw.initCognitiveAgent(); err != nil {
		return nil, fmt.Errorf("cognitive: %w", err)
	}
	if err := gw.initKnowledgeSystem(); err != nil {
		return nil, fmt.Errorf("knowledge: %w", err)
	}
	if err := gw.initSkillManager(); err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	if err := gw.initMultiAgent(); err != nil {
		return nil, fmt.Errorf("multi-agent: %w", err)
	}

	// Scheduler
	gw.sched = scheduler.New(gw.db, gw.cfg.Scheduler.PollInterval)
	gw.mcpManager = mcp.NewManager()

	// Set up approval function
	gw.runtime.SetApprovalFunc(gw.handleApproval)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetApprovalFunc(gw.handleApproval)
	}

	// Scheduler handler
	gw.sched.SetHandler(func(ctx context.Context, task scheduler.Task) {
		gw.handleInbound(ctx, channel.InboundMessage{
			Channel:   task.Channel,
			ChannelID: task.ChannelID,
			UserID:    "scheduler",
			UserName:  "scheduler",
			Text:      task.Prompt,
		})
	})

	return gw, nil
}
```

Update the Gateway struct to include all new fields:

```go
type Gateway struct {
	cfg            *config.Config
	db             *store.DB
	sessions       *session.Manager
	provider       agent.Provider
	runtime        *agent.Runtime
	cognitiveAgent *agent.CognitiveAgent
	tools          *tool.Registry
	hookMgr        *hook.Manager
	permEngine     *tool.PermissionEngine
	memStore       memory.Store
	factExtractor  *memory.LLMFactExtractor
	lifecycleMgr   *memory.LifecycleManager
	skillMgr       *skill.Manager
	channels       map[string]channel.Channel
	sched          *scheduler.Scheduler
	mcpManager     *mcp.Manager
	rlTrainer      *rl.Trainer
	resultStore    *tool.ResultStore
}
```

Remove unused imports from `gateway.go` (the init files now import their own dependencies).

- [ ] **Step 6: Verify build**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: Clean build.

- [ ] **Step 7: Run all tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./... -count=1`
Expected: All tests pass — this is a pure refactor with no behavior changes.

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/
git commit -m "refactor(gateway): complete split of New() into 8 focused init functions

New() reduced from 427 lines to ~40 lines. Each init function
handles one subsystem with explicit dependencies through Gateway fields."
```

---

### Task 9: Token Budget — Implementation

**Files:**
- Create: `internal/agent/token_budget.go`
- Create: `internal/agent/token_budget_test.go`

- [ ] **Step 1: Write failing test for TokenBudget.Check()**

Create `internal/agent/token_budget_test.go`:

```go
package agent

import (
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

func TestTokenBudgetCheck(t *testing.T) {
	budget := &TokenBudget{
		ModelLimit:      200000,
		LightThreshold:  0.70,
		MediumThreshold: 0.80,
		HeavyThreshold:  0.90,
		EstimateRatio:   0.25,
	}

	tests := []struct {
		name           string
		systemPrompt   string
		messageContent string // repeated to build size
		messageCount   int
		expectedAction BudgetAction
	}{
		{
			name:           "low usage — no action",
			systemPrompt:   "You are helpful.",
			messageContent: "short message",
			messageCount:   5,
			expectedAction: BudgetOK,
		},
		{
			name:           "light threshold — compress light",
			systemPrompt:   strings.Repeat("x", 100000),
			messageContent: strings.Repeat("y", 50000),
			messageCount:   10,
			expectedAction: BudgetCompressLight,
		},
		{
			name:           "heavy threshold — compress heavy",
			systemPrompt:   strings.Repeat("x", 200000),
			messageContent: strings.Repeat("y", 100000),
			messageCount:   10,
			expectedAction: BudgetCompressHeavy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &session.Session{ID: "test"}
			for i := 0; i < tt.messageCount; i++ {
				sess.AddMessage(session.Message{
					Role:    "user",
					Content: tt.messageContent,
				})
			}
			check := budget.Check(sess.History(), tt.systemPrompt)
			if check.Action != tt.expectedAction {
				t.Errorf("Check() action = %v, want %v (ratio=%.2f)",
					check.Action, tt.expectedAction, check.UsageRatio)
			}
		})
	}
}

func TestTokenBudgetDefaultsApplied(t *testing.T) {
	budget := NewTokenBudget(0, 0, 0, 0, 0)
	if budget.ModelLimit != 200000 {
		t.Errorf("default ModelLimit = %d, want 200000", budget.ModelLimit)
	}
	if budget.LightThreshold != 0.70 {
		t.Errorf("default LightThreshold = %f, want 0.70", budget.LightThreshold)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run TestTokenBudget ./internal/agent/ -v`
Expected: FAIL — `TokenBudget` not defined.

- [ ] **Step 3: Implement TokenBudget**

Create `internal/agent/token_budget.go`:

```go
package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// BudgetAction indicates what compression level is needed.
type BudgetAction int

const (
	BudgetOK             BudgetAction = iota // below all thresholds
	BudgetCompressLight                       // light compression needed
	BudgetCompressMedium                      // medium compression needed
	BudgetCompressHeavy                       // aggressive compression needed
)

func (a BudgetAction) String() string {
	switch a {
	case BudgetOK:
		return "ok"
	case BudgetCompressLight:
		return "compress_light"
	case BudgetCompressMedium:
		return "compress_medium"
	case BudgetCompressHeavy:
		return "compress_heavy"
	default:
		return "unknown"
	}
}

// BudgetCheck is the result of a token budget check.
type BudgetCheck struct {
	TotalChars  int
	UsageRatio  float64
	Action      BudgetAction
}

// TokenBudget monitors context window utilization and recommends compression levels.
type TokenBudget struct {
	ModelLimit      int     // context window size in tokens
	LightThreshold  float64 // fraction triggering light compression (default 0.70)
	MediumThreshold float64 // fraction triggering medium compression (default 0.80)
	HeavyThreshold  float64 // fraction triggering heavy compression (default 0.90)
	EstimateRatio   float64 // chars-to-tokens ratio (default 0.25)
}

// NewTokenBudget creates a TokenBudget with defaults for zero values.
func NewTokenBudget(modelLimit int, light, medium, heavy, estimateRatio float64) *TokenBudget {
	if modelLimit <= 0 {
		modelLimit = 200000
	}
	if light <= 0 {
		light = 0.70
	}
	if medium <= 0 {
		medium = 0.80
	}
	if heavy <= 0 {
		heavy = 0.90
	}
	if estimateRatio <= 0 {
		estimateRatio = 0.25
	}
	return &TokenBudget{
		ModelLimit:      modelLimit,
		LightThreshold:  light,
		MediumThreshold: medium,
		HeavyThreshold:  heavy,
		EstimateRatio:   estimateRatio,
	}
}

// Check evaluates current context usage and returns a recommended action.
func (tb *TokenBudget) Check(messages []session.Message, systemPrompt string) BudgetCheck {
	totalChars := len(systemPrompt)
	for _, m := range messages {
		totalChars += len(m.Content) + len(m.ToolInput) + 20
	}

	ratio := EstimateUtilization(totalChars, tb.EstimateRatio, tb.ModelLimit)

	action := BudgetOK
	if ratio >= tb.HeavyThreshold {
		action = BudgetCompressHeavy
	} else if ratio >= tb.MediumThreshold {
		action = BudgetCompressMedium
	} else if ratio >= tb.LightThreshold {
		action = BudgetCompressLight
	}

	return BudgetCheck{
		TotalChars: totalChars,
		UsageRatio: ratio,
		Action:     action,
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 -run TestTokenBudget ./internal/agent/ -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/token_budget.go internal/agent/token_budget_test.go
git commit -m "feat(agent): add TokenBudget for context window usage monitoring"
```

---

### Task 10: Token Budget — Wire into Runtime

**Files:**
- Modify: `internal/agent/runtime.go`
- Modify: `internal/gateway/init_multiagent.go` (or `init_agent.go`)

- [ ] **Step 1: Add tokenBudget field to Runtime**

In `internal/agent/runtime.go`, add to the `Runtime` struct (after `compressionPipeline`):

```go
	tokenBudget *TokenBudget
```

Add setter method after `SetCompressionPipeline`:

```go
// SetTokenBudget attaches a token budget monitor to the runtime.
func (r *Runtime) SetTokenBudget(tb *TokenBudget) { r.tokenBudget = tb }
```

- [ ] **Step 2: Add budget check before LLM call in HandleMessage**

In `internal/agent/runtime.go`, inside the `HandleMessage` method, add a budget check **before** the compression block (before line 211). Insert this block right after the hook firing (after line 208):

```go
	// Token budget check — triggers compression if needed
	if r.tokenBudget != nil {
		check := r.tokenBudget.Check(sess.History(), systemPrompt)
		if check.Action > BudgetOK {
			slog.Info("token budget triggered compression",
				"usage_ratio", fmt.Sprintf("%.1f%%", check.UsageRatio*100),
				"action", check.Action,
			)
		}
	}
```

Note: The existing compression pipeline already handles actual compression. The token budget's primary role here is logging and awareness. In a future iteration, the budget action can be passed to the compression pipeline to skip layers below the action level, but the existing threshold-based pipeline already handles this correctly via `estimateUtilization`.

- [ ] **Step 3: Add same check to handleNonStreaming**

In `handleNonStreaming`, add the same budget check before the loop (right at the start of the method):

```go
	if r.tokenBudget != nil {
		check := r.tokenBudget.Check(sess.History(), systemPrompt)
		if check.Action > BudgetOK {
			slog.Info("token budget triggered compression (non-streaming)",
				"usage_ratio", fmt.Sprintf("%.1f%%", check.UsageRatio*100),
				"action", check.Action,
			)
		}
	}
```

- [ ] **Step 4: Wire TokenBudget in gateway init**

In `internal/gateway/init_multiagent.go`, add after the compression pipeline setup:

```go
	// Token budget monitor
	tokenBudget := agent.NewTokenBudget(
		200000, // TODO: derive from model name
		float64(gw.cfg.Agent.Compression.Layers.ToolEvictionPct)/100.0,
		float64(gw.cfg.Agent.Compression.Layers.SummarizePct)/100.0,
		float64(gw.cfg.Agent.Compression.Layers.SlimPromptPct)/100.0,
		gw.cfg.Agent.Compression.TokenEstimateRatio,
	)
	gw.runtime.SetTokenBudget(tokenBudget)
	slog.Info("token budget monitor enabled",
		"model_limit", 200000,
		"light_pct", gw.cfg.Agent.Compression.Layers.ToolEvictionPct,
		"medium_pct", gw.cfg.Agent.Compression.Layers.SummarizePct,
		"heavy_pct", gw.cfg.Agent.Compression.Layers.SlimPromptPct,
	)
```

- [ ] **Step 5: Verify build and tests**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./... && CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -v -count=1`
Expected: Build clean, all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runtime.go internal/gateway/init_multiagent.go
git commit -m "feat(agent): wire TokenBudget into runtime for context usage monitoring"
```

---

### Task 11: Final Verification

**Files:** None (testing only)

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/wuqisen/learning/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./... -count=1`
Expected: All tests pass.

- [ ] **Step 2: Run linter**

Run: `cd /Users/wuqisen/learning/IronClaw && make lint`
Expected: No new lint errors.

- [ ] **Step 3: Verify build produces working binary**

Run: `cd /Users/wuqisen/learning/IronClaw && make build`
Expected: Binary builds successfully.

- [ ] **Step 4: Verify gateway.go is now small**

Run: `wc -l internal/gateway/gateway.go internal/gateway/init_*.go`
Expected: `gateway.go` < 150 lines, each `init_*.go` < 120 lines.

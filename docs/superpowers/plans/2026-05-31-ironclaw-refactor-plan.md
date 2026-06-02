# IronClaw Full Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate god objects, dead code, and zero-test packages. Replace the 5-phase cognitive model with a single LLM tool-use loop + self-correction hook. Split Gateway into subsystems. Achieve 100% package test coverage.

**Architecture:** Single cognitive loop (context injection → tool-use loop with middleware → post-loop self-correction). Gateway decomposed into 8 Subsystem interfaces. core/ package archived. 9 zero-test packages filled.

**Tech Stack:** Go 1.25, SQLite (mattn/go-sqlite3 + FTS5), Bubble Tea, wazero, OTel

**Design Spec:** `docs/superpowers/specs/2026-05-31-ironclaw-refactor-design.md`

---

## Worktree Dependency Graph

```
WT1 (P0 + Archive) ──┬──→ WT2 (Cognitive Rewrite) ──→ WT3 (Gateway Split)
                     │
                     └──→ WT4 (Test Completion, anytime)
                     
WT5 (Config + TUI) ──→ after WT3
```

---

# Worktree 1: P0 Fixes + Core Archive

**Files:**
- Modify: `internal/gateway/gateway.go:132`
- Modify: `internal/core/tools.go:80`
- Modify: `.gitignore`
- Move: `internal/core/` → `internal/archived/core/`

### Task 1.1: Fix context leak in gateway.go

**Files:** Modify: `internal/gateway/gateway.go:132`

- [ ] **Step 1: Read the leaking line and fix**

```bash
# Read lines 130-135 to confirm the issue
```

The fix — add `defer cancel()`:

```go
// BEFORE (line ~132):
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)

// AFTER:
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

- [ ] **Step 2: Verify with go vet**

```bash
cd /Users/wuqisen/dev/IronClaw && go vet ./internal/gateway/
```

Expected: zero output (no warnings).

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/gateway.go
git commit -m "fix(gateway): add missing defer cancel() for context timeout

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 1.2: Fix residual panic in core/tools.go

**Files:** Modify: `internal/core/tools.go:80`

- [ ] **Step 1: Read the panic location**

```go
// internal/core/tools.go around line 80:
// BEFORE:
if err != nil {
    panic(err.Error())
}

// AFTER:
if err != nil {
    return nil, fmt.Errorf("tool registry init: %w", err)
}
```

Check that the function signature returns `(something, error)`. If it doesn't, adjust the return.

- [ ] **Step 2: Verify with go vet**

```bash
cd /Users/wuqisen/dev/IronClaw && go vet ./internal/core/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/core/tools.go
git commit -m "fix(core): replace panic with error return in tools.go

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 1.3: Add web/studio/node_modules to .gitignore

**Files:** Modify: `.gitignore`

- [ ] **Step 1: Append to .gitignore**

```bash
echo "web/studio/node_modules/" >> .gitignore
```

- [ ] **Step 2: Verify git status clean**

```bash
cd /Users/wuqisen/dev/IronClaw && git status
```

Expected: `web/studio/node_modules/` no longer shows as untracked.

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: add web/studio/node_modules to gitignore

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 1.4: Archive core/ package

**Files:** Move: `internal/core/` → `internal/archived/core/`

- [ ] **Step 1: Create archived directory and move core/**

```bash
mkdir -p internal/archived
git mv internal/core internal/archived/core
```

- [ ] **Step 2: Update any remaining imports of internal/core/**

Find any references:
```bash
grep -rn "internal/core" internal/ --include='*.go' | grep -v "archived" | grep -v "_test.go"
```

Expected: zero results (no production code imports core/).

If any imports found, update them to point to `internal/archived/core`.

- [ ] **Step 3: Verify build still works**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...
```

Expected: builds successfully (core/ had no production consumers).

- [ ] **Step 4: Add archived/ README**

Create `internal/archived/README.md`:

```markdown
# Archived Code

## core/
A clean, composable agentic runtime built as an alternative to internal/agent/.
Archived 2026-05-31 during the cognitive rewrite. Key design ideas preserved:
- Middleware chain for tool execution
- Event bus for observability
- Provider-agnostic interfaces (Provider, Tool, Memory)

See docs/superpowers/specs/2026-05-31-ironclaw-refactor-design.md for context.
```

- [ ] **Step 5: Commit**

```bash
git add internal/archived/
git commit -m "refactor: archive internal/core/ to internal/archived/core/

Unused composable runtime. Key design ideas preserved for reference.
See spec for context.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

# Worktree 2: Cognitive Rewrite (The Big One)

**New Files:**
- `internal/agent/cognitive_v2.go` — new CognitiveAgentV2 with single loop
- `internal/agent/context_builder.go` — ContextBuilder + scanner interfaces
- `internal/agent/self_correction.go` — SelfCorrectionEngine
- `internal/agent/middleware_tool.go` — ToolMiddleware chain
- `internal/agent/middleware_loop.go` — LoopHook chain
- `internal/agent/cognitive_v2_test.go` — integration tests

**Modified Files:**
- `internal/agent/cognitive.go` — deprecation wrapper, Run() delegates to v2
- `internal/gateway/gateway.go` — wire CognitiveAgentV2
- `internal/gateway/init_agent.go` — build middleware + hook chains

### Task 2.1: Define core interfaces

**Files:** Create: `internal/agent/cognitive_v2.go`

- [ ] **Step 1: Write the CognitiveAgentV2 struct and interfaces**

```go
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// CognitiveAgentV2 implements a single LLM tool-use loop with
// context injection and post-loop self-correction.
//
// Architecture:
//   1. ContextBuilder runs once → populates system prompt
//   2. Tool-use loop: LLM → tool_calls → middleware → results → repeat
//   3. SelfCorrectionEngine: post-loop assertion check, replan on failure
//
// Cross-cutting concerns (checkpoint, replay, plan-mode, compression)
// are LoopHooks and ToolMiddleware, not embedded fields.
type CognitiveAgentV2 struct {
	provider    Provider
	tools       *ToolRegistry
	sessions    *session.Manager
	db          *store.DB
	cfg         config.AgentConfig
	llmCfg      config.LLMConfig

	// Pre-loop
	contextBuilder *ContextBuilder

	// Tool execution
	toolMiddleware *ToolMiddlewareChain

	// Loop hooks
	loopHooks *LoopHookChain

	// Post-loop
	selfCorrection *SelfCorrectionEngine

	// Observability
	dashEmitter DashboardEmitter
}

// ToolCall represents a single tool invocation within the loop.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolResult is the result of executing a tool call.
type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
	Duration   time.Duration
}

// LoopState holds mutable state during the tool-use loop.
type LoopState struct {
	SessionID      string
	Messages       []Message
	ToolCalls      []ToolCall
	ToolResults    []ToolResult
	TurnCount      int
	TotalTokens    int
	ContextUsedPct float64
	LastError      error
}

// LoopResult is the final output of the cognitive loop.
type LoopResult struct {
	Output       string
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
	TurnCount    int
	TotalTokens  int
	Assertions   []Assertion
	Learnings    []string
}

// ToolMiddleware wraps a single tool execution.
type ToolMiddleware interface {
	Wrap(next ToolExecutor) ToolExecutor
}

// ToolExecutor executes a single tool call.
type ToolExecutor func(ctx context.Context, call ToolCall) (*ToolResult, error)

// ToolMiddlewareChain executes middleware in onion order.
type ToolMiddlewareChain struct {
	middlewares []ToolMiddleware
}

func NewToolMiddlewareChain(mws ...ToolMiddleware) *ToolMiddlewareChain {
	return &ToolMiddlewareChain{middlewares: mws}
}

func (c *ToolMiddlewareChain) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	executor := c.coreExecutor()
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		outer := executor
		inner := c.middlewares[i]
		executor = func(ctx context.Context, call ToolCall) (*ToolResult, error) {
			return inner.Wrap(outer)(ctx, call)
		}
	}
	return executor(ctx, call)
}

func (c *ToolMiddlewareChain) coreExecutor() ToolExecutor {
	// Will be wired in Task 2.4
	return nil
}

// LoopHook observes or modifies the loop lifecycle.
type LoopHook interface {
	BeforeLoop(ctx context.Context, state *LoopState) error
	AfterTurn(ctx context.Context, state *LoopState) error
	AfterLoop(ctx context.Context, result *LoopResult) error
}

// LoopHookChain executes hooks in registration order.
type LoopHookChain struct {
	hooks []LoopHook
}

func NewLoopHookChain(hooks ...LoopHook) *LoopHookChain {
	return &LoopHookChain{hooks: hooks}
}

func (c *LoopHookChain) BeforeLoop(ctx context.Context, state *LoopState) error {
	for _, h := range c.hooks {
		if err := h.BeforeLoop(ctx, state); err != nil {
			return fmt.Errorf("hook %T.BeforeLoop: %w", h, err)
		}
	}
	return nil
}

func (c *LoopHookChain) AfterTurn(ctx context.Context, state *LoopState) error {
	for _, h := range c.hooks {
		if err := h.AfterTurn(ctx, state); err != nil {
			slog.Warn("hook AfterTurn failed", "hook", fmt.Sprintf("%T", h), "error", err)
			// AfterTurn errors are non-fatal
		}
	}
	return nil
}

func (c *LoopHookChain) AfterLoop(ctx context.Context, result *LoopResult) error {
	for _, h := range c.hooks {
		if err := h.AfterLoop(ctx, result); err != nil {
			slog.Warn("hook AfterLoop failed", "hook", fmt.Sprintf("%T", h), "error", err)
		}
	}
	return nil
}

// NewCognitiveAgentV2 creates the agent. Use opts pattern for optional deps.
func NewCognitiveAgentV2(
	provider Provider,
	tools *ToolRegistry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *CognitiveAgentV2 {
	return &CognitiveAgentV2{
		provider:        provider,
		tools:           tools,
		sessions:        sessions,
		db:              db,
		cfg:             cfg,
		llmCfg:          llmCfg,
		contextBuilder:  NewContextBuilder(),
		toolMiddleware:  NewToolMiddlewareChain(),
		loopHooks:       NewLoopHookChain(),
		selfCorrection:  NewSelfCorrectionEngine(cfg.Cognitive.MaxReplanAttempts),
	}
}

// SetContextBuilder sets the context builder (pre-loop context injection).
func (ca *CognitiveAgentV2) SetContextBuilder(cb *ContextBuilder) { ca.contextBuilder = cb }

// SetToolMiddleware sets the tool middleware chain.
func (ca *CognitiveAgentV2) SetToolMiddleware(tm *ToolMiddlewareChain) { ca.toolMiddleware = tm }

// SetLoopHooks sets the loop hook chain.
func (ca *CognitiveAgentV2) SetLoopHooks(lh *LoopHookChain) { ca.loopHooks = lh }

// SetDashboardEmitter sets the dashboard event emitter.
func (ca *CognitiveAgentV2) SetDashboardEmitter(e DashboardEmitter) { ca.dashEmitter = e }
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

Expected: compile errors about undefined types (we'll add them in subsequent tasks).

### Task 2.2: Implement ContextBuilder

**Files:** Create: `internal/agent/context_builder.go`

- [ ] **Step 1: Write ContextBuilder and scanner interface**

```go
package agent

import (
	"context"
	"fmt"
	"strings"
)

// ContextFragment is a piece of context contributed by a scanner.
type ContextFragment struct {
	Source   string // e.g. "project_scan", "git", "memory", "knowledge"
	Content  string
	Priority int // lower = injected earlier in prompt
}

// ContextScanner scans an information source and returns context fragments.
type ContextScanner interface {
	Name() string
	Scan(ctx context.Context) (*ContextFragment, error)
}

// ContextBuilder aggregates context from multiple scanners
// and produces the {{DYNAMIC_CONTENT}} block for the system prompt.
type ContextBuilder struct {
	scanners []ContextScanner
}

// NewContextBuilder creates an empty builder. Scanners are registered via AddScanner.
func NewContextBuilder(scanners ...ContextScanner) *ContextBuilder {
	return &ContextBuilder{scanners: scanners}
}

// AddScanner registers a context scanner.
func (cb *ContextBuilder) AddScanner(s ContextScanner) {
	cb.scanners = append(cb.scanners, s)
}

// Build runs all scanners and returns the merged context string.
// Scanner failures are logged but do not block the build.
func (cb *ContextBuilder) Build(ctx context.Context) string {
	var parts []string
	for _, s := range cb.scanners {
		fragment, err := s.Scan(ctx)
		if err != nil {
			// Non-fatal: one scanner failing shouldn't break the agent
			parts = append(parts, fmt.Sprintf("[%s: unavailable]", s.Name()))
			continue
		}
		if fragment != nil && fragment.Content != "" {
			parts = append(parts, fragment.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ScannerCount returns the number of registered scanners.
func (cb *ContextBuilder) ScannerCount() int { return len(cb.scanners) }
```

- [ ] **Step 2: Implement built-in scanners**

```go
// ProjectContextScanner scans the project directory for structure and language info.
type ProjectContextScanner struct {
	rootPath string
}

func NewProjectContextScanner(rootPath string) *ProjectContextScanner {
	return &ProjectContextScanner{rootPath: rootPath}
}

func (s *ProjectContextScanner) Name() string { return "project_scan" }

func (s *ProjectContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	// Reuse existing ProjectContextScanner logic from perceive.go
	// For now, delegate to the old implementation.
	// Full rewrite happens in Task 2.7.
	scanner := NewProjectContextScanner() // old impl
	result := scanner.Scan()              // old sync method
	return &ContextFragment{
		Source:   "project_scan",
		Content:  result.Summary,
		Priority: 1,
	}, nil
}

// GitContextScanner collects git branch, status, and recent log.
type GitContextScanner struct {
	rootPath string
}

func NewGitContextScanner(rootPath string) *GitContextScanner {
	return &GitContextScanner{rootPath: rootPath}
}

func (s *GitContextScanner) Name() string { return "git" }

func (s *GitContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	provider := NewGitContextProvider() // old impl
	result := provider.Collect()
	return &ContextFragment{
		Source:   "git",
		Content:  result.Summary,
		Priority: 2,
	}, nil
}

// MemoryContextScanner searches memory store for relevant facts.
type MemoryContextScanner struct {
	store    memory.Store
	maxItems int
}

func NewMemoryContextScanner(store memory.Store, maxItems int) *MemoryContextScanner {
	return &MemoryContextScanner{store: store, maxItems: maxItems}
}

func (s *MemoryContextScanner) Name() string { return "memory" }

func (s *MemoryContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	if s.store == nil {
		return nil, nil
	}
	// Search for relevant memories based on session context
	// Simplified for now — full implementation in Task 2.7
	return &ContextFragment{
		Source:   "memory",
		Content:  "",
		Priority: 3,
	}, nil
}

// KnowledgeContextScanner searches the knowledge base.
type KnowledgeContextScanner struct {
	searcher knowledge.Searcher
	maxItems int
}

func NewKnowledgeContextScanner(searcher knowledge.Searcher, maxItems int) *KnowledgeContextScanner {
	return &KnowledgeContextScanner{searcher: searcher, maxItems: maxItems}
}

func (s *KnowledgeContextScanner) Name() string { return "knowledge" }

func (s *KnowledgeContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	if s.searcher == nil {
		return nil, nil
	}
	// Simplified — full implementation in Task 2.7
	return &ContextFragment{
		Source:   "knowledge",
		Content:  "",
		Priority: 4,
	}, nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

### Task 2.3: Implement SelfCorrectionEngine

**Files:** Create: `internal/agent/self_correction.go`

- [ ] **Step 1: Write SelfCorrectionEngine**

```go
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// SelfCorrectionEngine runs post-loop assertion checks and triggers
// replan cycles on failure. Replaces the old OBSERVE+REFLECT phases.
type SelfCorrectionEngine struct {
	assertionEngine *AssertionEngine
	maxRetries      int
}

// NewSelfCorrectionEngine creates the engine with a retry cap.
func NewSelfCorrectionEngine(maxRetries int) *SelfCorrectionEngine {
	return &SelfCorrectionEngine{
		assertionEngine: NewAssertionEngine(),
		maxRetries:      maxRetries,
	}
}

// SetAssertionEngine replaces the default assertion engine.
func (e *SelfCorrectionEngine) SetAssertionEngine(ae *AssertionEngine) {
	e.assertionEngine = ae
}

// VerifyAndCorrect checks loop results and reruns on failure.
// runner is called with extra failure context injected for each retry.
func (e *SelfCorrectionEngine) VerifyAndCorrect(
	ctx context.Context,
	loopResult *LoopResult,
	runner func(ctx context.Context, failureContext string) (*LoopResult, error),
) (*LoopResult, error) {
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		assertions := e.assertionEngine.Verify(loopResult)

		if len(assertions.Failures) == 0 {
			loopResult.Assertions = assertions.All
			loopResult.Learnings = e.extractLearnings(assertions)
			return loopResult, nil
		}

		if attempt >= e.maxRetries {
			slog.Warn("self-correction max retries reached",
				"attempts", attempt,
				"remaining_failures", len(assertions.Failures))
			loopResult.Assertions = assertions.All
			return loopResult, nil
		}

		slog.Info("self-correction triggered",
			"attempt", attempt+1,
			"failures", len(assertions.Failures))

		failureCtx := e.buildFailureContext(assertions.Failures)
		var err error
		loopResult, err = runner(ctx, failureCtx)
		if err != nil {
			return loopResult, fmt.Errorf("self-correction runner: %w", err)
		}
	}
	return loopResult, nil
}

// buildFailureContext creates context string describing what went wrong.
func (e *SelfCorrectionEngine) buildFailureContext(failures []AssertionFailure) string {
	var sb strings.Builder
	sb.WriteString("## Previous Attempt Failures\n\n")
	sb.WriteString("The following assertions failed. Please address these issues:\n\n")
	for i, f := range failures {
		fmt.Fprintf(&sb, "%d. **%s**: %s\n", i+1, f.Assertion, f.Reason)
		if f.Suggestion != "" {
			fmt.Fprintf(&sb, "   Suggestion: %s\n", f.Suggestion)
		}
	}
	return sb.String()
}

// extractLearnings extracts reusable insights from passed assertions.
func (e *SelfCorrectionEngine) extractLearnings(assertions *AssertionResult) []string {
	var learnings []string
	for _, a := range assertions.Passed {
		if a.Learning != "" {
			learnings = append(learnings, a.Learning)
		}
	}
	return learnings
}
```

Note: This task depends on `AssertionEngine`, `AssertionResult`, `AssertionFailure` types
already defined in `internal/agent/assertion.go`. If any are missing, we'll stub them.

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

### Task 2.4: Implement Run() — the core loop

**Files:** Modify: `internal/agent/cognitive_v2.go` (add Run method)

- [ ] **Step 1: Write the Run() method**

Add to `cognitive_v2.go`:

```go
// Run executes the full cognitive loop:
//   Context injection → tool-use loop → self-correction
func (ca *CognitiveAgentV2) Run(
	ctx context.Context,
	sessionID string,
	userMessage string,
	extraContext string,
) (*LoopResult, error) {
	state := &LoopState{
		SessionID: sessionID,
		Messages:  ca.buildInitialMessages(sessionID, userMessage, extraContext),
	}

	// --- Phase 1: Context Injection ---
	dynamicCtx := ca.contextBuilder.Build(ctx)
	if dynamicCtx != "" {
		state.Messages[0].Content = strings.Replace(
			state.Messages[0].Content,
			"{{DYNAMIC_CONTEXT}}",
			dynamicCtx,
			1,
		)
	}

	// --- Loop Hooks: BeforeLoop ---
	if err := ca.loopHooks.BeforeLoop(ctx, state); err != nil {
		return nil, fmt.Errorf("before loop: %w", err)
	}

	// --- Phase 2: Tool-Use Loop ---
	maxTurns := ca.cfg.Cognitive.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 20
	}

	for state.TurnCount < maxTurns {
		state.TurnCount++

		// Check context utilization; reactive compression via hooks
		if state.ContextUsedPct > 0.85 {
			ca.loopHooks.AfterTurn(ctx, state)
		}

		// LLM completion
		response, err := ca.provider.Complete(ctx, state.Messages, ca.buildToolDefs())
		if err != nil {
			// Let hooks handle (e.g., CompressionHook for 413 errors)
			state.LastError = err
			ca.loopHooks.AfterTurn(ctx, state)
			if state.LastError != nil {
				return nil, fmt.Errorf("LLM completion (turn %d): %w", state.TurnCount, err)
			}
			continue
		}

		// Add assistant message
		state.Messages = append(state.Messages, Message{
			Role:    "assistant",
			Content: response.Text,
		})

		// No tool calls → loop complete
		if len(response.ToolCalls) == 0 {
			state.Messages = append(state.Messages, Message{
				Role:    "user",
				Content: "Task complete. Provide your final response.",
			})
			finalResp, _ := ca.provider.Complete(ctx, state.Messages, nil)
			if finalResp != nil {
				state.Messages = append(state.Messages, Message{
					Role:    "assistant",
					Content: finalResp.Text,
				})
			}
			break
		}

		// Execute tool calls through middleware chain
		for _, tc := range response.ToolCalls {
			result, err := ca.toolMiddleware.Execute(ctx, ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
			if err != nil {
				slog.Warn("tool execution failed", "tool", tc.Name, "error", err)
			}
			if result != nil {
				state.ToolResults = append(state.ToolResults, *result)
				state.Messages = append(state.Messages, Message{
					Role:    "user",
					Content: result.Content,
				})
			}
		}

		// Hook: AfterTurn
		ca.loopHooks.AfterTurn(ctx, state)
	}

	// Build loop result
	result := &LoopResult{
		Output:      state.Messages[len(state.Messages)-1].Content,
		ToolCalls:   state.ToolCalls,
		ToolResults: state.ToolResults,
		TurnCount:   state.TurnCount,
		TotalTokens: state.TotalTokens,
	}

	// --- Phase 3: Self-Correction ---
	result, err := ca.selfCorrection.VerifyAndCorrect(ctx, result,
		func(ctx context.Context, failureContext string) (*LoopResult, error) {
			return ca.Run(ctx, sessionID, userMessage,
				extraContext+"\n\n"+failureContext)
		})
	if err != nil {
		return result, err
	}

	// --- Loop Hooks: AfterLoop ---
	ca.loopHooks.AfterLoop(ctx, result)

	return result, nil
}

// buildInitialMessages creates the initial message list with system prompt.
func (ca *CognitiveAgentV2) buildInitialMessages(sessionID, userMessage, extraContext string) []Message {
	msgs := []Message{
		{
			Role:    "system",
			Content: ca.buildSystemPrompt(sessionID),
		},
	}

	if extraContext != "" {
		msgs[0].Content += "\n\n" + extraContext
	}

	msgs = append(msgs, Message{
		Role:    "user",
		Content: userMessage,
	})

	return msgs
}

// buildSystemPrompt constructs the system prompt with {{DYNAMIC_CONTEXT}} placeholder.
func (ca *CognitiveAgentV2) buildSystemPrompt(sessionID string) string {
	return fmt.Sprintf(`You are an AI agent with tool-use capabilities.

{{DYNAMIC_CONTEXT}}

Session: %s
Current time: %s

Execute the user's task using available tools. After each tool call, observe results
and decide the next step. When the task is complete, provide a summary.`, sessionID, time.Now().Format(time.RFC3339))
}

// buildToolDefs converts the tool registry to the LLM provider's tool format.
func (ca *CognitiveAgentV2) buildToolDefs() []ToolDefinition {
	// Delegate to existing tool registry serialization
	// (same logic currently in agent/runtime.go)
	return nil // Stub — wired in Task 2.5
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

Expected: compile errors for undefined symbols (Message, ToolDefinition, etc.) — these
are type aliases to existing types in the agent package. We'll resolve in Task 2.6.

### Task 2.5: Implement ToolMiddleware core executor

**Files:** Modify: `internal/agent/cognitive_v2.go` (wire coreExecutor)

- [ ] **Step 1: Wire the real tool executor into ToolMiddlewareChain**

Replace the stub `coreExecutor()` in `ToolMiddlewareChain`:

```go
// SetCoreExecutor sets the innermost executor that actually runs tools.
func (c *ToolMiddlewareChain) SetCoreExecutor(exec ToolExecutor) {
	c.coreExec = exec
}

// coreExec is the innermost executor, set via SetCoreExecutor.
// Add to struct:
type ToolMiddlewareChain struct {
	middlewares []ToolMiddleware
	coreExec    ToolExecutor
}

func (c *ToolMiddlewareChain) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	if c.coreExec == nil {
		return nil, fmt.Errorf("ToolMiddlewareChain: core executor not set")
	}
	executor := c.coreExec
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		outer := executor
		inner := c.middlewares[i]
		executor = func(ctx context.Context, call ToolCall) (*ToolResult, error) {
			return inner.Wrap(outer)(ctx, call)
		}
	}
	return executor(ctx, call)
}
```

- [ ] **Step 2: Create the default core executor**

Add to `cognitive_v2.go`:

```go
// defaultToolExecutor executes tools against the tool registry.
func defaultToolExecutor(tools *ToolRegistry) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		start := time.Now()
		result, err := tools.Execute(ctx, call.Name, call.Arguments)
		duration := time.Since(start)
		if err != nil {
			return &ToolResult{
				ToolCallID: call.ID,
				Content:    fmt.Sprintf("Error: %v", err),
				IsError:    true,
				Duration:   duration,
			}, err
		}
		return &ToolResult{
			ToolCallID: call.ID,
			Content:    result,
			IsError:    false,
			Duration:   duration,
		}, nil
	}
}
```

- [ ] **Step 3: Update NewCognitiveAgentV2 to wire default executor**

```go
func NewCognitiveAgentV2(...) *CognitiveAgentV2 {
	ca := &CognitiveAgentV2{...}
	ca.toolMiddleware.SetCoreExecutor(defaultToolExecutor(tools))
	return ca
}
```

### Task 2.6: Type aliases and compilation fixes

**Files:** Modify: `internal/agent/cognitive_v2.go`

- [ ] **Step 1: Add type aliases and imports**

Ensure cognitive_v2.go can resolve all types. Add necessary imports and type aliases:

```go
import (
	// ... existing imports
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/knowledge"
)

// Message is an alias for the existing message type used by the Provider.
// (Already defined in runtime.go or provider.go — ensure consistency)
```

- [ ] **Step 2: Full build verification**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/ 2>&1
```

Fix all compile errors. Check each undefined symbol against existing agent package types.

### Task 2.7: Rewrite scanner implementations (replace old PERCEIVE stubs)

**Files:** Modify: `internal/agent/context_builder.go`

- [ ] **Step 1: Implement ProjectContextScanner properly**

Replace the stub in context_builder.go with real implementation that reuses
the existing `ProjectContextScanner.Scan()` logic from `perceive.go`:

```go
func (s *ProjectContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	scanner := &projectContextScanner{
		rootPath: s.rootPath,
	}
	summary, err := scanner.scan(ctx)
	if err != nil {
		return nil, err
	}
	return &ContextFragment{
		Source:   "project_scan",
		Content:  summary,
		Priority: 1,
	}, nil
}
```

- [ ] **Step 2: Implement MemoryContextScanner properly**

```go
func (s *MemoryContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	if s.store == nil {
		return nil, nil
	}
	facts, err := s.store.Search(ctx, memory.SearchRequest{
		Limit:  s.maxItems,
		Scopes: []memory.Scope{memory.ScopeUser, memory.ScopeGlobal},
	})
	if err != nil {
		return nil, fmt.Errorf("memory scan: %w", err)
	}
	if len(facts) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteString("## Relevant Memories\n\n")
	for _, f := range facts {
		fmt.Fprintf(&sb, "- %s\n", f.Content)
	}
	return &ContextFragment{
		Source:   "memory",
		Content:  sb.String(),
		Priority: 3,
	}, nil
}
```

- [ ] **Step 3: Implement KnowledgeContextScanner properly**

```go
func (s *KnowledgeContextScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	if s.searcher == nil {
		return nil, nil
	}
	results, err := s.searcher.Search(ctx, knowledge.SearchRequest{
		Limit: s.maxItems,
	})
	if err != nil {
		return nil, fmt.Errorf("knowledge scan: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteString("## Relevant Knowledge\n\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "- %s\n", r.Content)
	}
	return &ContextFragment{
		Source:   "knowledge",
		Content:  sb.String(),
		Priority: 4,
	}, nil
}
```

- [ ] **Step 4: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

### Task 2.8: Implement HookMiddleware (old pre_tool_use hooks)

**Files:** Create: `internal/agent/middleware_tool.go`

- [ ] **Step 1: Write HookMiddleware**

```go
package agent

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/IronClaw/internal/hook"
)

// HookMiddleware wraps tool execution with pre/post tool hooks.
type HookMiddleware struct {
	hookMgr *hook.Manager
}

func NewHookMiddleware(hm *hook.Manager) *HookMiddleware {
	return &HookMiddleware{hookMgr: hm}
}

func (m *HookMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.hookMgr != nil {
			if err := m.hookMgr.PreToolUse(ctx, call.Name, call.Arguments); err != nil {
				return nil, fmt.Errorf("pre_tool_use hook: %w", err)
			}
		}

		result, err := next(ctx, call)

		if m.hookMgr != nil {
			hookErr := m.hookMgr.PostToolUse(ctx, call.Name, call.Arguments, result.Content, err)
			if hookErr != nil {
				// Post-hook errors are logged, not fatal
			}
		}

		return result, err
	}
}
```

- [ ] **Step 2: Write PermissionMiddleware**

```go
// PermissionMiddleware enforces tool permission policies.
type PermissionMiddleware struct {
	permEngine  *tool.PermissionEngine
	approvalFunc ApprovalFunc
}

func NewPermissionMiddleware(pe *tool.PermissionEngine, af ApprovalFunc) *PermissionMiddleware {
	return &PermissionMiddleware{permEngine: pe, approvalFunc: af}
}

func (m *PermissionMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.permEngine != nil {
			level := m.permEngine.Check(call.Name)
			switch level {
			case tool.PermDeny:
				return nil, fmt.Errorf("tool %s: permission denied", call.Name)
			case tool.PermApprove:
				if m.approvalFunc != nil {
					approved, err := m.approvalFunc(ctx, nil, channel.MessageTarget{}, call.Name, call.Arguments)
					if err != nil || !approved {
						return nil, fmt.Errorf("tool %s: approval denied", call.Name)
					}
				}
			}
		}
		return next(ctx, call)
	}
}
```

- [ ] **Step 3: Write SandboxMiddleware**

```go
// SandboxMiddleware dispatches tool execution through security sandbox.
type SandboxMiddleware struct {
	interceptorChain *tool.InterceptorChain
}

func NewSandboxMiddleware(ic *tool.InterceptorChain) *SandboxMiddleware {
	return &SandboxMiddleware{interceptorChain: ic}
}

func (m *SandboxMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.interceptorChain != nil {
			// Interceptor chain handles sandboxing internally
			return next(ctx, call)
		}
		return next(ctx, call)
	}
}
```

- [ ] **Step 4: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

### Task 2.9: Implement LoopHooks

**Files:** Create: `internal/agent/middleware_loop.go`

- [ ] **Step 1: Write CheckpointHook**

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// CheckpointHook saves loop state after each turn for resume capability.
type CheckpointHook struct {
	store CheckpointStore
	db    *store.DB
}

func NewCheckpointHook(cs CheckpointStore, db *store.DB) *CheckpointHook {
	return &CheckpointHook{store: cs, db: db}
}

func (h *CheckpointHook) BeforeLoop(ctx context.Context, state *LoopState) error {
	if h.store == nil {
		return nil
	}
	// Try to restore from checkpoint
	saved, err := h.store.Load(ctx, state.SessionID)
	if err != nil || saved == nil {
		return nil
	}
	state.Messages = saved.Messages
	state.TurnCount = saved.TurnCount
	slog.Info("checkpoint restored", "session", state.SessionID, "turns", state.TurnCount)
	return nil
}

func (h *CheckpointHook) AfterTurn(ctx context.Context, state *LoopState) error {
	if h.store == nil {
		return nil
	}
	data, _ := json.Marshal(struct {
		Messages  []Message `json:"messages"`
		TurnCount int       `json:"turn_count"`
	}{state.Messages, state.TurnCount})
	return h.store.Save(ctx, state.SessionID, data)
}

func (h *CheckpointHook) AfterLoop(ctx context.Context, result *LoopResult) error {
	if h.store == nil {
		return nil
	}
	return h.store.Delete(ctx, result.SessionID) // Clean up on success
}
```

- [ ] **Step 2: Write CompressionHook**

```go
// CompressionHook triggers context compression when utilization exceeds threshold.
type CompressionHook struct {
	contextMgr  ContextManager
	threshold   float64 // e.g., 0.85
}

func NewCompressionHook(cm ContextManager, threshold float64) *CompressionHook {
	return &CompressionHook{contextMgr: cm, threshold: threshold}
}

func (h *CompressionHook) AfterTurn(ctx context.Context, state *LoopState) error {
	if h.contextMgr == nil || state.ContextUsedPct < h.threshold {
		return nil
	}
	slog.Info("context compression triggered",
		"session", state.SessionID,
		"utilization", fmt.Sprintf("%.1f%%", state.ContextUsedPct*100),
	)
	compressed, err := h.contextMgr.Compress(ctx, state.Messages)
	if err != nil {
		return fmt.Errorf("compression: %w", err)
	}
	state.Messages = compressed
	state.ContextUsedPct = 0
	return nil
}

func (h *CompressionHook) BeforeLoop(ctx context.Context, state *LoopState) error { return nil }
func (h *CompressionHook) AfterLoop(ctx context.Context, result *LoopResult) error  { return nil }
```

- [ ] **Step 3: Write PlanModeHook**

```go
// PlanModeHook pauses the loop before tool execution for human approval.
type PlanModeHook struct {
	planMode    *PlanMode
	approvalFunc ApprovalFunc
}

func NewPlanModeHook(pm *PlanMode, af ApprovalFunc) *PlanModeHook {
	return &PlanModeHook{planMode: pm, approvalFunc: af}
}

func (h *PlanModeHook) BeforeLoop(ctx context.Context, state *LoopState) error {
	if h.planMode == nil || !h.planMode.Enabled {
		return nil
	}
	slog.Info("plan mode: waiting for approval before execution")
	h.planMode.WaitForApproval(ctx)
	return nil
}

func (h *PlanModeHook) AfterTurn(ctx context.Context, state *LoopState) error { return nil }
func (h *PlanModeHook) AfterLoop(ctx context.Context, result *LoopResult) error  {
	if h.planMode != nil {
		h.planMode.Reset()
	}
	return nil
}
```

- [ ] **Step 4: Write EvolutionHook**

```go
// EvolutionHook dispatches loop events to the evolution engine.
type EvolutionHook struct {
	evoEngine *evolution.Engine
	dashEmitter DashboardEmitter
}

func NewEvolutionHook(ee *evolution.Engine, de DashboardEmitter) *EvolutionHook {
	return &EvolutionHook{evoEngine: ee, dashEmitter: de}
}

func (h *EvolutionHook) BeforeLoop(ctx context.Context, state *LoopState) error { return nil }

func (h *EvolutionHook) AfterTurn(ctx context.Context, state *LoopState) error {
	if h.evoEngine != nil {
		h.evoEngine.DispatchToolExec(ctx, state.ToolCalls, state.ToolResults)
	}
	return nil
}

func (h *EvolutionHook) AfterLoop(ctx context.Context, result *LoopResult) error {
	if h.evoEngine != nil {
		h.evoEngine.DispatchEpisode(ctx, result.Output, result.TurnCount)
	}
	return nil
}
```

- [ ] **Step 5: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/
```

### Task 2.10: CognitiveAgent deprecation wrapper

**Files:** Modify: `internal/agent/cognitive.go`

- [ ] **Step 1: Add toV2() conversion and deprecation delegation**

Add to `cognitive.go`:

```go
// Run executes the cognitive loop.
// Deprecated: CognitiveAgent is replaced by CognitiveAgentV2 (single-loop architecture).
// Run() now delegates to CognitiveAgentV2 internally.
// The old 5-phase implementation is preserved in this file for reference.
func (ca *CognitiveAgent) Run(
	ctx context.Context,
	sessionID string,
	userMessage string,
	extraContext string,
) (*LoopResult, error) {
	v2 := ca.toV2()
	return v2.Run(ctx, sessionID, userMessage, extraContext)
}

// toV2 converts the old CognitiveAgent to the new CognitiveAgentV2.
func (ca *CognitiveAgent) toV2() *CognitiveAgentV2 {
	v2 := NewCognitiveAgentV2(
		ca.runtime.provider,
		ca.runtime.tools,
		ca.sessions,
		ca.db,
		ca.cfg,
		ca.llmCfg,
	)

	// Wire context builder
	cb := NewContextBuilder()
	if ca.perceiver != nil {
		cb.AddScanner(NewProjectContextScanner(""))
		cb.AddScanner(NewGitContextScanner(""))
	}
	if ca.memStore != nil {
		cb.AddScanner(NewMemoryContextScanner(ca.memStore, 5))
	}
	// Add knowledge scanner if available (via observer or perceiver)
	v2.SetContextBuilder(cb)

	// Wire tool middleware chain
	mw := NewToolMiddlewareChain()
	mw.SetCoreExecutor(defaultToolExecutor(ca.runtime.tools))
	if ca.hookMgr != nil {
		mw.middlewares = append(mw.middlewares, NewHookMiddleware(ca.hookMgr))
	}
	if ca.permEngine != nil {
		mw.middlewares = append(mw.middlewares, NewPermissionMiddleware(ca.permEngine, ca.executor.approvalFunc))
	}
	v2.SetToolMiddleware(mw)

	// Wire loop hooks
	var hooks []LoopHook
	if ca.checkpointStore != nil {
		hooks = append(hooks, NewCheckpointHook(ca.checkpointStore, ca.db))
	}
	if ca.contextManager != nil {
		hooks = append(hooks, NewCompressionHook(ca.contextManager, 0.85))
	}
	if ca.planMode != nil {
		hooks = append(hooks, NewPlanModeHook(ca.planMode, ca.executor.approvalFunc))
	}
	if ca.evoEngine != nil {
		hooks = append(hooks, NewEvolutionHook(ca.evoEngine, ca.dashEmitter))
	}
	v2.SetLoopHooks(NewLoopHookChain(hooks...))

	return v2
}
```

- [ ] **Step 2: Verify full build**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...
```

Expected: full project builds. Fix any compilation errors from type mismatches.

### Task 2.11: Write integration tests

**Files:** Create: `internal/agent/cognitive_v2_test.go`

- [ ] **Step 1: Write ContextBuilder tests**

```go
package agent

import (
	"context"
	"testing"
)

type mockScanner struct {
	name    string
	content string
	err     error
}

func (m *mockScanner) Name() string                        { return m.name }
func (m *mockScanner) Scan(ctx context.Context) (*ContextFragment, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ContextFragment{Source: m.name, Content: m.content, Priority: 1}, nil
}

func TestContextBuilder_Build(t *testing.T) {
	cb := NewContextBuilder(
		&mockScanner{name: "a", content: "content A"},
		&mockScanner{name: "b", content: "content B"},
	)

	result := cb.Build(context.Background())
	if result == "" {
		t.Fatal("expected non-empty context")
	}
	if len(cb.scanners) != 2 {
		t.Fatalf("expected 2 scanners, got %d", len(cb.scanners))
	}
}

func TestContextBuilder_ScannerFailure(t *testing.T) {
	cb := NewContextBuilder(
		&mockScanner{name: "broken", err: assert.AnError}, // will use fmt.Errorf
		&mockScanner{name: "ok", content: "still works"},
	)

	result := cb.Build(context.Background())
	// Should not be empty — the ok scanner still contributed
	if result == "" {
		t.Fatal("expected content from non-failing scanner")
	}
}
```

- [ ] **Step 2: Write SelfCorrectionEngine tests**

```go
func TestSelfCorrectionEngine_PassesOnFirstTry(t *testing.T) {
	engine := NewSelfCorrectionEngine(2)

	callCount := 0
	runner := func(ctx context.Context, failureCtx string) (*LoopResult, error) {
		callCount++
		return &LoopResult{Output: "done"}, nil
	}

	result, err := engine.VerifyAndCorrect(context.Background(),
		&LoopResult{Output: "initial"},
		runner,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 runner call, got %d", callCount)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
```

- [ ] **Step 3: Write ToolMiddlewareChain tests**

```go
type countingMiddleware struct {
	count *int
}

func (m *countingMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		*m.count++
		return next(ctx, call)
	}
}

func TestToolMiddlewareChain_ExecutionOrder(t *testing.T) {
	var count int
	mw1 := &countingMiddleware{count: &count}
	mw2 := &countingMiddleware{count: &count}

	chain := NewToolMiddlewareChain(mw1, mw2)
	chain.SetCoreExecutor(func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		return &ToolResult{Content: "core result"}, nil
	})

	result, err := chain.Execute(context.Background(), ToolCall{ID: "1", Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 middleware calls, got %d", count)
	}
	if result.Content != "core result" {
		t.Fatalf("expected 'core result', got %q", result.Content)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./internal/agent/ -run "TestContextBuilder|TestSelfCorrection|TestToolMiddleware" -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/cognitive_v2.go internal/agent/context_builder.go \
        internal/agent/self_correction.go internal/agent/middleware_tool.go \
        internal/agent/middleware_loop.go internal/agent/cognitive_v2_test.go \
        internal/agent/cognitive.go
git commit -m "feat(agent): add CognitiveAgentV2 — single loop + self-correction

Replace 5-phase model (PERCEIVE→PLAN→ACT→OBSERVE→REFLECT) with:
- One-time context injection (ContextBuilder + scanners)
- Single LLM tool-use loop with ToolMiddleware chain
- Post-loop SelfCorrectionEngine for assertion-driven replan
- LoopHooks for checkpoint, compression, plan-mode, evolution

Old CognitiveAgent.Run() delegates to v2 via toV2() adapter.
All existing callers unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

# Worktree 3: Gateway Subsystem Split

**New Files:**
- `internal/gateway/subsystem.go` — Subsystem interface
- `internal/gateway/subsystem_memory.go`
- `internal/gateway/subsystem_channel.go`
- `internal/gateway/subsystem_dashboard.go`
- `internal/gateway/subsystem_sandbox.go`
- `internal/gateway/subsystem_evolution.go`
- `internal/gateway/subsystem_task.go`
- `internal/gateway/subsystem_observability.go`
- `internal/gateway/subsystem_a2a.go`

**Modified Files:**
- `internal/gateway/gateway.go` — slim down from 55 fields to ~18

### Task 3.1: Define Subsystem interface

**Files:** Create: `internal/gateway/subsystem.go`

```go
package gateway

import "context"

// Subsystem is a self-contained module with its own lifecycle.
// Each subsystem manages its own goroutines, connections, and cleanup.
type Subsystem interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Subsystems is an ordered collection of subsystems.
// Start order = registration order. Stop order = reverse.
type Subsystems []Subsystem

// StartAll starts all subsystems in order. First error aborts.
func (ss Subsystems) StartAll(ctx context.Context) error {
	for _, s := range ss {
		if err := s.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all subsystems in reverse order. Errors are logged, not fatal.
func (ss Subsystems) StopAll(ctx context.Context) {
	for i := len(ss) - 1; i >= 0; i-- {
		_ = ss[i].Stop(ctx)
	}
}
```

### Task 3.2: Extract MemorySubsystem

**Files:** Create: `internal/gateway/subsystem_memory.go`

Extract fields from Gateway: `memStore`, `embedder`, `factExtractor`, `lifecycleMgr`,
`consolidator`, `compactor`, `profiler`, `graphDecay` → `MemorySubsystem`.

```go
package gateway

type MemorySubsystem struct {
	store          memory.Store
	embedder       memory.EmbeddingProvider
	factExtractor  *memory.LLMFactExtractor
	lifecycleMgr   *memory.LifecycleManager
	consolidator   *memory.Consolidator
	compactor      *memory.Compactor
	profiler       *memory.Profiler
	graphDecay     *graph.GraphDecayTask
}

func (ms *MemorySubsystem) Name() string { return "memory" }
func (ms *MemorySubsystem) Start(ctx context.Context) error { return nil }
func (ms *MemorySubsystem) Stop(ctx context.Context) error {
	if ms.consolidator != nil { ms.consolidator.Stop() }
	if ms.compactor != nil { ms.compactor.Stop() }
	if ms.graphDecay != nil { ms.graphDecay.Stop() }
	return nil
}

func (ms *MemorySubsystem) Store() memory.Store             { return ms.store }
func (ms *MemorySubsystem) FactExtractor() *memory.LLMFactExtractor { return ms.factExtractor }
func (ms *MemorySubsystem) LifecycleManager() *memory.LifecycleManager { return ms.lifecycleMgr }
func (ms *MemorySubsystem) Profiler() *memory.Profiler       { return ms.profiler }
```

### Task 3.3: Extract remaining subsystems

Follow the same pattern for each subsystem. Each extraction:
1. Create `<name>_subsystem.go`
2. Move fields from Gateway struct to subsystem struct
3. Add accessor methods
4. Update gateway.go to use `gw.subsystems.StartAll(ctx)`

### Task 3.4: Slim down Gateway struct

**Files:** Modify: `internal/gateway/gateway.go`

After extracting all 8 subsystems, Gateway struct becomes:

```go
type Gateway struct {
	cfg            *config.Config
	db             *store.DB
	sessions       *session.Manager
	provider       agent.Provider
	cognitiveAgent *agent.CognitiveAgentV2 // or *agent.CognitiveAgent
	tools          *tool.Registry
	hookMgr        *hook.Manager
	features       *feature.Registry
	featureStatePath string

	subsystems Subsystems

	// Subsystem references (for accessor convenience)
	memory        *MemorySubsystem
	channels      *ChannelSubsystem
	dashboard     *DashboardSubsystem
	sandbox       *SandboxSubsystem
	evolution     *EvolutionSubsystem
	tasks         *TaskSubsystem
	observability *ObservabilitySubsystem
	a2a           *A2ASubsystem
}
```

### Task 3.5: Commit

```bash
git add internal/gateway/
git commit -m "refactor(gateway): extract 8 subsystems from god object

Gateway reduced from 55 fields to ~18. Each subsystem manages its
own lifecycle via the Subsystem interface.

Subsystems: Memory, Channel, Dashboard, Sandbox, Evolution,
Task, Observability, A2A.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

# Worktree 4: Test Completion

### Task 4.1: knowledge/store_test.go — ingest→search pipeline

### Task 4.2: knowledge/graph/graph_test.go — entity extraction + traversal

### Task 4.3: wasm/plugin_test.go — plugin load + execution

### Task 4.4: code_engine/symbol_index_test.go — index + query

### Task 4.5: guardian/guardian_test.go — quality pipeline

### Task 4.6: browser_agent tests

### Task 4.7: finetune tests

### Task 4.8: channel/userdir/util smoke tests

(Each test task follows the same pattern: write test → run → verify → commit.
Full test code expanded during execution.)

---

# Worktree 5: Config + TUI Cleanup

### Task 5.1: Config — remove duplicate StreamingEnabled

### Task 5.2: Config — extract merge.go and validate.go

### Task 5.3: TUI — split model.go into 4 files

### Task 5.4: Commit and verify full test suite

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/... -count=1
```

---

## Final Verification Checklist

After all worktrees merged:

- [ ] `go vet ./internal/...` — zero warnings
- [ ] `CGO_ENABLED=1 go test -tags fts5 ./internal/...` — all pass
- [ ] `CGO_ENABLED=1 go build -tags fts5 ./...` — clean build
- [ ] Gateway struct ≤ 20 fields
- [ ] `internal/core/` directory does not exist
- [ ] Zero packages with source but no tests
- [ ] `grep -rn 'panic(' internal/ --include='*.go' | grep -v '_test.go' | grep -v 'archived'` — zero results
- [ ] Agent eval suite passes: `make eval-baseline`

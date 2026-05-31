# IronClaw Phase 1: 止血 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate Setter Hell (AgentDeps DI), extract command routing, add no-op implementations, and add init rollback to Gateway.New().

**Architecture:** Five sub-struct AgentDeps replaces 30+ Set* methods. CommandRouter map replaces 170-line handleInbound. No-op implementations eliminate nil checks. rollbackStack ensures init failures clean up resources.

**Tech Stack:** Go 1.25, no new dependencies.

---

## File Structure Map

```
internal/agent/
  deps.go              NEW — AgentDeps + 5 sub-structs + WithDefaults() methods
  noop.go              NEW — discardEmitter, discardMetrics, noopContextManager
  runtime.go           MODIFY — delete Set*, change NewRuntime(deps AgentDeps), use deps.*
  cognitive.go         MODIFY — delete Set*, change NewCognitiveAgent(deps AgentDeps, opts)
  subagent.go          MODIFY — change NewSubAgentManager(deps AgentDeps)
  perceive.go          MODIFY — r.memStore → r.deps.Memory.Store
  reflect.go           MODIFY — r.memStore → r.deps.Memory.Store
  act.go               MODIFY — r.interceptorChain → r.deps.Security.Interceptor
  stream.go            MODIFY — r.dashEmitter → r.deps.Observability.Emitter
  compression.go       MODIFY — r.contextManager → r.deps.Memory.ContextMgr

internal/memory/
  noop.go              NEW — noopStore

internal/knowledge/
  noop.go              NEW — noopSearcher

internal/tool/
  noop_interceptor.go  NEW — passthroughChain (Interceptor interface)

internal/taskledger/
  noop.go              NEW — noopTaskLedger

internal/gateway/
  rollback.go          NEW — rollbackStack
  router.go            NEW — commandTable + dispatch logic
  commands.go          NEW — extracted slash command handlers (methods on *Gateway)
  gateway.go           MODIFY — New() uses rollbackStack, handleInbound → router.Dispatch
  init_agent.go        MODIFY — construct AgentDeps, pass to NewRuntime
  init_cognitive.go    MODIFY — pass AgentDeps to NewCognitiveAgent
  init_multiagent.go   MODIFY — pass AgentDeps to NewSubAgentManager
  gateway_test.go      MODIFY — adapt test deps construction

internal/eval/
  cognitive_runner.go  MODIFY — adapt Agent construction
```

---

## Workstream 1: Foundation — No-op Implementations + AgentDeps Struct

These files have zero dependencies on other changes. Build them first.

### Task 1: Create noopEmitter, noopContextManager, discardMetrics in agent/noop.go

**Files:**
- Create: `internal/agent/noop.go`

- [ ] **Step 1: Write noopEmitter and discardMetrics**

```go
package agent

// discardEmitter is the zero-value DashboardEmitter. All methods are no-ops.
// Used when no dashboard or TUI emitter is configured.
type discardEmitter struct{}

func (discardEmitter) EmitPhaseStart(string, string)                                    {}
func (discardEmitter) EmitPhaseEnd(string, string, int64)                                {}
func (discardEmitter) EmitToolStart(string, string, string)                              {}
func (discardEmitter) EmitToolEnd(string, string, bool, int64)                           {}
func (discardEmitter) EmitSessionStart(string, string)                                   {}
func (discardEmitter) EmitSessionEnd(string, bool, int64)                                {}
func (discardEmitter) EmitMetricsUpdate(string, int, int, float64, int64, int64, int64, int64, string, string) {}
func (discardEmitter) EmitPlanGenerated(string, int, string, bool)                       {}
func (discardEmitter) EmitReplanStart(string, int, string)                               {}
func (discardEmitter) EmitObservationResult(string, int, int, int, float64)              {}
func (discardEmitter) EmitSubAgentSpawn(string, string, string, string)                  {}
func (discardEmitter) EmitSubAgentComplete(string, string, bool, int64)                  {}
func (discardEmitter) EmitContextCompress(string, string, int, float64, float64)         {}

// discardMetrics is the zero-value MetricsEmitter. All methods are no-ops.
type discardMetrics struct{}

func (discardMetrics) SendMetrics(RuntimeMetrics) {}
```

- [ ] **Step 2: Write noopContextManager**

```go
// noopContextManager implements ContextManager with no compression.
type noopContextManager struct{}

func (noopContextManager) Compress(_ context.Context, _ *session.Session, _ string) (bool, error) {
	return false, nil
}
func (noopContextManager) ReactiveCompress(_ context.Context, _ *session.Session, _ string) error {
	return nil
}
func (noopContextManager) Utilization(_ *session.Session, _ string) float64 { return 0 }
func (noopContextManager) SplitSystemPrompt(full string) (string, string)   { return full, "" }
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/`
Expected: no errors (new file with no callers compiles cleanly)

- [ ] **Step 4: Commit**

```bash
git add internal/agent/noop.go
git commit -m "feat(agent): add discardEmitter, discardMetrics, noopContextManager no-op implementations

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 2: Create noopStore in memory/noop.go

**Files:**
- Create: `internal/memory/noop.go`

- [ ] **Step 1: Write noopStore**

```go
package memory

import "context"

// noopStore implements Store with all no-op methods.
// Used when memory feature is disabled.
type noopStore struct{}

func (noopStore) Save(_ context.Context, _ Entry) error                          { return nil }
func (noopStore) Search(_ context.Context, _ SearchQuery) ([]SearchResult, error) { return nil, nil }
func (noopStore) ListByScope(_ context.Context, _ MemoryScope, _ string) ([]Entry, error) {
	return nil, nil
}
func (noopStore) Update(_ context.Context, _ string, _ string, _ int) error { return nil }
func (noopStore) Delete(_ context.Context, _ string) error                  { return nil }

// Compile-time check
var _ Store = noopStore{}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/memory/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/memory/noop.go
git commit -m "feat(memory): add noopStore no-op implementation

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 3: Create noopSearcher in knowledge/noop.go

**Files:**
- Create: `internal/knowledge/noop.go`

- [ ] **Step 1: Write noopSearcher**

```go
package knowledge

import "context"

// noopSearcher implements Searcher with a no-op Search method.
// Used when the knowledge feature is disabled.
type noopSearcher struct{}

func (noopSearcher) Search(_ context.Context, _ KnowledgeQuery) ([]KnowledgeResult, error) {
	return nil, nil
}

var _ Searcher = noopSearcher{}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/knowledge/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/knowledge/noop.go
git commit -m "feat(knowledge): add noopSearcher no-op implementation

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 4: Create passthroughChain in tool/noop_interceptor.go

**Files:**
- Create: `internal/tool/noop_interceptor.go`

- [ ] **Step 1: Write passthroughChain**

```go
package tool

import "context"

// passthroughChain implements a no-op interceptor that calls the final handler directly.
// Used when no interceptor chain is configured (sandbox disabled, no hooks).
type passthroughChain struct{}

func (passthroughChain) Execute(ctx context.Context, call *ToolCall, final InterceptorFunc) (*ToolResult, error) {
	return final(ctx, call)
}

// PassthroughInterceptor returns an interceptor that does nothing.
func PassthroughInterceptor() *InterceptorChain {
	return NewInterceptorChain(nil)
}
```

Wait — `passthroughChain` doesn't implement `InterceptorChain`. Looking at the Runtime, it stores `*tool.InterceptorChain` and calls `r.interceptorChain.Execute(...)`. So `InterceptorChain` is the type, not an interface. We can't make a no-op for `*InterceptorChain` directly.

Instead, the `*InterceptorChain.Execute` already handles `len(c.interceptors) == 0` (line 58 of interceptor.go) — it just calls `final` directly. So an empty `InterceptorChain` IS the no-op.

In SecurityDeps, we should use `*tool.InterceptorChain` directly (not an interface), and the WithDefaults() just leaves it nil. The call sites check `if r.interceptorChain != nil` — actually, since InterceptorChain.Execute already handles nil internally... let me check.

Looking at interceptor.go line 49-74: `Execute` is a method on `*InterceptorChain`, so calling it on nil would panic. We need to nil-check at call sites.

Let me reconsider: for `*tool.InterceptorChain`, the WithDefaults() creates an empty `&tool.InterceptorChain{}` (no interceptors), which means Execute runs final directly. This is the no-op.

```go
package tool

import "context"

// NewPassthroughChain returns an InterceptorChain with no interceptors.
// When Execute() is called, it runs the final handler directly with zero overhead.
func NewPassthroughChain() *InterceptorChain {
	return NewInterceptorChain(nil)
}
```

Actually wait, this belongs in `interceptor.go` since it uses `NewInterceptorChain`. Let me put it there instead. Or just inline `&tool.InterceptorChain{}` in the deps WithDefaults().

Actually, let me just keep it simple. In SecurityDeps.WithDefaults(), we do:
```go
if d.Interceptor == nil {
    d.Interceptor = tool.NewInterceptorChain(nil) // empty chain = passthrough
}
```

No new file needed in tool/. The `NewInterceptorChain` already exists.

- [ ] **Step 1: No file needed — delete this task**

The empty `InterceptorChain` already works as a passthrough. In SecurityDeps.WithDefaults() we'll use `tool.NewInterceptorChain(nil)`.

### Task 5: Create noopTaskLedger in taskledger/noop.go

**Files:**
- Create: `internal/taskledger/noop.go`

- [ ] **Step 1: Write noopTaskLedger**

```go
package taskledger

import (
	"context"
	"time"
)

// noopTaskLedger implements TaskLedger with all no-op methods.
type noopTaskLedger struct{}

func (noopTaskLedger) Register(_ context.Context, _ Task) error                    { return nil }
func (noopTaskLedger) Get(_ context.Context, _ string) (*Task, error)              { return nil, nil }
func (noopTaskLedger) Update(_ context.Context, _ Task) error                      { return nil }
func (noopTaskLedger) List(_ context.Context, _ TaskFilter) ([]Task, error)        { return nil, nil }
func (noopTaskLedger) Cancel(_ context.Context, _ string, _ string) error          { return nil }
func (noopTaskLedger) ClaimNext(_ context.Context, _ TaskKind, _ string) (*Task, error) {
	return nil, nil
}
func (noopTaskLedger) Heartbeat(_ context.Context, _ string) error                 { return nil }
func (noopTaskLedger) GetTree(_ context.Context, _ string) ([]Task, error)         { return nil, nil }
func (noopTaskLedger) DetectStale(_ context.Context, _ time.Duration) ([]Task, error) {
	return nil, nil
}

var _ TaskLedger = noopTaskLedger{}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/taskledger/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/taskledger/noop.go
git commit -m "feat(taskledger): add noopTaskLedger no-op implementation

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 6: Create AgentDeps struct in agent/deps.go

**Files:**
- Create: `internal/agent/deps.go`

- [ ] **Step 1: Write AgentDeps with all sub-structs and WithDefaults()**

```go
package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ────────────────────────────── CoreDeps ──────────────────────────────

// CoreDeps holds the required dependencies to run an agent. Every field is mandatory.
type CoreDeps struct {
	Provider   Provider
	Tools      *tool.Registry
	Sessions   *session.Manager
	DB         *store.DB
	Cfg        config.AgentConfig
	LLMCfg     config.LLMConfig
	AgentID    string
}

// ────────────────────────────── MemoryDeps ──────────────────────────────

// MemoryDeps holds optional memory subsystem dependencies. Call WithDefaults()
// to fill nil interface fields with no-op implementations.
type MemoryDeps struct {
	Store         memory.Store               // default: memory.NoopStore()
	LifecycleMgr  *memory.LifecycleManager   // nil = NOOP lifecycle decisions
	Profiler      *memory.Profiler           // nil = no profile updates
	ContextMgr    ContextManager             // default: noopContextManager{}
	FactExtractor *memory.LLMFactExtractor   // nil = no extraction
	BaseDir       string                     // base directory for file-based memory storage
}

// WithDefaults returns a copy of MemoryDeps with nil interface fields filled.
func (d MemoryDeps) WithDefaults() MemoryDeps {
	if d.Store == nil {
		d.Store = memory.NoopStore()
	}
	if d.ContextMgr == nil {
		d.ContextMgr = noopContextManager{}
	}
	return d
}

// ────────────────────────────── SecurityDeps ──────────────────────────────

// SecurityDeps holds optional security subsystem dependencies.
type SecurityDeps struct {
	Interceptor *tool.InterceptorChain  // nil = passthrough (tools execute directly)
	HookMgr     *hook.Manager           // nil = no hooks
	PermEngine  *tool.PermissionEngine  // nil = allow-all
}

// WithDefaults returns a copy of SecurityDeps with nil fields filled.
func (d SecurityDeps) WithDefaults() SecurityDeps {
	if d.Interceptor == nil {
		d.Interceptor = tool.NewInterceptorChain(nil) // empty chain = direct execution
	}
	return d
}

// ────────────────────────────── ObservabilityDeps ──────────────────────────────

// ObservabilityDeps holds optional observability subsystem dependencies.
type ObservabilityDeps struct {
	Emitter        DashboardEmitter // default: discardEmitter{}
	MetricsEmitter MetricsEmitter   // default: discardMetrics{}
	ReplayRecorder *ReplayRecorder  // nil = no recording
}

// WithDefaults returns a copy of ObservabilityDeps with nil interface fields filled.
func (d ObservabilityDeps) WithDefaults() ObservabilityDeps {
	if d.Emitter == nil {
		d.Emitter = discardEmitter{}
	}
	if d.MetricsEmitter == nil {
		d.MetricsEmitter = discardMetrics{}
	}
	return d
}

// ────────────────────────────── MultiAgentDeps ──────────────────────────────

// MultiAgentDeps holds optional multi-agent subsystem dependencies.
// Nil fields mean the feature is disabled.
type MultiAgentDeps struct {
	SkillMgr       *skill.Manager
	AgentMgr       *AgentManager
	Orchestrator   *AgentOrchestrator
	SubAgentMgr    *SubAgentManager  // nil = no sub-agents
	AgentMCP       *AgentMCPManager
	ResultStore    *tool.ResultStore
	TaskLedger     taskledger.TaskLedger       // default: taskledger.NoopTaskLedger()
	Speculative    *SpeculativeExecutor        // nil = disabled
	PromptCache    *PromptCache                // nil = disabled
	BgManager      *BackgroundManager          // nil = disabled
}

// WithDefaults returns a copy of MultiAgentDeps with nil interface fields filled.
func (d MultiAgentDeps) WithDefaults() MultiAgentDeps {
	if d.TaskLedger == nil {
		d.TaskLedger = taskledger.NoopTaskLedger()
	}
	return d
}

// ────────────────────────────── AgentDeps ──────────────────────────────

// AgentDeps is the complete dependency bundle for all agent types (Runtime,
// CognitiveAgent, SubAgentManager). Construct once in Gateway.New() and share.
type AgentDeps struct {
	Core          CoreDeps
	Memory        MemoryDeps
	Security      SecurityDeps
	Observability ObservabilityDeps
	MultiAgent    MultiAgentDeps
}

// WithDefaults calls WithDefaults() on each sub-struct, filling nil interfaces with no-ops.
func (d AgentDeps) WithDefaults() AgentDeps {
	d.Memory = d.Memory.WithDefaults()
	d.Security = d.Security.WithDefaults()
	d.Observability = d.Observability.WithDefaults()
	d.MultiAgent = d.MultiAgent.WithDefaults()
	return d
}
```

- [ ] **Step 2: Add exported no-op constructors in the respective noop files**

Add to `internal/memory/noop.go`:
```go
// NoopStore returns a Store that discards all operations.
func NoopStore() Store { return noopStore{} }
```

Add to `internal/taskledger/noop.go`:
```go
// NoopTaskLedger returns a TaskLedger that discards all operations.
func NoopTaskLedger() TaskLedger { return noopTaskLedger{} }
```

Add to `internal/knowledge/noop.go`:
```go
// NoopSearcher returns a Searcher that returns empty results.
func NoopSearcher() Searcher { return noopSearcher{} }
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/agent/deps.go internal/memory/noop.go internal/taskledger/noop.go internal/knowledge/noop.go
git commit -m "feat(agent): add AgentDeps with 5 grouped sub-structs and WithDefaults()

CoreDeps, MemoryDeps, SecurityDeps, ObservabilityDeps, MultiAgentDeps.
Each sub-struct has WithDefaults() to fill nil interfaces with no-ops.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Workstream 2: Runtime Migration

### Task 7: Refactor Runtime struct and NewRuntime

**Files:**
- Modify: `internal/agent/runtime.go`

- [ ] **Step 1: Change Runtime struct to use AgentDeps**

Replace the current Runtime struct (lines 29-68) with:

```go
// Runtime orchestrates the agent loop: context → LLM → tools → reply.
type Runtime struct {
	deps AgentDeps

	// Runtime identity (set externally after construction)
	parentID string // parent agent ID (empty for top-level)
	depth    int    // nesting depth
	chainID  string // invocation chain ID

	// Transient state
	replayID       string
	approvalFunc   ApprovalFunc
}
```

- [ ] **Step 2: Change NewRuntime signature**

Replace lines 210-226:

```go
func NewRuntime(deps AgentDeps) *Runtime {
	return &Runtime{
		deps: deps,
	}
}
```

- [ ] **Step 3: Delete all Set* methods (lines 70-183)**

Delete these methods entirely:
- SetMemoryStore, SetFactExtractor, SetLifecycleManager, SetProfiler
- SetMemoryBaseDir, SetSkillManager, SetAgentManager, SetOrchestrator
- SetCompressor, SetConcurrentConfig, SetResultStore
- SetCompressionPipeline, SetTokenBudget, SetHookManager
- SetPermissionEngine, SetAgentID, SetParentID, SetDepth
- SetChainID, SetBackgroundManager, SetPromptCache
- SetAgentMCPManager, SetContextManager, SetSpeculativeExecutor
- SetTaskLedger, SetInterceptorChain, SetDashboardEmitter
- SetMetricsEmitter, SetReplayRecorder, SetSelfHealEngine

Keep: SetApprovalFunc, SetModel, GetTools, GetMessages, GetSystemPrompt, AgentID, ParentID, Depth, ChainID (these are used by external callers with non-deps semantics).

- [ ] **Step 4: Update SetModel to use deps**

```go
func (r *Runtime) SetModel(model string) { r.deps.Core.LLMCfg.Model = model }
```

- [ ] **Step 5: Update GetTools to use deps**

```go
func (r *Runtime) GetTools() *tool.Registry { return r.deps.Core.Tools }
```

- [ ] **Step 6: Update AgentID, ParentID, Depth, ChainID methods**

```go
func (r *Runtime) AgentID() string  { return r.deps.Core.AgentID }
func (r *Runtime) ParentID() string { return r.parentID }
func (r *Runtime) Depth() int       { return r.depth }
func (r *Runtime) ChainID() string  { return r.chainID }

// SetParentID, SetDepth, SetChainID — kept for sub-agent setup
func (r *Runtime) SetParentID(id string)  { r.parentID = id }
func (r *Runtime) SetDepth(d int)          { r.depth = d }
func (r *Runtime) SetChainID(id string)    { r.chainID = id }
```

- [ ] **Step 7: Batch-replace all field accesses in runtime.go**

Replace ALL occurrences using these mapping rules:
```
r.provider          → r.deps.Core.Provider
r.tools             → r.deps.Core.Tools
r.sessions          → r.deps.Core.Sessions
r.db                → r.deps.Core.DB
r.cfg               → r.deps.Core.Cfg
r.llmCfg            → r.deps.Core.LLMCfg
r.memStore          → r.deps.Memory.Store
r.skillMgr          → r.deps.MultiAgent.SkillMgr
r.agentMgr          → r.deps.MultiAgent.AgentMgr
r.orchestrator      → r.deps.MultiAgent.Orchestrator
r.resultStore       → r.deps.MultiAgent.ResultStore
r.agentMCP          → r.deps.MultiAgent.AgentMCP
r.bgManager         → r.deps.MultiAgent.BgManager
r.promptCache       → r.deps.MultiAgent.PromptCache
r.taskLedger        → r.deps.MultiAgent.TaskLedger
r.speculativeExecutor → r.deps.MultiAgent.Speculative
r.hookMgr           → r.deps.Security.HookMgr
r.permEngine        → r.deps.Security.PermEngine
r.interceptorChain  → r.deps.Security.Interceptor
r.dashEmitter       → r.deps.Observability.Emitter
r.metricsEmitter    → r.deps.Observability.MetricsEmitter
r.replayRecorder    → r.deps.Observability.ReplayRecorder
r.contextManager    → r.deps.Memory.ContextMgr
r.factExtractor     → r.deps.Memory.FactExtractor
r.lifecycleMgr      → r.deps.Memory.LifecycleMgr
r.profiler          → r.deps.Memory.Profiler
r.memoryBaseDir     → r.deps.Memory.BaseDir
r.tokenBudget       → (removed — was only used via contextManager fallback)
r.compressor        → (removed — unused after contextManager migration)
r.compressionPipeline → (removed — unused after contextManager migration)
r.concurrentCfg     → (removed — unused)
r.selfHealEngine    → (removed — unused)
```

- [ ] **Step 8: Remove nil checks on interface fields**

Now that Memory.Store, Memory.ContextMgr, Observability.Emitter, Observability.MetricsEmitter, Security.Interceptor, MultiAgent.TaskLedger are never nil, remove ALL nil-guards around them.

For POINTER fields that CAN be nil (`Memory.LifecycleMgr`, `Memory.Profiler`, `Memory.FactExtractor`, `MultiAgent.SkillMgr`, `MultiAgent.AgentMgr`, etc.), keep existing nil checks.

- [ ] **Step 9: Clean up imports**

Remove unused imports: `config`, `hook`, `memory`, `skill`, `store` (some may still be needed by helper methods — check after bulk replace).

- [ ] **Step 10: Verify compilation (will FAIL — expected)**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/ 2>&1 | head -30`
Expected: errors from other files (perceive.go, reflect.go, act.go, stream.go, compression.go) that still reference old field names. This is expected — we fix them in Task 8.

---

### Task 8: Update agent sub-packages for deps migration

**Files:**
- Modify: `internal/agent/perceive.go`
- Modify: `internal/agent/reflect.go`
- Modify: `internal/agent/act.go`
- Modify: `internal/agent/stream.go`
- Modify: `internal/agent/compression.go`
- Modify: `internal/agent/*.go` (any remaining files with r.xxx field access)

- [ ] **Step 1: Update perceive.go**

Replace all `r.memStore` → `r.deps.Memory.Store`, `r.memoryBaseDir` → `r.deps.Memory.BaseDir`, `r.skillMgr` → `r.deps.MultiAgent.SkillMgr`, `r.agentMgr` → `r.deps.MultiAgent.AgentMgr`, `r.profiler` → `r.deps.Memory.Profiler`.
Remove nil checks on interface fields, keep nil checks on pointer fields.

- [ ] **Step 2: Update reflect.go**

Replace `r.memStore` → `r.deps.Memory.Store`, `r.factExtractor` → `r.deps.Memory.FactExtractor`, `r.lifecycleMgr` → `r.deps.Memory.LifecycleMgr`, `r.profiler` → `r.deps.Memory.Profiler`.

- [ ] **Step 3: Update act.go**

Replace `r.interceptorChain` → `r.deps.Security.Interceptor`, `r.dashEmitter` → `r.deps.Observability.Emitter`, `r.hookMgr` → `r.deps.Security.HookMgr`, `r.permEngine` → `r.deps.Security.PermEngine`, `r.speculativeExecutor` → `r.deps.MultiAgent.Speculative`, `r.resultStore` → `r.deps.MultiAgent.ResultStore`, `r.bgManager` → `r.deps.MultiAgent.BgManager`.

- [ ] **Step 4: Update stream.go**

Replace `r.dashEmitter` → `r.deps.Observability.Emitter`, `r.metricsEmitter` → `r.deps.Observability.MetricsEmitter`, `r.replayRecorder` → `r.deps.Observability.ReplayRecorder`, `r.contextManager` → `r.deps.Memory.ContextMgr`.

- [ ] **Step 5: Update compression.go**

Replace `r.contextManager` → `r.deps.Memory.ContextMgr`.

- [ ] **Step 6: Scan for remaining old field references**

Run: `cd /Users/wuqisen/dev/IronClaw && grep -rn 'r\.\(provider\|tools\|sessions\|db\|cfg\|llmCfg\|memStore\|skillMgr\|agentMgr\|orchestrator\|resultStore\|agentMCP\|bgManager\|promptCache\|taskLedger\|speculativeExecutor\|hookMgr\|permEngine\|interceptorChain\|dashEmitter\|metricsEmitter\|replayRecorder\|contextManager\|factExtractor\|lifecycleMgr\|profiler\|memoryBaseDir\|tokenBudget\|compressor\|compressionPipeline\|concurrentCfg\|selfHealEngine\)' internal/agent/ | grep -v '_test.go' | grep -v 'deps\.'`
Expected: no output (all old references migrated)

- [ ] **Step 7: Verify compilation of agent package**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/ 2>&1`
Expected: no errors (or only errors from other packages that call Set* methods — those are fixed in Workstream 4)

- [ ] **Step 8: Commit**

```bash
git add internal/agent/
git commit -m "refactor(agent): migrate Runtime to AgentDeps, delete 30 Set* methods

All Runtime fields now accessed through deps.Core/Memory/Security/Observability/MultiAgent.
No-op defaults eliminate nil checks on interface fields.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Workstream 3: CognitiveAgent + SubAgentManager Migration

### Task 9: Refactor CognitiveAgent to accept AgentDeps

**Files:**
- Modify: `internal/agent/cognitive.go`

- [ ] **Step 1: Change CognitiveAgent struct**

Replace the current struct (lines 47-82) with:

```go
type CognitiveAgent struct {
	deps AgentDeps

	// Phase components
	perceiver        *Perceiver
	planner          *Planner
	executor         *Executor
	observer         *Observer
	reflector        *Reflector

	// Optional subsystems (nil = disabled)
	debateCfg        config.DebateSettings
	entityExtractor  *graph.LLMEntityExtractor
	rlPolicy         RLPolicy
	rlTrainer        RLTrainer
	evoEngine        *evolution.Engine
	checkpointStore  CheckpointStore
	planMode         *PlanMode
	treePlanner      *StrategicTreePlanner
	mctsPlanner      *MCTSPlanner
	codebaseIndex    *CodebaseIndex
	cortex           *cortex.UnifiedRetriever

	// Transient
	observationCallback func(result *ObservationResult)
}
```

Note: `sessions`, `db`, `cfg`, `llmCfg`, `memStore`, `skillMgr`, `agentMgr`, `orchestrator`, `teamManager`, `hookMgr`, `permEngine`, `contextManager`, `taskLedger`, `dashEmitter`, `replayRecorder`, `selfHealEngine` are REMOVED — they come from `deps` now.

- [ ] **Step 2: Change NewCognitiveAgent**

```go
func NewCognitiveAgent(
	deps AgentDeps,
	opts *CognitiveAgentOptions,
) *CognitiveAgent {
	ca := &CognitiveAgent{
		deps: deps,
	}

	cogCfg := deps.Core.Cfg.Cognitive

	// Build phase components using deps
	ca.perceiver = NewPerceiver(deps.Memory.Store, deps.Memory.BaseDir)
	scanner := NewProjectContextScanner()
	ca.perceiver.SetProjectScanner(scanner)
	gitProvider := NewGitContextProvider()
	ca.perceiver.SetGitProvider(gitProvider)
	budgetAlloc := NewContextBudgetAllocator()
	ca.perceiver.SetBudgetAllocator(budgetAlloc)
	ca.planner = NewPlanner(deps.Core.Provider, deps.Core.Tools, cogCfg, deps.Core.LLMCfg.Model)
	ca.executor = NewExecutor(deps.Core.Provider, deps.Core.Tools, deps.Security.Interceptor, deps.Core.Cfg.Concurrent)
	ca.observer = NewObserver(deps.Core.Provider, deps.Core.Tools, deps.Core.LLMCfg.Model)
	ca.reflector = NewReflector(deps.Core.Provider, deps.Core.LLMCfg.Model, deps.Memory.Store, deps.Memory.BaseDir, deps.Memory.FactExtractor, deps.Memory.LifecycleMgr, deps.Memory.Profiler)

	// Wire optional subsystems from opts
	if opts != nil {
		ca.entityExtractor = opts.EntityExtractor
		ca.rlPolicy = opts.RLPolicy
		ca.rlTrainer = opts.RLTrainer
		ca.evoEngine = opts.EvolutionEngine
		ca.checkpointStore = opts.CheckpointStore
		ca.planMode = opts.PlanMode
		ca.treePlanner = opts.TreePlanner
		ca.mctsPlanner = opts.MCTSPlanner
		ca.codebaseIndex = opts.CodebaseIndex
		ca.cortex = opts.CortexRetriever
		ca.debateCfg = opts.DebateConfig
		ca.observationCallback = opts.ObservationCallback
	}

	return ca
}
```

- [ ] **Step 3: Delete all Set* methods**

Delete: SetMemoryStore, SetFactExtractor, SetLifecycleManager, SetProfiler, SetMemoryBaseDir, SetSkillManager, SetAgentManager, SetOrchestrator, SetTeamManager, SetHookManager, SetPermissionEngine, SetInterceptorChain, SetContextManager, SetCompressionPipeline, SetTaskLedger, SetDashboardEmitter, SetReplayRecorder, SetSelfHealEngine, SetTreePlanner, SetMCTSPlanner, SetCodebaseIndex, SetCortexRetriever, SetApprovalFunc, SetPlanMode, SetObservationCallback.

- [ ] **Step 4: Add accessor methods that external callers need**

Keep/add:
```go
func (ca *CognitiveAgent) SetApprovalFunc(fn ApprovalFunc) { /* stored on internal runtime or deps */ }
func (ca *CognitiveAgent) SetPlanMode(pm *PlanMode) { ca.planMode = pm }
func (ca *CognitiveAgent) SetObservationCallback(fn func(*ObservationResult)) { ca.observationCallback = fn }
func (ca *CognitiveAgent) LLMProvider() Provider { return ca.deps.Core.Provider }
```

- [ ] **Step 5: Fix internal field accesses**

Batch replace in cognitive.go and all phase files:
```
ca.memStore        → ca.deps.Memory.Store
ca.skillMgr        → ca.deps.MultiAgent.SkillMgr
ca.agentMgr        → ca.deps.MultiAgent.AgentMgr
ca.orchestrator    → ca.deps.MultiAgent.Orchestrator
ca.hookMgr         → ca.deps.Security.HookMgr
ca.permEngine      → ca.deps.Security.PermEngine
ca.contextManager  → ca.deps.Memory.ContextMgr
ca.taskLedger      → ca.deps.MultiAgent.TaskLedger
ca.dashEmitter     → ca.deps.Observability.Emitter
ca.replayRecorder  → ca.deps.Observability.ReplayRecorder
ca.selfHealEngine  → (removed — unused)
ca.sessions        → ca.deps.Core.Sessions
ca.db              → ca.deps.Core.DB
ca.cfg             → ca.deps.Core.Cfg
ca.llmCfg          → ca.deps.Core.LLMCfg
```

- [ ] **Step 6: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/ 2>&1 | head -30`
Expected: errors only from subagent.go (not yet migrated) and external packages (gateway, eval)

- [ ] **Step 7: Commit**

```bash
git add internal/agent/cognitive.go
git commit -m "refactor(agent): migrate CognitiveAgent to AgentDeps, delete 20+ Set* methods

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 10: Refactor SubAgentManager to accept AgentDeps

**Files:**
- Modify: `internal/agent/subagent.go`

- [ ] **Step 1: Change SubAgentManager struct**

Replace lines 29-41:

```go
type SubAgentManager struct {
	deps       AgentDeps
}
```

- [ ] **Step 2: Change NewSubAgentManager**

```go
func NewSubAgentManager(deps AgentDeps) *SubAgentManager {
	return &SubAgentManager{deps: deps}
}
```

- [ ] **Step 3: Delete Set* methods**

Delete: SetBackgroundManager, SetAgentMCPManager, SetDashboardEmitter.

- [ ] **Step 4: Fix internal field accesses**

Batch replace in subagent.go:
```
m.provider    → m.deps.Core.Provider
m.sessions    → m.deps.Core.Sessions
m.db          → m.deps.Core.DB
m.memStore    → m.deps.Memory.Store
m.tools       → m.deps.Core.Tools
m.cfg         → m.deps.Core.Cfg
m.llmCfg      → m.deps.Core.LLMCfg
m.bgManager   → m.deps.MultiAgent.BgManager
m.agentMCP    → m.deps.MultiAgent.AgentMCP
m.dashEmitter → m.deps.Observability.Emitter
```

- [ ] **Step 5: Verify agent package compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/agent/ 2>&1`
Expected: no errors in agent package (errors from gateway/eval expected)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/subagent.go
git commit -m "refactor(agent): migrate SubAgentManager to AgentDeps

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Workstream 4: Gateway — Command Router, Init Rollback, AgentDeps Wiring

### Task 11: Create rollbackStack in gateway/rollback.go

**Files:**
- Create: `internal/gateway/rollback.go`

- [ ] **Step 1: Write rollbackStack**

```go
package gateway

// rollbackStack tracks cleanup functions to run on init failure.
// Cleanups are executed in LIFO order (last registered, first run).
type rollbackStack struct {
	cleanups []func()
}

func (r *rollbackStack) push(fn func()) {
	r.cleanups = append(r.cleanups, fn)
}

func (r *rollbackStack) run() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/gateway/ 2>&1 | head -10`
Expected: errors from other changes not yet made (OK)

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/rollback.go
git commit -m "feat(gateway): add rollbackStack for init failure cleanup

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 12: Create command router in gateway/router.go + commands.go

**Files:**
- Create: `internal/gateway/router.go`
- Create: `internal/gateway/commands.go`

- [ ] **Step 1: Write router.go**

```go
package gateway

import (
	"context"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// CommandHandler processes a slash command and returns a response string.
type CommandHandler func(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (response string, err error)

type commandEntry struct {
	handler CommandHandler
	exact   bool // true = exact match only, false = prefix match (for commands with args)
}

// commandTable maps slash command names to their handlers.
// Populated in Gateway.New().
type commandTable map[string]commandEntry

// dispatch tries to match msg.Text against registered commands.
// Returns (response, true) if a command was matched, ("", false) otherwise.
func (ct commandTable) dispatch(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, bool) {
	text := msg.Text

	// 1. Exact match
	if entry, ok := ct[text]; ok && entry.exact {
		resp, err := entry.handler(ctx, ch, msg)
		if err != nil {
			return "Error: " + err.Error(), true
		}
		return resp, true
	}

	// 2. Prefix match (commands with arguments like "/feature enable dashboard")
	for prefix, entry := range ct {
		if !entry.exact && strings.HasPrefix(text, prefix) {
			// Must be exact match OR followed by space
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				resp, err := entry.handler(ctx, ch, msg)
				if err != nil {
					return "Error: " + err.Error(), true
				}
				return resp, true
			}
		}
	}

	return "", false
}
```

- [ ] **Step 2: Write commands.go with extracted handlers**

The handlers are methods on `*Gateway` (they access gw.features, gw.tools, gw.cfg, etc.) but live in commands.go for cohesion. Each handler corresponds to an existing method in gateway.go, extracted and adapted to the `CommandHandler` signature.

Extract from gateway.go:
- `handleTasksCommand` → method on Gateway, signature `func (gw *Gateway) handleTasks(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, error)`
- `handleTeamCommand` → same pattern
- `handleModeCommand` → same pattern
- `handleFeatureCommand` → same pattern
- `handleConfigCommand` → same pattern
- `handleCompactCommand` → same pattern
- `handleModelCommand` → same pattern  
- `handleResetCommand` (was inline for /new and /start) → same pattern

Each handler sends its own response via `ch.Send()` and returns `("", nil)` (since response was already sent). Or returns the response string and the router sends it.

**Decision**: Handlers return `(string, error)`. The router sends the response. This makes handlers testable without a mock channel.

The existing handler bodies are moved from gateway.go lines 815-965 to commands.go, with the signature change. Each handler is adapted to return `(string, error)` instead of sending via `ch.Send()` internally.

Example:
```go
func (gw *Gateway) handleTasks(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw.tasks.TaskLedger() == nil {
		return "Task ledger not available.", nil
	}
	runningTasks, err := gw.tasks.TaskLedger().List(ctx, taskledger.TaskFilter{State: &taskledger.TaskStateRunning})
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}
	// ... format and return response string
}
```

Note: The full handler bodies are the same logic as the existing methods in gateway.go:815-965. We move them verbatim and adapt the signature. The exact code is omitted here for brevity but is 1:1 with the current gateway.go code.

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/gateway/ 2>&1 | head -20`
Expected: errors from gateway.go not yet updated (OK)

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/router.go internal/gateway/commands.go
git commit -m "feat(gateway): add command router with extracted slash command handlers

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 13: Rewrite Gateway.New() with rollbackStack and AgentDeps

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/init_agent.go`
- Modify: `internal/gateway/init_cognitive.go`
- Modify: `internal/gateway/init_multiagent.go`

- [ ] **Step 1: Update Gateway.New() to use rollbackStack**

In gateway.go, modify the `New()` function (lines 100-305) to wrap each init step with rollback:

```go
func New(cfg *config.Config, opts ...GatewayOptions) (*Gateway, error) {
	gw := &Gateway{...} // existing initialization
	
	var rb rollbackStack
	var err error
	defer func() { if err != nil { rb.run() } }()

	if err = gw.initDatabase(); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	rb.push(func() { gw.db.Close() })

	// ... each subsequent init step
	
	// After the last init succeeds:
	rb.cleanups = nil // success — Stop() handles cleanup, not rollback
	return gw, nil
}
```

The exact code wraps each `if err := gw.initXxx(); err != nil` block with `rb.push(...)` for the corresponding cleanup action. The cleanup for each init step is:
- `initDatabase` → `gw.db.Close()`
- `initFeatures` → (features have no cleanup beyond what's in Stop())
- `initToolsAndHooks` → (tools/hooks cleaned up in Stop())
- `initAgentRuntime` → (runtime cleaned up in Stop())
- `initMemorySystem` → `gw.memory.Store().Close()` if applicable
- `initCognitiveAgent` → (cleaned up in Stop())
- `initGraphEngine` → (cleaned up in Stop())
- `initKnowledgeSystem` → (cleaned up in Stop())
- `initSkillManager` → (cleaned up in Stop())
- `initMultiAgent` → (cleaned up in Stop())
- `initDashboard` → `gw.stopDashboard()`

- [ ] **Step 2: Update init_agent.go to construct and pass AgentDeps**

Replace the Runtime construction to build AgentDeps once:

```go
func (gw *Gateway) initAgentRuntime() error {
	deps := agent.AgentDeps{
		Core: agent.CoreDeps{
			Provider: gw.provider,
			Tools:    gw.tools,
			Sessions: gw.sessions,
			DB:       gw.db,
			Cfg:      gw.cfg.Agent,
			LLMCfg:   gw.cfg.LLM,
			AgentID:  "gateway",
		},
		Memory: agent.MemoryDeps{
			Store:        gw.memory.Store(),
			LifecycleMgr: gw.memory.LifecycleMgr(),
			Profiler:     gw.memory.Profiler(),
			ContextMgr:   gw.contextMgr,
			FactExtractor: gw.memory.FactExtractor(),
			BaseDir:      gw.memory.BaseDir(),
		}.WithDefaults(),
		Security: agent.SecurityDeps{
			Interceptor: gw.buildInterceptorChain(),
			HookMgr:     gw.hookMgr,
			PermEngine:  gw.permEngine,
		}.WithDefaults(),
		Observability: agent.ObservabilityDeps{
			Emitter:        gw.dashboard.Emitter(),
			MetricsEmitter: nil, // set later by TUI
			ReplayRecorder: gw.replayRecorder,
		}.WithDefaults(),
		MultiAgent: agent.MultiAgentDeps{
			SkillMgr:     gw.skillMgr,
			AgentMgr:     gw.agentMgr,
			Orchestrator: gw.orchestrator,
			ResultStore:  gw.resultStore,
			TaskLedger:   gw.tasks.TaskLedger(),
			Speculative:  gw.speculativeExecutor,
			PromptCache:  gw.promptCache,
			BgManager:    gw.bgManager,
		}.WithDefaults(),
	}

	gw.agentDeps = deps // store for later use by cognitive/multiagent init

	gw.runtime = agent.NewRuntime(deps)
	gw.runtime.SetApprovalFunc(gw.handleApproval)

	return nil
}
```

Note: `gw.buildInterceptorChain()` is a helper that returns `gw.interceptorChain` if set, or `tool.NewInterceptorChain(nil)` for the passthrough.

- [ ] **Step 3: Update init_cognitive.go**

```go
func (gw *Gateway) initCognitiveAgent() error {
	if gw.cfg.Agent.Mode != "cognitive" {
		return nil
	}

	opts := &agent.CognitiveAgentOptions{
		EntityExtractor:     gw.entityExtractor,
		EvolutionEngine:     gw.evolution.Engine(),
		RLPolicy:            gw.rlPolicy,
		RLTrainer:           gw.rlTrainer,
		CheckpointStore:     gw.checkpointStore,
		PlanMode:            gw.planMode,
		TreePlanner:         gw.treePlanner,
		MCTSPlanner:         gw.mctsPlanner,
		CodebaseIndex:       gw.codebaseIndex,
		CortexRetriever:     gw.cortex,
		DebateConfig:        gw.cfg.Agent.Cognitive.Debate,
		ObservationCallback: gw.observationCallback,
	}

	gw.cognitiveAgent = agent.NewCognitiveAgent(gw.agentDeps, opts)
	gw.cognitiveAgent.SetApprovalFunc(gw.handleApproval)

	return nil
}
```

- [ ] **Step 4: Update init_multiagent.go**

```go
func (gw *Gateway) initMultiAgent() error {
	if !gw.features.IsEnabled("multi_agent") {
		return nil
	}

	deps := gw.agentDeps // clone and customize for sub-agents
	deps.Core.AgentID = "subagent-manager"

	gw.subAgentManager = agent.NewSubAgentManager(deps)
	gw.tasks.SetSubAgentManager(gw.subAgentManager)

	return nil
}
```

- [ ] **Step 5: Delete handleInbound's slash command logic, replace with router.Dispatch**

In `handleInbound` (currently lines 538-706), delete lines 583-665 (all the slash command if-blocks: /tasks, /team, /mode, /feature, /config, /compact, /model, /new, /start).

Replace with:
```go
// Command dispatch
if resp, handled := gw.cmdTable.dispatch(ctx, ch, msg); handled {
	if resp != "" {
		if err := ch.Send(ctx, channel.OutboundMessage{
			Channel: msg.Channel, ChannelID: msg.ChannelID, Text: resp,
		}); err != nil {
			slog.Warn("failed to send command response", "err", err)
		}
	}
	return
}
```

- [ ] **Step 6: Populate commandTable in New()**

After all handlers are available (after initToolsAndHooks and initAgentRuntime), populate:

```go
gw.cmdTable = commandTable{
	"/tasks":   {gw.handleTasks, true},
	"/team":    {gw.handleTeam, false},
	"/mode":    {gw.handleMode, false},
	"/feature": {gw.handleFeature, false},
	"/config":  {gw.handleConfig, true},
	"/compact": {gw.handleCompact, true},
	"/model":   {gw.handleModel, false},
	"/new":     {gw.handleReset, true},
	"/start":   {gw.handleReset, true},
}
```

- [ ] **Step 7: Update Gateway struct to add agentDeps and cmdTable fields**

Add to Gateway struct:
```go
agentDeps agent.AgentDeps // shared dependency bundle
cmdTable  commandTable    // slash command routing table
```

Remove fields that are now accessed via agentDeps (the ones that were duplicated across runtime/cognitiveAgent — but keep them if they're used directly by Gateway for other purposes).

- [ ] **Step 8: Verify gateway package compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/gateway/ 2>&1`
Expected: fix compilation errors iteratively

- [ ] **Step 9: Commit**

```bash
git add internal/gateway/
git commit -m "refactor(gateway): wire AgentDeps, add rollbackStack, use command router

New() now constructs AgentDeps once, passes to Runtime/CognitiveAgent/SubAgentManager.
handleInbound delegates slash commands to commandTable.
Init failures trigger rollbackStack cleanup.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Workstream 5: Fix External Consumers

### Task 14: Fix eval harness

**Files:**
- Modify: `internal/eval/cognitive_runner.go`

- [ ] **Step 1: Adapt NewCognitiveAgent call**

The eval runner creates CognitiveAgent instances for benchmarks. Update to construct AgentDeps:

```go
deps := agent.AgentDeps{
	Core: agent.CoreDeps{
		Provider: provider,
		Tools:    tools,
		Sessions: sessions,
		DB:       db,
		Cfg:      cfg,
		LLMCfg:   llmCfg,
		AgentID:  "eval",
	},
}.WithDefaults()  // fills all sub-deps with no-ops

ca := agent.NewCognitiveAgent(deps, opts)
```

Note: ALL sub-deps (Memory, Security, Observability, MultiAgent) get their zero values + WithDefaults() = all no-ops. This is correct for eval — the benchmark harness doesn't need real memory/knowledge/dashboard.

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./internal/eval/ 2>&1`
Expected: fix any remaining errors

- [ ] **Step 3: Commit**

```bash
git add internal/eval/cognitive_runner.go
git commit -m "fix(eval): adapt cognitive runner to AgentDeps constructor

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

### Task 15: Fix test files and remaining compilation errors

**Files:**
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/agent/*_test.go` (all agent test files)
- Modify: Any other files with compilation errors

- [ ] **Step 1: Full build to find all errors**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./... 2>&1`
Expected: list of remaining compilation errors

- [ ] **Step 2: Fix each error**

For each compilation error:
- If a test calls `NewRuntime(provider, tools, sessions, db, cfg, llmCfg)`, change to `NewRuntime(AgentDeps{Core: CoreDeps{...}}.WithDefaults())`
- If a test uses `runtime.SetMemoryStore(x)`, change to constructing deps with `MemoryDeps{Store: x}`
- If a test uses any other Set* method, move that dependency into the deps construction

- [ ] **Step 3: Repeat until clean build**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./... 2>&1`
Expected: no errors

- [ ] **Step 4: Run all tests**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags fts5 ./... 2>&1`
Expected: all tests pass (or identify failing tests to fix)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "fix: adapt all tests and remaining callers to AgentDeps

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Final Verification

### Task 16: End-to-end verification

- [ ] **Step 1: Full build**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go build -tags fts5 ./...`
Expected: zero errors

- [ ] **Step 2: Full test suite**

Run: `cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -tags fts5 -count=1 ./... 2>&1`
Expected: all tests pass

- [ ] **Step 3: Verify success criteria**

Run these checks:
```bash
# 1. Zero Set* methods on Runtime (except SetApprovalFunc, SetModel, SetParentID, SetDepth, SetChainID)
grep -c 'func (r \*Runtime) Set' internal/agent/runtime.go
# Expected: 5 (SetApprovalFunc, SetModel, SetParentID, SetDepth, SetChainID)

# 2. Zero if r.xxx != nil on interface fields
grep -n 'if r\.\(dashEmitter\|metricsEmitter\|memStore\|contextManager\|taskLedger\|interceptorChain\)' internal/agent/runtime.go
# Expected: no output

# 3. handleInbound under 20 lines  
# (count lines between func signature and closing brace in the new handleInbound)

# 4. Gateway.New() uses rollbackStack
grep -n 'rollbackStack' internal/gateway/gateway.go
# Expected: multiple matches showing rb.push() calls
```

- [ ] **Step 4: Final commit (if any fixups needed)**

```bash
git add -A && git commit -m "chore: final verification fixes for Phase 1

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Dependency Order

```
Task 1 (agent/noop.go) ──┐
Task 2 (memory/noop.go) ─┤
Task 3 (knowledge/noop.go) ─┤
Task 4 DELETED            ├──→ Task 6 (deps.go) ──→ Task 7 (runtime) ──→ Task 8 (sub-packages)
Task 5 (taskledger/noop.go) ─┘                                       

Task 11 (rollback.go) ──┐
Task 12 (router.go)     ├──→ Task 13 (gateway init) ──→ Task 14 (eval) ──→ Task 15 (tests) ──→ Task 16 (verify)
                         │
Task 9 (cognitive.go) ──┤
Task 10 (subagent.go) ──┘
```

Parallel opportunities: Tasks 1-5 can run concurrently. Tasks 7-8-9-10 are sequential (each builds on the previous). Task 11-12 can run in parallel with 9-10. Task 13 depends on all preceding tasks.

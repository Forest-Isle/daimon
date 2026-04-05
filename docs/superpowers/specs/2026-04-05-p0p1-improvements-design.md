# P0+P1 Improvements Design — API Retry, Gateway Split, Token Budget, Progressive Compression

**Date:** 2026-04-05
**Scope:** 4 improvements referencing Claude Code architecture patterns

---

## 1. API Layer Retry Mechanism

### Problem
All LLM calls (`Complete`, `Stream`) fail immediately on transient errors (429 rate limit, 5xx server errors, network timeouts). No retry logic exists anywhere in the codebase.

### Design: Decorator Pattern — `RetryProvider`

A new `RetryProvider` struct wraps any `Provider` implementation without modifying existing code.

**File:** `internal/agent/retry.go` (new)

```go
type RetryProvider struct {
    inner      Provider
    maxRetries int           // default: 3
    baseDelay  time.Duration // default: 1s
    maxDelay   time.Duration // default: 30s
}

func (rp *RetryProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
    // Retry loop with exponential backoff + jitter
}

func (rp *RetryProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
    // Only retry initial connection errors
    // Mid-stream errors are NOT retried (would lose partial state)
}
```

**Error classification:**
- **Retryable:** HTTP 429, 500, 502, 503, 529, network timeouts, connection reset
- **Non-retryable:** HTTP 400, 401, 403, 404, context cancelled

**Backoff formula:** `delay = min(baseDelay * 2^attempt + jitter, maxDelay)`
- Jitter: random 0-25% of computed delay

**Config additions** (`ironclaw.yaml`):
```yaml
llm:
  retry:
    max_retries: 3
    base_delay: 1s
    max_delay: 30s
```

**Wiring** (in `gateway.go`): Wrap provider before passing to runtime:
```go
provider := agent.NewClaudeProvider(...)
if cfg.LLM.Retry.MaxRetries > 0 {
    provider = agent.NewRetryProvider(provider, cfg.LLM.Retry)
}
runtime := agent.NewRuntime(provider, ...)
```

### Streaming Retry Strategy

- **Pre-stream error** (connection failure): Retry the entire `Stream()` call
- **Mid-stream error** (partial data received): Do NOT retry. Return error to caller. The agent loop already handles this by sending error to user and continuing.

### Logging

Each retry logs at `slog.Warn` level:
```
WARN retry attempt llm_call=Complete attempt=2/3 delay=2.1s error="429 rate limited"
```

---

## 2. Gateway Initialization Split

### Problem
`gateway.New()` is 427 lines with 12 interleaved subsystems. Hard to read, test, and maintain.

### Design: Extract `init*()` Methods

Split into 8 focused initialization functions. Each takes explicit dependencies, returns components.

**New Gateway struct fields** (for intermediate state during init):

```go
type Gateway struct {
    // existing fields...
    db             *store.DB
    sessions       *session.Manager
    tools          *tool.Registry
    hookMgr        *hook.Manager
    permEngine     *tool.PermissionEngine
    runtime        *agent.Runtime
    memStore       *memory.FileStore
    cognitive      *agent.CognitiveAgent
    skillMgr       *skill.Manager
    // ...
}
```

**Refactored `New()`:**

```go
func New(cfg *config.Config) (*Gateway, error) {
    gw := &Gateway{cfg: cfg}

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
    return gw, nil
}
```

**File organization:**
- `internal/gateway/gateway.go` — Gateway struct + `New()` orchestrator (~60 lines)
- `internal/gateway/init_database.go` — `initDatabase()` (~15 lines)
- `internal/gateway/init_tools.go` — `initToolsAndHooks()` (~45 lines)
- `internal/gateway/init_agent.go` — `initAgentRuntime()` (~40 lines)
- `internal/gateway/init_memory.go` — `initMemorySystem()` (~100 lines)
- `internal/gateway/init_cognitive.go` — `initCognitiveAgent()` (~30 lines)
- `internal/gateway/init_knowledge.go` — `initKnowledgeSystem()` (~90 lines)
- `internal/gateway/init_skills.go` — `initSkillManager()` (~25 lines)
- `internal/gateway/init_multiagent.go` — `initMultiAgent()` (~45 lines)

**Rules:**
- Each `init*()` is a method on `*Gateway` — can read/write Gateway fields
- Dependencies are accessed through `gw.*` fields set by prior init functions
- Each function handles its own "if not enabled, skip" logic
- Error wrapping provides clear subsystem identification

---

## 3. Token Budget System

### Problem
No awareness of context window usage. Compression is all-or-nothing — no gradual response to growing context.

### Design: `TokenBudget` Component

**File:** `internal/agent/token_budget.go` (new)

```go
type BudgetAction int

const (
    BudgetOK              BudgetAction = iota // < threshold, no action
    BudgetCompressLight                        // 70-80% — trim tool results
    BudgetCompressMedium                       // 80-90% — summarize history
    BudgetCompressHeavy                        // > 90% — aggressive truncation
)

type TokenBudget struct {
    ModelLimit      int     // context window size (tokens)
    LightThreshold  float64 // default 0.70
    MediumThreshold float64 // default 0.80
    HeavyThreshold  float64 // default 0.90
}

type BudgetCheck struct {
    TotalTokens   int
    UsageRatio    float64
    Action        BudgetAction
    TokensToFree  int  // how many tokens to reclaim
}

func (tb *TokenBudget) Check(messages []session.Message) BudgetCheck
```

**Token counting strategy:**
- Use character-based estimation: `tokens ≈ len(content) / 3.5` (works for mixed CJK/English)
- Simple and fast — no external dependency needed
- Accuracy within ~10% is sufficient for budget decisions

**Config:**
```yaml
agent:
  token_budget:
    model_limit: 200000    # claude-3.5-sonnet context window
    light_threshold: 0.70
    medium_threshold: 0.80
    heavy_threshold: 0.90
```

**Integration point:** Called in `runtime.go` before each LLM call:
```go
check := r.budget.Check(sess.Messages)
if check.Action > BudgetOK {
    r.compress(ctx, sess, check)  // triggers progressive compression
}
```

---

## 4. Progressive Compression Strategy

### Problem
Current `layered` compression is a single strategy. No gradual response — either full compression or nothing.

### Design: 3-Level Progressive Compression

**File:** `internal/agent/compression.go` (modify existing)

Extend `CompressionPipeline` to support levels:

**Level 1 — Light (70-80% usage):**
- Truncate `tool_result` content to first 200 chars + "... [truncated]"
- Remove duplicate system context injections
- Keep all conversation messages intact
- Target: free ~15% of tokens

**Level 2 — Medium (80-90% usage):**
- LLM-powered summarization of messages older than last 5 turns
- Replace N old messages with 1 summary message (role: "system")
- Preserve all tool_use/tool_result pairs from recent turns
- Target: free ~30% of tokens

**Level 3 — Heavy (>90% usage):**
- Keep only: system prompt + facts summary + last 3 turns
- Generate a "session context" summary from all discarded messages
- Inject summary as a system message
- Target: free ~50% of tokens

**Implementation:**

```go
func (r *Runtime) compress(ctx context.Context, sess *session.Session, check BudgetCheck) error {
    switch check.Action {
    case BudgetCompressLight:
        return r.compressLight(sess)        // in-place, no LLM call
    case BudgetCompressMedium:
        return r.compressMedium(ctx, sess)  // 1 LLM call for summary
    case BudgetCompressHeavy:
        return r.compressHeavy(ctx, sess)   // 1 LLM call for aggressive summary
    }
    return nil
}
```

**Light compression** (no LLM call — fast):
```go
func (r *Runtime) compressLight(sess *session.Session) error {
    for i, msg := range sess.Messages {
        if msg.Role == "tool_result" && len(msg.Content) > 500 {
            sess.Messages[i].Content = msg.Content[:200] + "\n... [truncated, " +
                strconv.Itoa(len(msg.Content)) + " chars total]"
        }
    }
    return nil
}
```

**Medium compression** (1 LLM call):
```go
func (r *Runtime) compressMedium(ctx context.Context, sess *session.Session) error {
    // Keep last 5 turns, summarize the rest
    keepCount := 10 // 5 turns = 10 messages (user+assistant)
    if len(sess.Messages) <= keepCount+1 {
        return nil // nothing to compress
    }

    oldMessages := sess.Messages[1:len(sess.Messages)-keepCount] // skip system prompt
    summary, err := r.summarize(ctx, oldMessages)
    if err != nil {
        return r.compressLight(sess) // fallback to light
    }

    // Replace old messages with summary
    newMessages := make([]session.Message, 0)
    newMessages = append(newMessages, sess.Messages[0]) // system prompt
    newMessages = append(newMessages, session.Message{
        Role:    "system",
        Content: "## Conversation Summary\n" + summary,
    })
    newMessages = append(newMessages, sess.Messages[len(sess.Messages)-keepCount:]...)
    sess.Messages = newMessages
    return nil
}
```

**Heavy compression** (1 LLM call):
Same as medium but keeps only last 3 turns and generates more aggressive summary.

**Fallback chain:** Heavy fails → try Medium → try Light → give up (let LLM handle overflow)

---

## Implementation Order

1. **#5 API Retry** — independent, no other code changes needed
2. **#25 Gateway Split** — pure refactor, no behavior change
3. **#1 Token Budget** — new component, small integration point
4. **#2 Progressive Compression** — depends on Token Budget

Items 1 & 2 are independent and can be done in parallel.
Items 3 & 4 are sequential (compression needs budget to trigger).

---

## Testing Strategy

- **Retry:** Unit test with mock provider that fails N times then succeeds; test error classification
- **Gateway:** Ensure existing tests still pass after split (pure refactor)
- **Token Budget:** Unit test threshold logic with known message sizes
- **Compression:** Unit test each level independently; integration test budget→compression flow

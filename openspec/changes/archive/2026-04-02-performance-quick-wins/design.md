## Context

IronClaw is a local-first AI agent runtime in Go that connects Claude AI with tools (bash, file, HTTP, browser) via channels (Telegram, TUI). The current tool execution model in `internal/agent/runtime.go` is strictly sequential: when the LLM returns multiple tool calls, each is executed one-by-one in a `for` loop (lines 208-250). The compaction strategy in `internal/agent/compaction.go` uses a single threshold (40 messages) and always invokes an LLM call to summarize, with no intermediate cost-saving layers. Tool results are kept fully in session messages, truncated only by a compressor if attached.

This design was appropriate for simple, single-tool interactions but becomes a bottleneck as conversations grow longer and tool calls multiply. Claude Code's architecture demonstrates that pipelined execution, disk persistence, and layered compression can dramatically improve both latency and token efficiency without sacrificing correctness.

## Goals / Non-Goals

**Goals:**
- Reduce tool execution latency by 30-50% through concurrent execution of independent read-only tools
- Reduce token consumption by 40-60% through disk persistence of large tool outputs
- Minimize unnecessary LLM calls by replacing single-pass compression with progressive multi-layer strategy
- Maintain backward compatibility: existing tool implementations continue to work unchanged (default `IsReadOnly() = false`)
- Keep the architecture simple and Go-idiomatic

**Non-Goals:**
- Streaming tool results during execution (future work)
- Changing the Tool interface in breaking ways (new methods have defaults)
- Implementing a full plugin/hook system (that's Phase 2)
- Modifying the cognitive agent's 5-phase loop (only simple mode's tool execution loop changes)
- Distributed caching or shared tool result storage

## Decisions

### Decision 1: Optional `ReadOnlyTool` interface instead of breaking `Tool` interface

**Choice**: Define a new optional interface `ReadOnlyTool` with an `IsReadOnly() bool` method. Tools that don't implement it default to `false` (write-capable, executed sequentially).

**Alternatives considered**:
- *Add `IsReadOnly()` to the `Tool` interface*: Would break all existing tool implementations (bash, file, HTTP, browser, MCP tools). Rejected — too disruptive for a performance optimization.
- *Use tool name heuristics*: Guess read-only status from tool name patterns. Rejected — fragile and incorrect for tools like `bash` which can be either.
- *Configuration-based*: Declare read-only status in YAML config. Rejected — tools know their own semantics best.

**Rationale**: Go's interface composition pattern (type assertion) is idiomatic, non-breaking, and lets each tool opt-in explicitly. This is the same pattern used for `ApprovalSender` / `ReflectionSender` in channels.

```go
type ReadOnlyTool interface {
    IsReadOnly() bool
}

// Usage in registry
func isReadOnly(t Tool) bool {
    if ro, ok := t.(ReadOnlyTool); ok {
        return ro.IsReadOnly()
    }
    return false
}
```

### Decision 2: Bounded concurrency with `errgroup` for read-only tools

**Choice**: Use `golang.org/x/sync/errgroup` with a configurable concurrency limit (default 4). Within a single LLM response's tool calls, read-only tools execute concurrently while write tools execute sequentially after all reads complete.

**Alternatives considered**:
- *Unbounded goroutines per tool call*: Simple but risks resource exhaustion with many concurrent bash commands. Rejected.
- *Worker pool*: More complex, unnecessary for typical 2-5 concurrent tool calls. Rejected — errgroup is sufficient.
- *Full DAG scheduler (like cognitive mode)*: Overkill for simple mode's flat tool call list. Rejected.

**Execution order**:
```
Tool calls from LLM: [file_read, grep, bash_write, file_read]
                      ├── Concurrent: file_read(1), grep, file_read(2)
                      └── Sequential (after reads): bash_write
```

### Decision 3: Disk persistence with line-boundary-aware preview

**Choice**: Tool results exceeding a configurable threshold (default 8KB) are written to `~/.ironclaw/cache/tool-results/{session_id}/{tool_use_id}.txt`. The session message stores a truncated preview (first 2000 chars, cut at line boundary) plus a `[TRUNCATED — full output: {path}]` reference.

**Alternatives considered**:
- *Always persist to disk*: Unnecessary overhead for small results (most are < 1KB). Rejected.
- *LLM-based summarization of results*: Adds latency and cost for every large result. Rejected as the default strategy — can be layered on later.
- *In-memory LRU cache*: Doesn't survive restarts, still consumes RAM. Rejected.

**Cleanup strategy**: Results older than 24h cleaned by a background goroutine (runs hourly). Session-scoped directory makes bulk cleanup trivial.

### Decision 4: Four-layer progressive compression replacing single-pass

**Choice**: Replace `CompactHistory()` with a `CompressionPipeline` that runs layers in order, each checking if compression is still needed:

| Layer | Trigger (usage %) | Action | LLM call? |
|-------|-------------------|--------|-----------|
| 1. Tool result eviction | 30% | Replace inline results > threshold with disk references | No |
| 2. Old turn summarization | 50% | Summarize turns older than N iterations | Yes (cheap) |
| 3. System prompt slimming | 70% | Remove low-relevance memories, trim skill metadata | No |
| 4. Emergency truncation | 90% | Drop oldest messages (keep last N turns) | No |

**Alternatives considered**:
- *Single improved threshold*: Still all-or-nothing. Rejected.
- *Token-counting per message*: Accurate but expensive (requires tokenizer). Use character-based estimation instead (4 chars ≈ 1 token). Acceptable for threshold checks.

**Rationale**: Each layer is independently testable. Only layer 2 requires an LLM call, and it's skipped entirely if layer 1 brings usage below 50%. This avoids the current pattern where every compaction triggers an LLM call.

### Decision 5: Configuration structure

```yaml
tools:
  concurrent_execution:
    enabled: true
    max_concurrency: 4  # max parallel read-only tools
  result_persistence:
    enabled: true
    threshold_bytes: 8192  # persist results larger than this
    preview_chars: 2000    # chars to keep inline
    cache_dir: ""          # default: ~/.ironclaw/cache/tool-results/
    ttl_hours: 24          # cleanup after this duration

agent:
  compression:
    strategy: layered  # "layered" | "legacy" (old single-pass)
    layers:
      tool_eviction_pct: 30
      summarize_pct: 50
      slim_prompt_pct: 70
      emergency_pct: 90
    token_estimate_ratio: 0.25  # chars-to-tokens ratio
```

## Risks / Trade-offs

**[Risk] Concurrent tool execution may cause ordering-dependent bugs** → Mitigation: Only tools that explicitly opt in via `ReadOnlyTool` interface run concurrently. Write tools always wait for all reads to complete first. Integration tests verify ordering.

**[Risk] Disk-persisted results may become stale or orphaned** → Mitigation: 24h TTL with hourly cleanup. Session-scoped directories enable bulk deletion. Startup cleanup sweep removes any stale cache.

**[Risk] Character-based token estimation is imprecise** → Mitigation: Use conservative ratio (4 chars = 1 token, overestimates for English). Compression triggers early rather than late. Layer 4 (emergency) provides hard safety net.

**[Risk] Existing `CompactHistory` callers need migration** → Mitigation: `strategy: legacy` config option preserves old behavior. Default changes to `layered` only in new installations.

**[Trade-off] Added complexity in tool execution path** → Accepted: The concurrent path is isolated behind a feature flag and only activates when multiple read-only tools are called simultaneously. Sequential fallback is always available.

**[Trade-off] Disk I/O for result persistence** → Accepted: Disk writes are async (goroutine), reads are rare (only when LLM explicitly requests full output via a hypothetical `read_cached_result` tool or during context reconstruction).

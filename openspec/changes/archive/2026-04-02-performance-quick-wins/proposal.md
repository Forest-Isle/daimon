## Why

IronClaw's agent loop currently executes tools sequentially, keeps all tool results in memory regardless of size, and uses a single-threshold LLM compression strategy for context management. Analysis of Claude Code's architecture reveals that these patterns introduce significant latency (tools wait on each other), waste tokens (large outputs stay in context), and trigger unnecessary LLM calls (compression is all-or-nothing). Adopting pipelined execution, disk persistence, and layered compression can yield 30-50% latency reduction and 40-60% token savings with relatively low implementation effort.

## What Changes

- **Concurrent tool execution**: Read-only tools (file read, grep, HTTP GET) execute in parallel when multiple tool calls arrive in a single LLM response. A new `IsReadOnly()` capability flag on the `Tool` interface enables safe concurrency decisions.
- **Tool result disk persistence**: Large tool outputs (above a configurable threshold) are written to disk under `~/.ironclaw/cache/tool-results/`. Only a truncated preview and a disk reference remain in the conversation context.
- **Layered context compression**: Replace the current dual-trigger (token threshold + turn count) single-pass LLM compression with a 4-layer progressive strategy: (1) tool result persistence, (2) old turn summarization, (3) system prompt slimming, (4) emergency truncation. Each layer activates at a different context utilization threshold.

## Capabilities

### New Capabilities
- `tool-concurrent-execution`: Parallel execution of read-only tools with `IsReadOnly` capability flag, bounded concurrency, and ordering guarantees for write tools.
- `tool-result-persistence`: Disk-based storage for large tool outputs with configurable thresholds, preview generation at line boundaries, and transparent retrieval.
- `layered-context-compression`: Multi-layer progressive compression strategy with configurable thresholds per layer, replacing the single-pass LLM compression approach.

### Modified Capabilities
<!-- No existing spec-level requirements are changing. Implementation details of the agent loop and compactor change, but no existing capability contracts are altered. -->

## Impact

- **`internal/tool/`**: `Tool` interface gains `IsReadOnly() bool` method (all existing tools need implementation). `Registry` gains concurrent dispatch logic.
- **`internal/agent/runtime.go`**: Tool execution loop refactored for concurrency. Result handling updated to support disk references.
- **`internal/agent/compact.go`**: Compaction logic replaced with layered strategy. New `CompressionStrategy` interface and registry.
- **`internal/agent/context.go`**: Context builder updated to handle disk-persisted results and progressive slimming.
- **Configuration**: New YAML sections under `agent.compression` and `tools.result_persistence` with sensible defaults.
- **Disk**: New cache directory `~/.ironclaw/cache/tool-results/` with automatic cleanup.
- **Dependencies**: No new external dependencies. Uses standard library `sync`, `os`, `path/filepath`.

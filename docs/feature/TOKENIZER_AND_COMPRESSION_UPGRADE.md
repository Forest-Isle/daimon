# Tokenizer & Context Compression Upgrade

## Overview

Replaces the character-based token estimation with a real tokenizer and adds a multi-stage 413 recovery chain to the context management pipeline. This brings token utilization accuracy from ~60% to ~95%, preventing premature or delayed compression.

## Problem

The original `EstimateUtilization` used `charCount * 0.25 / contextWindow` — a fixed ratio that diverges significantly from actual BPE token counts. This caused:

- Premature compression on CJK-heavy text (characters are much more token-dense)
- Late compression on ASCII code (characters are less token-dense)
- No recovery when a 413 error occurred — a single `RunForced` was the only fallback

## Architecture

### Tokenizer Interface

```go
// internal/agent/tokenizer.go

type Tokenizer interface {
    Count(text string) int
    CountMessages(msgs []session.Message, systemPrompt string) int
}
```

Three implementations, selected by factory with automatic fallback:

| Implementation | Accuracy | Latency | Dependency |
|---|---|---|---|
| `TiktokenTokenizer` | ~95% | ~2ms/call | `github.com/pkoukk/tiktoken-go` |
| `RatioTokenizer` | ~60% | <1ms | None (legacy fallback) |

**Factory**: `NewTokenizer(model, ratio)` tries tiktoken first; falls back to ratio on failure.

**Large text optimization**: Texts exceeding 32KB are sampled (first 32KB encoded, ratio extrapolated) to avoid O(n) BPE performance on very large tool outputs.

### Model-to-Encoding Mapping

```go
func modelToEncoding(model string) string {
    // GPT-4 / GPT-3.5 → cl100k_base
    // Claude / unknown  → cl100k_base (closest available approximation)
}
```

Claude uses a proprietary tokenizer, but cl100k_base provides a reasonable approximation (~85-90% accuracy) and is the only widely available BPE encoding in Go.

### Integration Points

**`CompressionPipeline`** (`internal/agent/compression.go`):
- `estimateUtilization()` now calls `Tokenizer.CountMessages()` instead of `countContextChars() * ratio`
- Accepts an optional `Tokenizer` via the `SetTokenizer()` method
- Falls back to `RatioTokenizer` if none is set

**`PipelineContextManager`** (`internal/agent/context_manager.go`):
- Holds a `Tokenizer` field, initialized in `NewPipelineContextManager()`
- `Utilization()` delegates to `Tokenizer.CountMessages()`
- Backward compatible: `nil` tokenizer triggers legacy ratio path

### 413 Recovery Chain

New method: `ReactiveCompressWithRetry(ctx, sess, systemPrompt, originalMaxTokens) (int, error)`

```
Step 1: RunForced (all compression layers, no threshold checks)
  └→ Success → return reduced maxTokens
  └→ Failure ↓
Step 2: Reduce maxTokens to 75% of original
  └→ Return reduced maxTokens for caller to retry
  └→ If already at minimum (1024) → return error
```

The caller (cognitive loop) handles the actual LLM retry with the adjusted `maxTokens`.

## Files Changed

| File | Change |
|---|---|
| `internal/agent/tokenizer.go` | **New** — Tokenizer interface + TiktokenTokenizer + RatioTokenizer + factory |
| `internal/agent/tokenizer_test.go` | **New** — Unit tests for all tokenizer implementations |
| `internal/agent/compression.go` | Modified — `SetTokenizer()` method, updated `estimateUtilization()` |
| `internal/agent/context_manager.go` | Modified — Tokenizer integration, `ReactiveCompressWithRetry()` |
| `internal/agent/context_manager_test.go` | Modified — Tests for tokenizer integration and recovery chain |
| `go.mod` / `go.sum` | Modified — Added `github.com/pkoukk/tiktoken-go` dependency |

## Configuration

No new configuration required. The tokenizer is automatically selected based on the model name passed to `NewPipelineContextManager()`. The existing `TokenEstimateRatio` config field is used as the fallback ratio if tiktoken is unavailable.

## Compression Pipeline (Unchanged Structure)

The 5-layer compression pipeline structure is preserved:

```
Layer 0: ToolOutputPrePrune  (30%) — truncate old tool outputs
Layer 1: ToolEviction        (30%) — persist/truncate large results
Layer 2: TurnSummarization   (50%) — LLM-summarize old turns
Layer 3: OldContextRemoval   (70%) — remove oldest third
Layer 4: EmergencyTruncation (90%) — keep only last 10 turns
```

The only change is how utilization is measured (real tokens vs character ratio).

## Testing

```bash
go test -tags fts5 -run "TestTokenizer|TestEstimate|TestContextManager|TestCompression" ./internal/agent/...
```

# Prompt Cache Metrics & OpenAI Cache Awareness

## Overview

Adds comprehensive prompt caching metrics tracking and extends the OpenAI provider with cache-aware token reporting. The Claude provider already implements Anthropic's explicit `cache_control` API; this change brings parity by tracking OpenAI's automatic prefix caching and providing unified cache performance visibility.

## Components

### Cache Metrics (`internal/agent/cache_metrics.go`)

Thread-safe metrics tracker for prompt caching performance:

```go
metrics := NewCacheMetrics(100)  // 100-entry sliding window

// Record after each LLM call
metrics.Record(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)

// Query performance
metrics.HitRate()          // 0.0-1.0 overall
metrics.RecentHitRate()    // 0.0-1.0 over recent window
metrics.TokenSavingsRate() // fraction of tokens served from cache
metrics.Snapshot()         // JSON-serializable point-in-time copy
```

**Metrics tracked**:

| Metric | Description |
|--------|-------------|
| `TotalRequests` | Total LLM API calls |
| `CacheHits` | Requests with cacheReadTokens > 0 |
| `CacheMisses` | Requests with cacheReadTokens == 0 |
| `HitRate` | CacheHits / TotalRequests |
| `TotalInputTokens` | Cumulative input tokens |
| `TotalCacheReadTokens` | Tokens served from cache |
| `TotalCacheCreationTokens` | Tokens used to create cache entries |
| `TokenSavingsRate` | CacheReadTokens / (InputTokens + CacheReadTokens) |
| `RecentHitRate` | Hit rate over sliding window (default 100 requests) |

### OpenAI Provider Enhancement (`internal/agent/openai.go`)

**New response parsing**:
```go
type oaiUsage struct {
    PromptTokens        int                      `json:"prompt_tokens"`
    CompletionTokens    int                      `json:"completion_tokens"`
    TotalTokens         int                      `json:"total_tokens"`
    PromptTokensDetails *oaiPromptTokensDetails  `json:"prompt_tokens_details,omitempty"`
}

type oaiPromptTokensDetails struct {
    CachedTokens int `json:"cached_tokens"`
}
```

OpenAI's API (2024+) automatically caches prompt prefixes. The provider now:
1. Sends `stream_options: {"include_usage": true}` on streaming requests
2. Parses `prompt_tokens_details.cached_tokens` from response usage
3. Records to `CacheMetrics` after each Complete/Stream call
4. Exposes `GetCacheStats()` and `CacheMetricsSnapshot()` methods

### Runtime Integration (`internal/agent/runtime.go`)

Metrics emission now uses type switch to support both providers:
```go
switch p := provider.(type) {
case *ClaudeProvider:
    // existing cache metrics
case *OpenAIProvider:
    stats := p.GetTokenStats()
    // emit to dashboard/cogmetrics
}
```

## Caching Strategy by Provider

| Provider | Cache Mechanism | IronClaw Support |
|----------|----------------|------------------|
| **Claude** | Explicit `cache_control` on system prompt + tools | Full — static/dynamic split at `<!-- DYNAMIC_CONTEXT -->` |
| **OpenAI** | Automatic prefix matching (no explicit API) | Metrics tracking — maximized by keeping system prompt stable |

### Maximizing OpenAI Cache Hit Rate

OpenAI caches the **longest common prefix** across requests. To maximize hits:
1. System prompt is assembled with stable content first (personality, rules)
2. Dynamic content (memories, project context) goes after the cache boundary
3. Tool definitions are stable across turns (automatically cached by OpenAI)

The `RecentHitRate()` metric helps verify that prompt assembly order is effective.

## Files

| File | Lines | Description |
|---|---|---|
| `internal/agent/cache_metrics.go` | 144 | CacheMetrics with sliding window |
| `internal/agent/cache_metrics_test.go` | 165 | 7 tests for all metric calculations |
| `internal/agent/openai.go` | +105/-6 | Cache-aware usage parsing |
| `internal/agent/runtime.go` | +10/-2 | Dual-provider metrics emission |

## Testing

```bash
go test -run TestCacheMetrics ./internal/agent/...
# 7 tests pass
```

## Dashboard Integration

`CacheMetricsSnapshot` is JSON-serializable for dashboard display:

```json
{
  "total_requests": 142,
  "cache_hits": 98,
  "cache_misses": 44,
  "hit_rate": 0.69,
  "total_input_tokens": 1250000,
  "total_cache_read_tokens": 890000,
  "total_cache_creation_tokens": 45000,
  "token_savings_rate": 0.42
}
```

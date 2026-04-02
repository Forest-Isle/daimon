## 1. Tool Interface Extension — ReadOnlyTool

- [x] 1.1 Define `ReadOnlyTool` optional interface in `internal/tool/tool.go` with `IsReadOnly() bool` method
- [x] 1.2 Add `isReadOnly(t Tool) bool` helper function in registry using type assertion
- [x] 1.3 Implement `IsReadOnly()` on `file.go` tool (return `true` for read operations, `false` for write)
- [x] 1.4 Implement `IsReadOnly()` on `bash.go` tool (return `false` — bash commands may have side effects)
- [x] 1.5 Implement `IsReadOnly()` on `http.go` tool (return `true` for GET/HEAD, `false` for POST/PUT/DELETE)
- [x] 1.6 Implement `IsReadOnly()` on `browser.go` tool (return `true`)
- [x] 1.7 Write unit tests for `isReadOnly` helper with tools that do and don't implement the interface

## 2. Concurrent Tool Execution

- [x] 2.1 Add `tools.concurrent_execution` config fields to `internal/config/config.go` (enabled, max_concurrency)
- [x] 2.2 Add `golang.org/x/sync/errgroup` dependency
- [x] 2.3 Implement `executeConcurrent()` method on Runtime that partitions tool calls into read-only and write groups
- [x] 2.4 Execute read-only group concurrently via errgroup with bounded concurrency, collect results into map
- [x] 2.5 Execute write group sequentially after all reads complete
- [x] 2.6 Serialize approval requests for tools that require approval in concurrent batch
- [x] 2.7 Refactor `HandleMessage` tool execution loop (lines 208-250) to use `executeConcurrent()` when enabled
- [x] 2.8 Refactor `handleNonStreaming` tool execution loop similarly
- [x] 2.9 Write integration test: 3 read-only tools execute concurrently (verify parallelism via timing)
- [x] 2.10 Write integration test: mixed read/write tools execute in correct order
- [x] 2.11 Write test: feature disabled falls back to sequential execution

## 3. Tool Result Disk Persistence

- [x] 3.1 Add `tools.result_persistence` config fields to `internal/config/config.go` (enabled, threshold_bytes, preview_chars, cache_dir, ttl_hours)
- [x] 3.2 Create `internal/tool/resultstore.go` with `ResultStore` struct (Store, Load, Cleanup methods)
- [x] 3.3 Implement `Store()`: write large results to `{cache_dir}/{session_id}/{tool_use_id}.txt`, return preview
- [x] 3.4 Implement line-boundary-aware preview truncation (`truncateAtLineBoundary`)
- [x] 3.5 Implement `Cleanup()`: remove files older than TTL, called on startup and periodically
- [x] 3.6 Start cleanup goroutine in gateway.go (hourly interval)
- [x] 3.7 Integrate `ResultStore` into Runtime: after tool execution, persist if result exceeds threshold
- [x] 3.8 Update `addToolResult` to store preview + reference instead of full output when persisted
- [x] 3.9 Write unit test for `truncateAtLineBoundary` with various edge cases
- [x] 3.10 Write unit test for Store/Load/Cleanup lifecycle
- [x] 3.11 Write test: error results are never persisted to disk

## 4. Layered Context Compression

- [x] 4.1 Add `agent.compression` config fields to `internal/config/config.go` (strategy, layers thresholds, token_estimate_ratio)
- [x] 4.2 Create `internal/agent/compression.go` with `CompressionLayer` interface and `CompressionPipeline` struct
- [x] 4.3 Implement `estimateUtilization()` using char-to-token ratio against model context window
- [x] 4.4 Implement Layer 1 (`ToolEvictionLayer`): replace large inline tool results with disk references
- [x] 4.5 Implement Layer 2 (`TurnSummarizationLayer`): LLM-based summarization of old turns (reuse existing compaction logic)
- [x] 4.6 Implement Layer 3 (`SystemPromptSlimLayer`): remove low-relevance memories and trim skill metadata
- [x] 4.7 Implement Layer 4 (`EmergencyTruncationLayer`): drop oldest messages keeping last N turns
- [x] 4.8 Implement pipeline executor: run layers in order with early exit when utilization drops below next threshold
- [x] 4.9 Integrate pipeline into Runtime, replacing `CompactHistory()` call in `HandleMessage` (line 96-98)
- [x] 4.10 Preserve `CompactHistory()` as the `legacy` strategy option
- [x] 4.11 Write unit test for `estimateUtilization` calculation
- [x] 4.12 Write test: early exit after layer 1 skips LLM call
- [x] 4.13 Write test: legacy mode uses original CompactHistory behavior
- [x] 4.14 Write integration test: full pipeline with all 4 layers triggered progressively

## 5. Configuration & Documentation

- [x] 5.1 Add default values for all new config fields in `ironclaw.example.yaml`
- [x] 5.2 Update `configs/ironclaw.yaml` template with new sections (commented out with explanations)
- [x] 5.3 Verify all new config fields are properly loaded and validated in config package
- [x] 5.4 Run full test suite (`make test`) and fix any regressions
- [x] 5.5 Run linter (`make lint`) and fix any issues

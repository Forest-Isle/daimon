# Memory System Optimization - Implementation Tasks

## Phase 1: Foundation (Week 1-2)

### Task 1.1: Create UnifiedStore Wrapper
**Priority**: P0
**Estimate**: 2 days
**Owner**: Backend

Create a unified interface that wraps existing FileStore and SQLiteStore without breaking changes.

**Files**:
- `internal/memory/unified_store.go` (new)

**Acceptance Criteria**:
- [ ] UnifiedStore implements Store interface
- [ ] Delegates to FileStore as primary
- [ ] Async index updates to SQLite
- [ ] All existing tests pass
- [ ] No performance regression

---

### Task 1.2: Add Fact History Table
**Priority**: P0
**Estimate**: 1 day
**Owner**: Backend

Add immutable audit log for all fact changes.

**Files**:
- `internal/store/migrations/008_fact_history.sql` (new)
- `internal/memory/history.go` (new)

**Acceptance Criteria**:
- [ ] Migration creates fact_history table
- [ ] HistoryManager records ADD/UPDATE/DELETE
- [ ] GetFactAtTime() works for time-travel queries
- [ ] Rollback() restores previous versions

---

### Task 1.3: Add Access Tracking
**Priority**: P1
**Estimate**: 1 day
**Owner**: Backend

Track how often each fact is retrieved for smart consolidation.

**Files**:
- `internal/store/migrations/009_access_log.sql` (new)
- `internal/memory/access_log.go` (new)

**Acceptance Criteria**:
- [ ] fact_access_log table created
- [ ] Increment counter on each Search() hit
- [ ] GetAccessCount() returns frequency
- [ ] Periodic aggregation (hourly → daily)

---

### Task 1.4: Enable HNSW by Default
**Priority**: P0
**Estimate**: 2 days
**Owner**: Backend

Make sqlite-vss HNSW indexing the default, with graceful fallback.

**Files**:
- `internal/memory/sqlite_store.go` (modify)
- `internal/memory/vss.go` (modify)
- `configs/ironclaw.example.yaml` (modify)

**Acceptance Criteria**:
- [ ] HNSW enabled by default in config
- [ ] Automatic fallback if sqlite-vss unavailable
- [ ] Index auto-created on first search
- [ ] 10x speedup benchmark for 1K+ facts

---

## Phase 2: Smart Features (Week 3-4)

### Task 2.1: Implement SmartConsolidator
**Priority**: P1
**Estimate**: 3 days
**Owner**: Backend

Replace time-based consolidation with quality-based scoring.

**Files**:
- `internal/memory/smart_consolidator.go` (new)
- `internal/memory/consolidator.go` (deprecate)

**Acceptance Criteria**:
- [ ] ConsolidationScore with access/recency/importance
- [ ] LLM importance scoring (cached)
- [ ] Configurable weights
- [ ] A/B test shows >20% improvement in promoted fact quality

---

### Task 2.2: Memory-Graph Integration
**Priority**: P1
**Estimate**: 3 days
**Owner**: Backend

Automatically extract entities from facts and index in knowledge graph.

**Files**:
- `internal/memory/graph_integrator.go` (new)
- `internal/knowledge/graph/extractor.go` (modify)

**Acceptance Criteria**:
- [ ] Facts auto-extract entities on save
- [ ] Provenance links fact_id → graph nodes
- [ ] QueryKnowledge("X", depth) traverses graph
- [ ] Graph queries return source facts

---

### Task 2.3: Multi-Provider Embeddings
**Priority**: P2
**Estimate**: 2 days
**Owner**: Backend

Support multiple embedding providers with automatic fallback.

**Files**:
- `internal/memory/embedding_registry.go` (new)
- `internal/memory/voyage.go` (new)
- `internal/memory/ollama.go` (new)

**Acceptance Criteria**:
- [ ] EmbeddingRegistry with provider registration
- [ ] Support OpenAI, Voyage, Ollama
- [ ] Automatic fallback chain
- [ ] ModelID() for version tracking

---

### Task 2.4: Search Explainability
**Priority**: P2
**Estimate**: 2 days
**Owner**: Backend

Add debug mode that explains why facts were retrieved.

**Files**:
- `internal/memory/explainer.go` (new)
- `internal/memory/unified_store.go` (modify)

**Acceptance Criteria**:
- [ ] SearchExplanation struct with score breakdown
- [ ] BM25/Vector/RRF scores per result
- [ ] Latency metrics per stage
- [ ] Debug mode in config

---

## Phase 3: Migration & Cleanup (Week 5-6)

### Task 3.1: Data Migration Tool
**Priority**: P0
**Estimate**: 3 days
**Owner**: Backend

CLI tool to migrate from old storage to unified mode.

**Files**:
- `cmd/ironclaw/memory.go` (new)
- `internal/memory/migrator.go` (modify)

**Acceptance Criteria**:
- [ ] `ironclaw memory migrate` command
- [ ] Backup before migration
- [ ] Validate data integrity after
- [ ] Rollback on failure

---

### Task 3.2: Remove Dual Writes
**Priority**: P1
**Estimate**: 2 days
**Owner**: Backend

Clean up legacy code after migration.

**Files**:
- `internal/memory/sqlite_store.go` (modify)
- `internal/memory/store.go` (modify)

**Acceptance Criteria**:
- [ ] Remove Save() method (keep SaveFact only)
- [ ] Drop memories table in migration
- [ ] Update all callers
- [ ] Documentation updated

---

### Task 3.3: Configuration Overhaul
**Priority**: P1
**Estimate**: 1 day
**Owner**: Backend

Update config schema for new features.

**Files**:
- `configs/ironclaw.example.yaml` (modify)
- `internal/config/config.go` (modify)
- `docs/configuration.md` (update)

**Acceptance Criteria**:
- [ ] New memory.* config structure
- [ ] Backward compatibility for old configs
- [ ] Validation for invalid combinations
- [ ] Migration guide in docs

---

## Phase 4: Testing & Optimization (Week 7-8)

### Task 4.1: Performance Benchmarks
**Priority**: P0
**Estimate**: 2 days
**Owner**: Backend

Comprehensive benchmarks for all components.

**Files**:
- `internal/memory/benchmark_test.go` (expand)
- `docs/performance.md` (new)

**Acceptance Criteria**:
- [ ] Benchmark search at 1K/10K/100K facts
- [ ] Benchmark consolidation scoring
- [ ] Benchmark graph extraction
- [ ] Results documented with charts

---

### Task 4.2: Integration Tests
**Priority**: P0
**Estimate**: 3 days
**Owner**: Backend

End-to-end tests with real LLM calls.

**Files**:
- `internal/memory/integration_test.go` (new)

**Acceptance Criteria**:
- [ ] Test full lifecycle: extract → store → search → consolidate
- [ ] Test graph integration
- [ ] Test history & rollback
- [ ] Test migration path

---

### Task 4.3: Chaos Testing
**Priority**: P2
**Estimate**: 2 days
**Owner**: Backend

Test resilience to failures.

**Files**:
- `internal/memory/chaos_test.go` (new)

**Acceptance Criteria**:
- [ ] Simulate index corruption → rebuild from files
- [ ] Simulate embedding API failure → fallback
- [ ] Simulate disk full → graceful degradation
- [ ] Recovery procedures documented

---

### Task 4.4: Documentation
**Priority**: P1
**Estimate**: 2 days
**Owner**: Backend

Comprehensive docs for new features.

**Files**:
- `docs/memory-system.md` (new)
- `docs/migration-guide.md` (new)
- `README.md` (update)

**Acceptance Criteria**:
- [ ] Architecture diagrams
- [ ] Configuration examples
- [ ] Migration guide
- [ ] Troubleshooting section

---

## Phase 5: Advanced Features (Week 9-10, Optional)

### Task 5.1: Conflict Resolution UI
**Priority**: P3
**Estimate**: 3 days
**Owner**: Backend + Frontend

User confirmation for ambiguous lifecycle decisions.

**Files**:
- `internal/memory/lifecycle.go` (modify)
- `internal/channel/telegram/approval.go` (modify)

**Acceptance Criteria**:
- [ ] Confidence score in lifecycle decisions
- [ ] If confidence < 0.7, ask user
- [ ] Telegram inline keyboard for merge/keep-both/discard
- [ ] Learn from user choices

---

### Task 5.2: Incremental Embedding Updates
**Priority**: P3
**Estimate**: 2 days
**Owner**: Backend

Only re-embed changed chunks for large documents.

**Files**:
- `internal/memory/incremental.go` (new)

**Acceptance Criteria**:
- [ ] Content hashing to detect changes
- [ ] Chunk-level diff
- [ ] Only re-embed modified chunks
- [ ] Cost savings >50% for updates

---

### Task 5.3: OpenTelemetry Integration
**Priority**: P3
**Estimate**: 2 days
**Owner**: Backend

Add distributed tracing for observability.

**Files**:
- `internal/memory/tracing.go` (new)
- `go.mod` (add otel dependencies)

**Acceptance Criteria**:
- [ ] Spans for search pipeline stages
- [ ] Metrics: latency, cache hit rate, cost
- [ ] Export to Jaeger/Prometheus
- [ ] Dashboard examples

---

## Summary

**Total Estimate**: 8-10 weeks (40-50 days)

**Critical Path**:
1. UnifiedStore + History (Week 1-2)
2. SmartConsolidator + Graph (Week 3-4)
3. Migration Tool (Week 5)
4. Testing (Week 7-8)

**Dependencies**:
- Task 2.2 depends on 1.1 (UnifiedStore)
- Task 3.1 depends on all Phase 1 tasks
- Task 3.2 depends on 3.1 (migration)
- Task 4.x can run in parallel

**Risk Mitigation**:
- Feature flags for gradual rollout
- Comprehensive backups before migration
- Rollback procedures documented
- Canary testing with subset of users

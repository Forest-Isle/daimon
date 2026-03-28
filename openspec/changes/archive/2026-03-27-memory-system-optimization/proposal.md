# Memory System Optimization

## Overview

Optimize IronClaw's memory system by addressing architectural issues, improving performance, and aligning with industry best practices while maintaining the unique file-based approach.

## Problem Statement

Current memory system has several issues:
1. **Dual storage confusion** - SQLite + File storage with unclear boundaries
2. **Suboptimal consolidation** - Time-based instead of quality-based
3. **Disconnected graph** - Knowledge graph exists but not integrated with memory facts
4. **Optional HNSW** - Performance optimization not enabled by default
5. **Single embedding provider** - Locked to OpenAI
6. **No versioning** - Can't rollback or audit changes
7. **Limited observability** - Hard to debug search quality

## Goals

1. **Unify storage architecture** - Clear primary/secondary roles
2. **Smart consolidation** - Quality-based promotion with access tracking
3. **Integrate memory + graph** - Facts become graph nodes automatically
4. **Enable HNSW by default** - Fast vector search out of the box
5. **Multi-provider embeddings** - Support local models + cloud APIs
6. **Add fact history** - Immutable audit log with rollback
7. **Search explainability** - Debug why facts were retrieved

## Non-Goals

- Rewrite entire memory system from scratch
- Break backward compatibility with existing data
- Add distributed/multi-node support (local-first focus)

## Success Criteria

- Migration completes without data loss
- Search latency improves 10x for >1000 facts
- Consolidation promotes high-value facts (not just old ones)
- Graph queries work: "What does user know about X?"
- Can rollback to any previous memory state
- Search results include score explanations

## Risks

- Migration complexity for existing users
- HNSW dependency (sqlite-vss) may not be available everywhere
- LLM costs increase (importance scoring, graph extraction)
- Storage size grows (history table)

## Alternatives Considered

1. **Keep dual storage** - Rejected: maintenance burden too high
2. **SQLite-only** - Rejected: loses human-readable benefit
3. **File-only** - Rejected: query performance suffers
4. **No history** - Rejected: debugging/auditing impossible

## Decision

**Primary: File-based storage** (human-readable, git-friendly)
**Secondary: SQLite indexes** (fast queries, HNSW, FTS5)

Rationale: File storage aligns with local-first philosophy, SQLite provides performance.

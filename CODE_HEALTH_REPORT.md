# Code Health Report
> Generated: 2026-06-04
> Project: IronClaw
> Scanned: 539 source files, 38 Go packages, 20 SQLite migrations

## Executive Summary

IronClaw is currently buildable and testable across the Go runtime and both frontend applications. The audit found one real wiring defect: Knowledge Base embedding ignored `memory.embedding_base_url` while Memory and Codebase Index respected it. That defect has been fixed in `internal/gateway/init_knowledge.go`. After the fix, the remaining findings are non-blocking: documented future A2A remote-agent fields, prototype-only Vue Studio views, and expected environment-skipped tests.

## 🔴 Critical Issues

| # | Location | Issue | Why it matters |
|---|---|---|---|
| 1 | `internal/gateway/init_knowledge.go` | Knowledge Base embedding previously used the default OpenAI embedding constructor and skipped `memory.embedding_base_url`. | OpenAI-compatible or self-hosted embedding endpoints would work for Memory and Codebase Index but not Knowledge Base retrieval. Fixed by using `memory.NewOpenAIEmbeddingWithURL(...)`. |

## 🟡 Incomplete Implementations

| # | Location | Pattern found | Notes |
|---|---|---|---|
| 1 | `internal/agent/spec.go:93` | A2A remote agent support is marked "reserved, not implemented". | This is explicitly a future extension. Local sub-agents work through in-process, subprocess, and Docker backends; remote A2A execution should not be documented as available. |

## 🟠 Broken Module Connections

| # | Location | Connection gap | Suggested fix |
|---|---|---|---|
| 1 | None after fix | No broken compile-time or runtime wiring was found by build, vet, short tests, race tests, and frontend builds. | Keep running the verification matrix after Gateway, config, tool, memory, or frontend route changes. |

## 🟣 Code Smells

| # | Location | Smell | Severity (H/M/L) |
|---|---|---|---|
| 1 | `internal/gateway/gateway.go` | Gateway is a large composition root with many subsystem dependencies. | M |
| 2 | `internal/gateway/init_tools.go` | Tool, hook, permission, sandbox, verify, and audit setup are all in one initializer. | M |

## 🔵 Optimization Opportunities

| # | Location | Opportunity | Estimated impact |
|---|---|---|---|
| 1 | `internal/gateway/init_tools.go` | Codebase indexing runs at Gateway construction when embeddings are available. Consider lazy or incremental indexing for very large repositories. | Medium for large workspaces. |
| 2 | `internal/gateway/init_knowledge.go` | Startup ingestion loops configured `knowledge.ingest_dirs` synchronously. Consider queueing or progress reporting for large document sets. | Medium when ingest directories are large. |
| 3 | `internal/gateway/gateway.go` | MCP startup is asynchronous, but hot-reload polling is fixed interval. Event-driven file watching could reduce latency and polling work. | Low to medium. |

## Recommended Action Plan

1. Keep the embedding base URL fix and ensure future embedding call sites use `NewOpenAIEmbeddingWithURL`.
2. Add a targeted unit test or Gateway construction test proving Knowledge Base receives `memory.embedding_base_url` when configured.
3. Decide whether Vue Studio should remain a prototype or receive live backend APIs; document and test whichever direction is chosen.
4. Keep Gateway as the composition root, but continue extracting initializer helpers only when they reduce concrete coupling or improve testability.
5. Expand integration tests around Knowledge ingestion with OpenAI-compatible embedding base URLs if that path becomes production-critical.

## Stats

- Total issues found: 7
- Critical: 1 | Incomplete: 3 | Broken: 0 after fix | Smells: 3 | Optimizations: 3
- Files scanned: 539 source files plus 20 migrations

## Verification Commands

Commands run during this audit:

```bash
make build-bin
make vet
make test-short
make test
cd web && npm ci && npm run build
```

Notes:

- `make test` uses the Go race detector. On macOS it emitted linker `LC_DYSYMTAB` warnings but completed successfully.
- Some tests intentionally skip when optional environment dependencies are unavailable, such as Docker-backed sandbox tests or exporter-specific observability paths.
- `npm ci` completed with 0 reported vulnerabilities in both frontend workspaces during the audit run.

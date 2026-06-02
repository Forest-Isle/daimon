# IronClaw Optimization Roadmap
> Generated: 2026-06-03  
> Based on: Industry Research (12 web searches), Code Health Scan (552 files), Competitive Analysis

---

## Project Snapshot

IronClaw is a local-first, self-evolving AI agent runtime written in Go. It's not a library — it's a full **agent operating system**: LLM providers → tools → interceptor chain → memory → knowledge → evolution → channels. The architecture is genuinely ambitious and, in several dimensions, already ahead of most 2026 frameworks:

- **Strengths**: 5-phase cognitive loop (PERCEIVE→PLAN→ACT→OBSERVE→REFLECT) is more sophisticated than most frameworks' ReAct loops; the evolution engine (preference learning, strategy optimization, skill synthesis) is rare in open source; MCP+A2A dual protocol support; file-first memory with hybrid retrieval; WASM plugin sandbox; feature registry with hot-reload.
- **Gaps**: Incomplete RL removal leaves dead artifacts; observability is present but test coverage is environment-dependent; no Agent-as-Tool composability (exposing agents as tools to other agents); context compression could use memorywire-style standardization; memory system lacks temporal fact modeling (validity windows); no agent gateway/control plane for multi-tenant production deployments.

## Competitive Landscape

The Go agent framework ecosystem in 2026 has rapidly matured. **Google's ADK Go** (v1.2.0, March 2026) is the most production-ready alternative with self-healing plugins, multi-agent patterns, and A2A protocol support. **GoMind** (v0.9.1) offers AI-driven dynamic orchestration with Redis-based service discovery — the closest to "self-organizing agent networks." **Lattice** (`go-agent` v1.11.4) provides agent-as-tool composability with shared memory spaces. **FlowCraft** (v0.1.0, May 2026) mirrors IronClaw's layered architecture (Kanban delegation, hybrid memory, checkpointing) but is in early development.

Compared to Python-dominant frameworks (LangGraph ~31k★, CrewAI ~51k★), IronClaw's Go-native approach gives it unique advantages: single-binary deployment, zero Python/Docker dependency, CGO SQLite for embedded persistence, and true concurrency via goroutines. The **gap** isn't features — it's polish, documentation, and community. IronClaw's cognitive loop + evolution engine is architecturally ahead of most Python frameworks' linear ReAct loops. But LangGraph's state-machine approach enables debugging/replay that IronClaw doesn't match yet, and CrewAI's role-based orchestration is more intuitive for non-programmers.

**Positioning**: IronClaw is uniquely positioned as the **self-evolving Go agent runtime** — no other framework combines cognitive loop, evolution engine, hybrid memory, knowledge graph, and MCP+A2A in a single Go binary. The opportunity is to double down on this differentiation while closing the observability and developer-experience gaps.

## Gap Analysis Table

| Dimension | Current State | Industry Standard (2026) | Gap | Priority |
|-----------|--------------|--------------------------|-----|----------|
| **Agent Loop** | Simple + 5-phase Cognitive | ReAct (default), Plan-Execute, Reflexion, ToT | ✅ Ahead | — |
| **Evolution/Self-Improvement** | Preference learning, strategy optimization, skill synthesis | Mostly absent; ADK Go has self-healing plugins only | ✅ Ahead | — |
| **Memory Architecture** | File-first, hybrid BM25+vector, RRF fusion, 3 scopes | Temporal KGs (Zep), OS-tiered (Letta), multi-strategy (Hindsight) | 🟡 Gap: no temporal fact modeling | High |
| **Observability** | OTel tracing + metrics, Prometheus endpoint, dashboard events | OTel-GenAI semantic conventions, OpenInference span kinds, eval-as-span-attribute | 🟡 Gap: no GenAI semantic conventions | Medium |
| **Multi-Agent** | SubAgentManager, TeamCoordinator, Orchestrator | Agent-as-Tool (Lattice), hierarchical supervisor (LangGraph), debate (ADK Go) | 🟢 Competitive | Low |
| **Protocol Support** | MCP client + A2A server | MCP universal, A2A growing (150+ orgs) | 🟢 Ahead of most Go frameworks | Low |
| **Security Sandbox** | Docker per-session, FileGuard, NetworkPolicy, interceptor chain | Agent gateways (Solo.io), Kyverno policies, identity-aware MCP | 🟡 Gap: no agent gateway concept | Medium |
| **Eval Framework** | Suite runner, multi-dimensional scoring, longitudinal, adaptive | LLM-as-judge (standard), CI regression gates, persona-driven simulation | 🟢 Competitive | Low |
| **Context Compression** | 5-layer pipeline, reactive 413 retry, prompt cache splitting | LangGraph's state reduction, Letta's self-editing context | 🟢 Competitive | Low |
| **Tool System** | 20+ built-in tools, typed schemas, interceptor chain | Typed function calls (Pydantic/JSON Schema), MCP tools, WASM plugins | 🟢 Competitive | Low |
| **Config & DX** | YAML config, env var expansion, hot-reload | Declarative YAML agents (ADK Go), config validation | 🟡 Gap: no config validation, dead RL keys | High |
| **Deployment** | Single binary, Docker, Makefile | K8s operators, Helm charts, distroless images | 🟡 Gap: no Helm chart, Dockerfile stale | Medium |
| **Documentation** | CLAUDE.md (thorough), architecture docs, OpenSpec specs | README-driven, API reference, quickstart, architecture decision records | 🟡 Gap: no public-facing README polish | Medium |
| **Community** | Single maintainer (wuqisen) | ADK Go (Google-backed), LangGraph (31k★), CrewAI (51k★) | 🔴 Gap: solo project | Critical (long-term) |
| **Production Readiness** | Rate limiting, health checks, graceful shutdown | Circuit breakers, feature flags, canary deploys, SLA monitoring | 🟡 Gap: no circuit breakers on LLM calls | Medium |

## Optimization Roadmap

### Horizon 1 — Quick Wins (Days to 2 weeks)
*Low-effort, high-impact. Fix the most glaring issues first.*

---

**1. Purge RL System Completely**
- **What**: Delete `007_rl_system.sql` migration, remove ~15 RL config keys from `ironclaw.yaml` and `ironclaw.example.yaml`, audit `cmd/ironclaw/training.go` and `internal/eval/training_export.go` for dead RL references.
- **Why it matters**: Every fresh IronClaw install creates 6 dead SQLite tables. Users editing RL config keys get silent no-ops. This is the #1 code health issue and it's purely cleanup — zero risk.
- **How**:
  1. Delete `internal/store/migrations/007_rl_system.sql`
  2. Add a migration `022_drop_rl_tables.sql` that drops RL tables on upgrade
  3. Remove RL keys from both config YAML files
  4. Audit `config.go` for any RL struct fields (none found in scan, but verify)
  5. Verify `training.go` → `training_export.go` → if RLHF format is dead, remove or repurpose
- **Reference**: Commit `5fa1b49` removed the Go code but intentionally left the migration — now is the time to finish.

**2. Fix Dockerfile & Add Distroless**
- **What**: Update builder to `golang:1.25-bookworm`, switch final stage to `gcr.io/distroless/static-debian12`.
- **Why it matters**: Go 1.25 toolchain directives will fail on Go 1.23. Distroless reduces attack surface to near-zero.
- **How**:
  ```dockerfile
  FROM golang:1.25-bookworm AS builder
  # ... build as before ...
  FROM gcr.io/distroless/static-debian12
  COPY --from=builder /out/ironclaw /ironclaw
  COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
  USER 65532:65532
  ENTRYPOINT ["/ironclaw"]
  ```
- **Reference**: [Google Distroless](https://github.com/GoogleContainerTools/distroless), standard for Go production images.

**3. Add Config Validation**
- **What**: Post-unmarshal step in `config.go` that warns about unrecognized top-level YAML keys.
- **Why it matters**: Prevents users from spending hours tuning dead config keys that silently do nothing.
- **How**: Use `yaml.v3`'s `KnownFields` decode mode, or add a simple `slog.Warn` loop comparing YAML keys against known struct tags.
- **Reference**: ADK Go's config validation pattern.

**4. Commit `.golangci.yml`**
- **What**: Standard Go linter config with `gocyclo`, `gocognit`, `dupl`, `funlen` tuned to project norms.
- **Why it matters**: CI already runs `golangci-lint` but uses defaults. Explicit config communicates code standards to contributors.
- **How**: Enable `errcheck`, `gosec`, `govet`, `staticcheck`; set `funlen` to 120 lines; exclude test files from `dupl`.

---

### Horizon 2 — Structural Improvements (Weeks to 1-2 months)
*Architectural changes that require planning but deliver compounding benefits.*

---

**5. Add Temporal Fact Modeling to Memory**
- **What**: Add `valid_from` and `valid_to` timestamp columns to `memory_index`; implement soft invalidation on contradiction; add temporal filter to hybrid search.
- **Why it matters**: The 15-point LongMemEval gap between flat vector stores and temporal KGs (Zep 63.8% vs Mem0 49.0%) is architectural. IronClaw's memory already has file versioning and archiving — adding validity windows is a schema extension, not a rewrite. This directly improves the agent's ability to reason about changing facts ("Alice used to prefer Python, now prefers Go").
- **How**:
  1. Migration: `ALTER TABLE memory_index ADD COLUMN valid_from DATETIME; ALTER TABLE memory_index ADD COLUMN valid_to DATETIME;`
  2. Create partial unique index: `CREATE UNIQUE INDEX idx_memory_active_fact ON memory_index(memory_id) WHERE valid_to IS NULL`
  3. Update `file_store.go` search to filter `valid_to IS NULL` by default
  4. Add `memorywire`-compatible wire format for remember/recall/forget/merge/expire operations
- **Reference**: [Zep/Graphiti](https://github.com/getzep/graphiti) temporal knowledge graph, [memorywire](https://arxiv.org/html/2606.01138v1) wire format spec.

**6. Implement Agent-as-Tool Composability**
- **What**: Allow any agent spec (`.ironclaw/agents/*.md`) to be registered as a tool callable by other agents — not just via `TeamCoordinator` but directly in the tool registry.
- **Why it matters**: Lattice's agent-as-tool pattern and LangGraph's subgraph pattern are becoming the standard way to build hierarchical agent systems. IronClaw already has `AgentTool` and `SubAgentManager` — this is about making agent tools first-class in the tool registry with proper schema generation and streaming passthrough.
- **How**:
  1. Add `AsTool()` method to `AgentSpec` that returns a `tool.Tool` with auto-generated JSON schema from the agent's description
  2. Register agent tools alongside built-in tools in `initToolsAndHooks()`
  3. Sub-agent streaming results pass through to the parent's channel
- **Reference**: [Lattice go-agent](https://github.com/Protocol-Lattice/go-agent) composability model.

**7. Decompose Monolith Files**
- **What**: Split `cognitive_loop.go` (1182 lines) into `cognitive_orchestrator.go` + `cognitive_debate.go` + `cognitive_checkpoint.go`. Split `gateway.go` (1098 lines) constructor into grouped subsystem initializers with parallel init where dependencies allow.
- **Why it matters**: These two files are the project's spinal cord — every new feature touches them. At 1100+ lines each, they're past the point where a single developer can hold the full flow in working memory. Decomposition reduces bug surface and makes the Phase 3 A2A backend integration straightforward.
- **How**:
  - `cognitive_loop.go` → core loop (PERCEIVE→PLAN→ACT→OBSERVE→REFLECT orchestration, ~400 lines) + `cognitive_debate.go` (~200 lines) + `cognitive_checkpoint.go` (~150 lines) + `cognitive_events.go` (~100 lines)
  - `gateway.go` → `Gateway` struct + `New()` constructor (~300 lines) with calls to grouped init methods in separate files (already partially done with 12 `init_*.go` files)
- **Reference**: Google ADK Go's agent decomposition pattern.

**8. Add OpenInference Span Kinds & OTel-GenAI Semantics**
- **What**: Annotate existing OTel spans with `gen_ai.*` semantic conventions and OpenInference span kinds (`LLM`, `TOOL`, `CHAIN`, `RETRIEVER`, `AGENT`, `RERANKER`, `GUARDRAIL`).
- **Why it matters**: IronClaw already has OTel tracing. Adding the semantic layer makes traces queryable by any OTel-GenAI-compatible backend (Grafana, Arize, LangSmith). This is ~100 lines of span attribute additions for a massive observability upgrade.
- **How**:
  - Add `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` to LLM call spans
  - Tag tool spans with `openinference.span.kind = "TOOL"`
  - Tag cognitive loop spans with `openinference.span.kind = "AGENT"`
- **Reference**: [OpenInference semantic conventions](https://github.com/Arize-AI/openinference), [OTel GenAI spec](https://github.com/open-telemetry/semantic-conventions).

---

### Horizon 3 — Production-Grade Hardening (Months)
*What separates good projects from great ones — long-term investments.*

---

**9. Agent Gateway / Control Plane**
- **What**: A new `internal/gateway/agent_gateway.go` that sits in front of agent execution, providing: MCP traffic termination, A2A traffic termination, centralized auth (API keys, OAuth, mTLS), rate limiting per agent/user, tool access policies, audit logging, cost tracking per trace.
- **Why it matters**: The 2026 MCP Dev Summit made clear that agent gateways are the emerging production pattern — terminating protocol traffic, centralizing policy enforcement, and generating evidence for compliance. IronClaw's interceptor chain already has the right architecture; this extends it to the protocol boundary.
- **Reference**: [Solo.io agent gateway](https://www.solo.io/), [MCP Dev Summit 2026 governance track](https://futurumgroup.com/insights/mcp-dev-summit-2026-aaif-sets-a-clear-direction-with-disciplined-guardrails/).

**10. Circuit Breakers & Resilience**
- **What**: Add circuit breakers to LLM provider calls (3 consecutive failures → open circuit for 30s), retry with jitter on transient HTTP errors, deadline propagation from parent context.
- **Why it matters**: LLM APIs fail in bursts (rate limits, provider outages). Without circuit breakers, a provider outage causes cascading failures across all sessions. IronClaw has `RetryProvider` but no circuit breaker — the retry loop itself becomes a DDoS against a failing upstream.
- **How**: Use `gobreaker` or a simple in-memory circuit breaker with 3 states (closed → open → half-open).
- **Reference**: [Netflix Hystrix](https://github.com/Netflix/Hystrix) pattern, ADK Go's built-in retry plugin.

**11. Helm Chart + K8s Operator**
- **What**: Production deployment artifacts: Helm chart with configurable IronClaw deployment, optional K8s operator for managing agent lifecycles.
- **Why it matters**: GoMind's K8s-native deployment and ADK Go's Cloud Run support set the bar. IronClaw's single binary makes it trivially containerizable — a Helm chart is the minimum viable production deployment story.
- **Reference**: [GoMind's K8s deployment](https://github.com/itsneelabh/gomind), ADK Go's Cloud Run quickstart.

**12. memorywire Protocol Compatibility**
- **What**: Implement the memorywire vendor-neutral wire format for cross-agent memory operations. Add MCP server that exposes memory operations via memorywire protocol.
- **Why it matters**: memorywire is to memory what MCP is to tools — a protocol for interoperability. As the three-protocol stack (MCP + A2A + memorywire) standardizes, IronClaw should speak all three natively.
- **Reference**: [memorywire arXiv paper](https://arxiv.org/html/2606.01138v1).

**13. Eval-as-Span-Attribute**
- **What**: Write evaluation scores back onto OTel spans as `gen_ai.evaluation.*` attributes. Enable tail-based sampling: keep 100% of spans with poor eval scores, sample 10% of clean traces.
- **Why it matters**: The canonical 2026 observability pattern. Without eval-on-span, debugging requires correlating separate eval and trace systems. With it, a single TraceQL query finds all poor-quality traces.
- **Reference**: [FutureAGI eval-as-span-attribute pattern](https://futureagi.com/blog/llm-eval-vs-llm-observability-2026/).

---

## Priority Call

**If you only do three things, do these:**

1. **Purge RL completely** (Horizon 1, Item 1) — Clean up the dead migration, dead config keys, and dead CLI command. This is zero-risk cleanup that removes confusion for every user and eliminates 6 dead SQLite tables from every fresh install.

2. **Add temporal fact modeling to memory** (Horizon 2, Item 5) — This is IronClaw's biggest architectural gap vs. the state of the art. Temporal knowledge graphs deliver 15+ point gains on LongMemEval benchmarks. The schema change is a migration, not a rewrite — and it compounds with the evolution engine (evolved preferences are inherently temporal).

3. **Add OpenInference span kinds** (Horizon 2, Item 8) — ~100 lines of span attributes unlocks compatibility with the entire 2026 observability ecosystem. Grafana, Arize, LangSmith, traceAI — all speak OTel-GenAI + OpenInference. This is the highest-leverage observability investment possible.

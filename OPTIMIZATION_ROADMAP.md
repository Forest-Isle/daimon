# Optimization Roadmap

This roadmap reflects the current source state after the documentation rewrite and health audit.

## Done in This Pass

- Replaced stale documentation with source-derived architecture and module docs.
- Fixed Knowledge Base embedding base URL wiring.
- Verified Go build, vet, short tests, race tests, and both frontend builds.

## Near-Term

1. Add a targeted test for Knowledge Base embedding configuration.
   - Goal: prove `memory.embedding_base_url` is used by Knowledge Base initialization.
   - Area: `internal/gateway/init_knowledge.go` and related test seam.

2. Clarify Vue Studio product direction.
   - Option A: keep it as a prototype and label demo state in UI/docs.
   - Option B: add real backend APIs for Prompt IDE save/test, Memory Explorer search, and Flow Editor persistence.

3. Improve startup cost for large repositories.
   - Codebase indexing currently starts during Gateway tool initialization when embeddings are configured.
   - Consider lazy indexing, incremental indexing, or explicit CLI trigger for large workspaces.

4. Improve Knowledge ingestion ergonomics.
   - `knowledge.ingest_dirs` is ingested at Gateway startup.
   - Large document sets would benefit from background queueing, progress events, and per-source status.

## Medium-Term

1. More integration coverage for OpenAI-compatible deployments.
   - LLM provider base URL.
   - Memory embedding base URL.
   - Knowledge embedding base URL.
   - Codebase Index embedding base URL.

2. Event-driven MCP config reload.
   - Current Gateway watches `~/.IronClaw/mcp/` by polling.
   - File watching would reduce reload latency and background work.

3. Gateway initializer test seams.
   - Keep Gateway as the explicit composition root.
   - Add narrow constructor options or dependency interfaces only where tests need to observe wiring without external services.

4. Dashboard route/API contract tests.
   - Dashboard REST routes are small and testable.
   - Add frontend API contract fixtures when adding new dashboard views.

## Long-Term

1. Decide on remote A2A agent execution.
   - Current `AgentSpec.Remote` is reserved.
   - Implement only with clear auth, timeout, permission, audit, and error semantics.

2. Productionize Studio if it becomes a supported surface.
   - Backend APIs.
   - Auth.
   - Persisted flows/prompts.
   - Runtime execution previews.
   - Tests for store and UI behavior.

3. Formalize tool capability taxonomy.
   - New tools already expose capabilities.
   - More detailed capabilities could improve permission defaults, parallel scheduling, and sandbox policy.

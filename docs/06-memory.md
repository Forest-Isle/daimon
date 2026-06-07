# 06. Memory

Memory stores user/session/runtime facts and profile information with a SQLite-backed index and optional embeddings.

## Memory Initialization

`internal/gateway/init_memory.go` runs when the `memory` feature is enabled.

```mermaid
flowchart TB
    Config[memory config] --> Embedder{OpenAI API key?}
    Embedder -- yes --> Cached[Cached OpenAI-compatible embedder]
    Embedder -- no --> Noop[Noop embedder]
    Cached --> Store[FileMemoryStore]
    Noop --> Store
    Store --> Cache{Search cache enabled?}
    Cache -- yes --> CachedStore[CachedStore wrapper]
    Cache -- no --> Deps[AgentDeps.Memory]
    CachedStore --> Deps
    Deps --> Tools[memory_manage]
    Deps --> Lifecycle{Fact extraction?}
    Lifecycle -- yes --> Facts[LLM fact extractor]
    Facts --> Reflect[Reflection tracker]
    Reflect --> Profiler[Profiler]
    Facts --> Compactor[Compactor]
    Store --> Consolidator[Session to user promotion]
    Store --> Retention[Forgetting curve and retention task]
```

Key behaviors:

- Storage dir defaults to `~/.IronClaw/memory`.
- `~/` prefixes in `memory.storage_dir` are expanded.
- Embeddings use `memory.openai_api_key`, `memory.embedding_model`, and `memory.embedding_base_url`.
- Search cache is optional.
- Fact extraction enables LLM fact extraction, lifecycle manager, reflection tracker, compactor, profiler, and audit logger.
- Consolidator runs regardless of fact extraction and promotes session facts to user scope.
- Retention/fade logic runs daily until Gateway stop.

## Memory Tools and AMP

Gateway registers:

- `memory_manage` after `FileMemoryStore` creation.
- `core_memory` after memory initialization when memory store exists.
- AMP memory tool through `memorywire.NewAdapter`, supporting standardized remember/recall/forget/merge/expire style operations.

The agent also reads memory passively during prompt construction and writes the user message to memory after handling a request.

## Prompt Memory Use

```mermaid
flowchart LR
    UserText[User text] --> Search[Memory search]
    Search --> Relevant[Relevant memories]
    BaseDir[Memory files] --> Profile[Profile sections]
    Profiler[Profiler] --> ColdStart[Cold-start prompt]
    Relevant --> Prompt[System prompt]
    Profile --> Prompt
    ColdStart --> Prompt
```

The prompt excludes `profile` memory type from general relevant memory search, then loads profile sections separately.

## Unified Retrieval

Gateway can construct a unified retriever (the "cortex") that fuses two sources:

- the memory store, and
- procedural memory.

```mermaid
flowchart LR
    Query[Retrieval query] --> MemSearch[Memory store search]
    Query --> ProcSearch[Procedural memory search]
    MemSearch --> Fusion[UnifiedRetriever fusion]
    ProcSearch --> Fusion
    Fusion --> Prompt[Prompt injection]
```

`FusionWeights` exposes `MemoryWeight` and `ProceduralWeight`. This gives the Agent a single memory/cortex style retrieval surface while retaining separate storage responsibilities.

The prompt injector emits memory-derived sections (relevant memories, profile, and the memory taxonomy's `## Knowledge Context` block built from semantic memory).

## Current Fixed Wiring

Memory embedding uses the OpenAI-compatible embedding base URL config (`memory.embedding_base_url`). This matters for deployments using relays, local embedding services, or non-default OpenAI-compatible endpoints.

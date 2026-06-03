# 06. Memory, Knowledge, and Graph

IronClaw has two related but distinct retrieval systems:

- Memory stores user/session/runtime facts and profile information.
- Knowledge ingests external documents into searchable chunks.

Knowledge Graph can connect both systems through entity/relation extraction.

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
- `core_memory` after Knowledge initialization when memory store exists.
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

## Knowledge Base

`internal/gateway/init_knowledge.go` runs after Memory and evolution/plan initialization.

```mermaid
flowchart TB
    KConfig[knowledge config] --> KBEmbedder{memory.openai_api_key?}
    KBEmbedder -- yes --> OpenAIURL[OpenAI-compatible embedding with base URL]
    KBEmbedder -- no --> NoopKB[Noop KB embedder]
    OpenAIURL --> KB[knowledge.New]
    NoopKB --> KB
    KB --> Reranker{reranker enabled + provider llm?}
    Reranker -- yes --> LLMRerank[LLM reranker]
    Reranker -- no --> NoopRerank[Noop reranker]
    LLMRerank --> Hybrid[HybridRetriever]
    NoopRerank --> Hybrid
    Hybrid --> AgentMemory[gw.memory.kbSearcher]
    KB --> Ingest[Ingest configured dirs]
```

Knowledge config controls:

- `chunk_size`
- `chunk_overlap`
- `bm25_weight`
- `vector_weight`
- `ingest_dirs`
- optional search cache
- optional reranker

If no embedding key is configured, Knowledge falls back to text/BM25-style search through the no-op embedder path.

## Knowledge Ingestion

The Knowledge package has ingestion support for:

- Code
- Markdown
- Plain text
- PDF
- Web/content ingestion paths

Pipeline chunking and storage live under `internal/knowledge`. At startup, Gateway loops over configured `knowledge.ingest_dirs` and calls `kb.GetPipeline().IngestDir(...)`.

For large directories, this startup path is functional but can delay startup. The roadmap recommends queueing/progress events if large knowledge corpora become common.

## Knowledge Graph

When `knowledge_graph` is enabled, Gateway:

1. Creates `graph.NewSQLiteGraph(gw.db)`.
2. Stores it in `gw.memory.graphStore`.
3. Creates an LLM entity extractor.
4. Starts background extraction from already-ingested KB chunks.
5. Wires `GraphSync` into Memory lifecycle manager when lifecycle exists.
6. Starts graph decay task every 24 hours.

```mermaid
flowchart LR
    KBChunks[Knowledge chunks] --> Extractor[LLM entity extractor]
    Memories[Memory lifecycle events] --> GraphSync[GraphSync]
    Extractor --> Graph[(SQLite graph)]
    GraphSync --> Graph
    Graph --> Decay[Graph decay task]
    Graph --> Retriever[UnifiedRetriever]
```

## Unified Retrieval

After Knowledge initializes, Gateway can construct:

- procedural memory store,
- unified retriever combining memory store,
- KB searcher,
- graph store,
- procedural store,
- shared embedder.

This gives the Agent a single memory/cortex style retrieval surface while retaining separate storage responsibilities.

## Current Fixed Wiring

Knowledge Base embedding now uses the same OpenAI-compatible embedding base URL config as Memory. This matters for deployments using relays, local embedding services, or non-default OpenAI-compatible endpoints.

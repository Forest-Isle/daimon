# Memory System

IronClaw's memory system is designed around cognitive science principles, using a **file-first architecture** that stores memories as Markdown files with YAML frontmatter. SQLite serves as an auxiliary index for fast hybrid search, not as the primary store.

## Why File-First?

Most agent frameworks store memories in vector databases or key-value stores — opaque binary data that you can't inspect, version, or manually edit. IronClaw takes a different approach:

| Property | IronClaw (File-first) | Typical Vector Store |
|----------|----------------------|---------------------|
| **Inspectable** | Open any `.md` file in your editor | Need custom tooling to read embeddings |
| **Version control** | `git diff` on memory changes | Requires export/import |
| **Manual editing** | Edit YAML frontmatter or content directly | Usually not supported |
| **Backup** | `cp -r ~/.ironclaw/memory/ backup/` | Database-specific dump |
| **Portability** | Plain text files work everywhere | Tied to specific DB engine |
| **Search** | FTS5 + vector hybrid via SQLite index | Vector-only or keyword-only |

## Memory Types (Cognitive Science Foundation)

Memories are classified into three types inspired by cognitive psychology's multi-store model:

### Episodic Memory
What happened — specific events, conversations, tool outcomes. Short-lived by default, promoted to long-term if accessed frequently.

```yaml
---
id: ep_20260418_abc123
scope: session
created_at: 2026-04-18T10:30:00Z
strength: 0.85
---
User asked to deploy the staging environment. Used bash to run
`kubectl apply -f staging/`. Deployment succeeded after fixing
a typo in the service name.
```

### Semantic Memory
What is known — facts, preferences, patterns extracted from episodes. Higher default stability, slower decay.

```yaml
---
id: sem_20260418_def456
scope: user
created_at: 2026-04-18T10:35:00Z
strength: 0.92
---
User prefers kubectl over helm for Kubernetes deployments.
Staging namespace is `staging-v2`, not the default `staging`.
```

### Procedural Memory
How to do things — learned sequences, tool combinations, best practices. Highest stability, rarely forgotten.

```yaml
---
id: proc_20260418_ghi789
scope: global
created_at: 2026-04-18T10:40:00Z
strength: 0.95
---
To deploy to staging: (1) verify kubeconfig context, (2) run dry-run
first, (3) apply with `--prune` flag, (4) verify pod status.
```

## Storage Architecture

```
~/.ironclaw/memory/
├── MEMORY.md            # Index: one-line description per active memory
├── session/             # Current session memories (auto-promoted or archived)
│   └── episodic_20260418_abc123.md
├── user/                # Long-term per-user memories
│   └── semantic_20260418_def456.md
├── global/              # Cross-user knowledge
│   └── procedural_20260418_ghi789.md
├── feedback/            # User feedback signals
└── archived/            # Memories below strength threshold
```

**MEMORY.md** acts as a fast-read index — the agent scans it first to decide which full files to load, avoiding unnecessary disk reads.

## Forgetting Curve

Memory strength decays over time following a modified Ebbinghaus curve. Each memory type has different stability:

- **Episodic**: Fastest decay (stability ~0.3). Specific events fade unless reinforced.
- **Semantic**: Moderate decay (stability ~0.6). Facts persist longer.
- **Procedural**: Slowest decay (stability ~0.9). Learned procedures are retained.

**Strength formula**: `strength = importance * e^(-t / (stability * scale))`

Where `t` is time since last access, `importance` is derived from metadata (access frequency, user feedback), and `scale` is a configurable constant.

**Auto-archival**: A background task runs every 24 hours. Memories with `strength < 0.3` are moved to `archived/` and removed from the active index.

## Consolidation

Session memories don't live forever in the `session/` directory:

1. **Promotion**: Session memories older than 24h with `strength >= 0.5` are promoted to `user/` scope (file physically moved from `session/` to `user/`).
2. **Conflict detection**: If a file with the same name already exists in `user/`, a `_v2` suffix is appended to prevent silent data loss.
3. **Archival**: Session memories that fall below the strength threshold are archived instead of promoted.

## Hybrid Search Pipeline

When the agent needs to recall relevant memories:

```
Query
  │
  ├─→ FTS5 full-text search (BM25 scoring)
  │
  ├─→ Vector embedding search (cosine similarity)
  │
  └─→ MEMORY.md index scan (keyword match)
        │
        ▼
    RRF Fusion (Reciprocal Rank Fusion)
        │
        ▼
    Strength-weighted reranking
        │
        ▼
    Top-K results → read full .md files
```

**RRF fusion** combines ranked lists from different retrieval methods without requiring score normalization, producing robust results even when one method underperforms.

## Lifecycle Management

An LLM-driven lifecycle manager evaluates each new piece of information:

| Decision | Action |
|----------|--------|
| **ADD** | Create new memory file with appropriate scope and type |
| **UPDATE** | Archive old version to `archived/`, create new version |
| **DELETE** | Move to `archived/` |
| **NOOP** | Information already captured or not worth storing |

Conflict detection prevents duplicate memories about the same topic. The lifecycle manager checks existing memories before adding new ones.

## Comparison with Other Systems

| Feature | IronClaw | LangChain Memory | Mem0 | MemGPT |
|---------|----------|-----------------|------|--------|
| Storage format | Markdown files | In-memory / vector DB | Vector DB | JSON + vector |
| Inspectable | Yes (plain text) | Partial | No | Partial |
| Git-friendly | Yes | No | No | No |
| Memory types | Episodic/Semantic/Procedural | Buffer/Summary/Entity | Flat | Core/Archival |
| Forgetting curve | Yes (type-aware decay) | No | No | Partial |
| Auto-consolidation | Yes (session→user promotion) | No | No | Yes |
| Hybrid search | FTS5 + vector + RRF | Vector-only | Vector + keyword | Vector-only |
| Conflict detection | Yes | No | No | No |
| Offline access | Yes (files on disk) | Requires running process | Requires API | Requires running process |

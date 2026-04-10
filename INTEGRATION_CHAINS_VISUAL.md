# IronClaw Integration Chains - Visual Architecture

---

## 1. AGENT CHAIN: Complete Message Flow

```
┌────────────────────────────────────────────────────────────────────────────┐
│                         EXTERNAL MESSAGE SOURCES                           │
├────────────────────────────────────────────────────────────────────────────┤
│  Telegram   │  TUI    │  Discord  │  Slack   │  [Custom Channels]         │
└─────┬──────┴────┬────┴───────────┴────┬─────┴──────────────────────────────┘
      │           │                    │
      └───────────┴────────────────────┘
                  │
          [Channel.Start(handler)]
                  │
                  ↓
    ┌─────────────────────────────────┐
    │   Gateway.handleInbound()        │  ← Routes incoming messages
    │                                 │
    │  • Validates channel exists     │
    │  • Handles /new & /start cmds   │
    │  • Routes to agent              │
    └────────┬────────────────────────┘
             │
             ├─────────[Cognitive Agent enabled?]─────────┐
             │ YES                                       │ NO
             ↓                                           ↓
    ┌──────────────────────────┐           ┌──────────────────────────┐
    │  CognitiveAgent          │           │  Runtime                 │
    │  .HandleMessage()        │           │  .HandleMessage()        │
    │                          │           │                          │
    │  Entry point for 5-phase │           │  Simple task mode        │
    │  cognitive loop          │           │  LLM → Tools → Response  │
    └────────┬─────────────────┘           └──────────┬───────────────┘
             │                                        │
             ↓                                        │
    ┌────────────────────────────────────┐           │
    │  PERCEIVE PHASE                    │           │
    ├────────────────────────────────────┤           │
    │  • Parse user goal                 │           │
    │  • Assess complexity               │           │
    │  • Search memories (session/user)  │           │
    │  • Query knowledge base            │           │
    │  • Query knowledge graph           │           │
    │  → CognitiveState                  │           │
    └────────┬─────────────────────────────┘         │
             │                                        │
    ┌────────┴─────────────────────────┐             │
    │ Is complexity == SIMPLE?        │             │
    │   YES → Delegate to Runtime ────┼─────────────┘
    │   NO  → Continue to PLAN        │
    └────────┬────────────────────────┘
             │
             ↓
    ┌────────────────────────────────────┐
    │  PLAN PHASE                        │
    ├────────────────────────────────────┤
    │  • LLM reasoning                   │
    │  • Generate TaskPlan               │
    │    - Goals                         │
    │    - Tools needed                  │
    │    - Steps                         │
    │    - Confidence score              │
    │  • Check for DirectReply           │
    │  • RL: PPO strategy adjustment     │
    │  • Replan if confidence low (2x)   │
    └────────┬────────────────────────────┘
             │
    ┌────────┴─────────────────────────┐
    │ DirectReply set?                 │
    │   YES → Stream & exit ───────────┼───┐
    │   NO  → Continue to ACT          │   │
    └────────┬────────────────────────┘    │
             │                              │
             ↓                              │
    ┌────────────────────────────────────┐  │
    │  ACT PHASE                         │  │
    ├────────────────────────────────────┤  │
    │  • Execute tool calls              │  │
    │  • LLM tool-use loop:              │  │
    │    - Model generates tool call     │  │
    │    - Execute tool via Registry     │  │
    │    - Feed result back to LLM       │  │
    │    - Iterate until complete       │  │
    │  • Collect observations            │  │
    │  • RL: Update state during exec   │  │
    │  → Observations[]                  │  │
    └────────┬────────────────────────────┘  │
             │                              │
             ↓                              │
    ┌────────────────────────────────────┐  │
    │  OBSERVE PHASE                     │  │
    ├────────────────────────────────────┤  │
    │  • Aggregate tool results          │  │
    │  • Calculate success count         │  │
    │  • Calculate failure count         │  │
    │  • Assess overall progress         │  │
    │  → ObservationResult               │  │
    └────────┬────────────────────────────┘  │
             │                              │
             ↓                              │
    ┌────────────────────────────────────┐  │
    │  REFLECT PHASE                     │  │
    ├────────────────────────────────────┤  │
    │  • Extract facts from results      │  │
    │  • LifecycleManager decision       │  │
    │    (ADD/UPDATE/DELETE/NOOP)        │  │
    │  • Store memories to file-store    │  │
    │  • Update knowledge graph          │  │
    │  • Generate final answer           │  │
    │  • Optional: ask user about        │  │
    │    replanning (interactive)        │  │
    │  → Reflection                      │  │
    └────────┬────────────────────────────┘  │
             │                              │
             ↓                              │
    ┌────────────────────────────────────┐  │
    │  Stream Final Answer               │←─┘
    │  to User via Channel               │
    └────────────────────────────────────┘
             │
             ↓
    ┌────────────────────────────────────┐
    │  Response Output                   │
    │  • Channel.Send()                  │
    │  • Channel.SendStreaming()         │
    │  • Update session history          │
    └────────────────────────────────────┘
             │
             ↓
    ┌────────────────────────────────────┐
    │  Message displayed to user         │
    └────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Channel → Gateway**: InboundHandler callback invoked on message
- ✅ **Gateway → Agent**: Routes to cognitive or simple runtime
- ✅ **5-Phase Loop**: Each phase input/output properly typed
- ✅ **Tool Execution**: Via Registry pattern (built-in, MCP, skills)
- ✅ **Response Output**: Streaming and batch modes supported

---

## 2. MEMORY CHAIN: Lifecycle

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          MEMORY LIFECYCLE                                 │
└──────────────────────────────────────────────────────────────────────────┘

┌─ CREATE ──────────────────────────────────────────────────────────────┐
│                                                                       │
│  1. FACT EXTRACTION (REFLECT phase)                                  │
│     ↓                                                                │
│     LLMFactExtractor.Extract()                                       │
│     (structured fact from agent reasoning)                           │
│                                                                       │
│  2. EMBEDDING                                                        │
│     ↓                                                                │
│     OpenAIEmbedding or NoopEmbedding                                │
│     (converts text to vector)                                        │
│                                                                       │
│  3. LIFECYCLE DECISION                                              │
│     ↓                                                                │
│     LifecycleManager.Decide()                                        │
│     ├─ ADD: New fact, store it                                      │
│     ├─ UPDATE: Similar fact exists, merge it                        │
│     ├─ DELETE: Conflicting fact, remove old                         │
│     └─ NOOP: Duplicate or low confidence                            │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
                             ↓
┌─ STORE ──────────────────────────────────────────────────────────────┐
│                                                                       │
│  FILE-BASED STORAGE                                                  │
│  Location: ~/.IronClaw/memory/                                       │
│                                                                       │
│  Memory JSON file format:                                            │
│  {                                                                   │
│    "id": "mem_<hash>",                                              │
│    "content": "...",                                                │
│    "embedding": [0.1, 0.2, ...],                                   │
│    "scope": "session|user|global",                                 │
│    "user_id": "user123",                                           │
│    "session_id": "sess456",                                        │
│    "created_at": "2026-04-10T10:00:00Z",                           │
│    "last_referenced": "2026-04-10T10:30:00Z",                      │
│    "strength": 0.8,                                                │
│    "tags": ["topic1", "topic2"]                                   │
│  }                                                                  │
│                                                                       │
│  SQLiteKnowledgeBase stores in DB:                                  │
│  • memory_id → file path mapping                                    │
│  • VSS embeddings index                                             │
│  • BM25 full-text index                                             │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
                             ↓
┌─ RETRIEVE ───────────────────────────────────────────────────────────┐
│                                                                       │
│  DURING PERCEIVE PHASE                                              │
│                                                                       │
│  1. Search Query                                                     │
│     memStore.Search({                                               │
│       Text: user_message,                                           │
│       Limit: 5,                                                      │
│       UserID: user_id,                                              │
│       Scopes: [ScopeSession, ScopeUser]                            │
│     })                                                               │
│                                                                       │
│  2. Hybrid Search                                                    │
│     ├─ Vector search (cosine similarity)                            │
│     ├─ BM25 ranking                                                 │
│     └─ Combine with weights                                         │
│                                                                       │
│  3. Results returned with scores                                    │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
                             ↓
┌─ INJECT IN CONTEXT ──────────────────────────────────────────────────┐
│                                                                       │
│  PERCEIVE PHASE OUTPUT                                              │
│  ↓                                                                  │
│  CognitiveState {                                                   │
│    RelevantMemories: [                                              │
│      {Content: "fact1", Score: 0.95},                              │
│      {Content: "fact2", Score: 0.87},                              │
│      ...                                                            │
│    ]                                                                │
│  }                                                                  │
│                                                                       │
│  PLAN PHASE INPUT                                                   │
│  ↓                                                                  │
│  Planner gets CognitiveState and formats:                          │
│  "---                                                               │
│  RELEVANT MEMORIES:                                                 │
│  - [fact1] (importance: high)                                       │
│  - [fact2] (importance: medium)                                     │
│  ---"                                                               │
│                                                                       │
│  Included in LLM prompt for reasoning                              │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
                             ↓
┌─ BACKGROUND TASKS ───────────────────────────────────────────────────┐
│                                                                       │
│  1. CONSOLIDATOR (runs hourly)                                       │
│     Session scope facts → User scope facts                           │
│     (promotes important session-local facts to user level)           │
│                                                                       │
│  2. COMPACTOR (runs periodically)                                    │
│     Merges redundant/similar facts                                  │
│     Compresses verbose facts                                        │
│                                                                       │
│  3. FORGETTING CURVE (runs daily)                                    │
│     Fade weak memories based on retention policy                    │
│     Remove expired facts by policy rules                            │
│                                                                       │
│  4. PROFILER (CURRENTLY NOT WIRED) ⚠️                                │
│     Creates summaries of related memory clusters                    │
│     Status: Component exists but not called                         │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Creation**: REFLECT phase extracts facts
- ✅ **Storage**: File-based + indexed in DB
- ✅ **Retrieval**: PERCEIVE phase searches by relevance
- ✅ **Context Injection**: Memories included in LLM prompts
- ⚠️ **Profiler Not Wired**: Component exists but callback not attached to reflector

---

## 3. KNOWLEDGE BASE CHAIN: Complete Pipeline

```
┌────────────────────────────────────────────────────────────────────┐
│                    KNOWLEDGE BASE PIPELINE                          │
└────────────────────────────────────────────────────────────────────┘

PHASE 1: INGESTION
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  Document Sources:                                               │
│  • Local files (Markdown, PDF, Text, Code)                       │
│  • Websites (via web scraper)                                    │
│  • Configuration directories (.IronClaw/knowledge/)              │
│                                                                  │
│  Entry: IngestPipeline.Ingest(uri, sourceType)                  │
│                                                                  │
│  ┌─ Registry.Extract() ──────────────────────────┐              │
│  │ Route by sourceType:                          │              │
│  │ • .md      → MarkdownExtractor                │              │
│  │ • .pdf     → PDFExtractor                     │              │
│  │ • .py/.go  → CodeExtractor                    │              │
│  │ • .txt     → TextExtractor                    │              │
│  │ • http://  → WebExtractor                     │              │
│  │                                               │              │
│  │ Returns: (title, content)                     │              │
│  └───────────────────────┬───────────────────────┘              │
│                          │                                      │
│              (Title: "...", Content: "......")                  │
│                          │                                      │
│  ┌─ Save Source Record ──┴──────────────────────┐              │
│  │ Store in KB:                                 │              │
│  │ {                                            │              │
│  │   source_id: "src_123",                      │              │
│  │   uri: "file:///docs/guide.md",              │              │
│  │   source_type: "markdown",                   │              │
│  │   title: "Installation Guide"                │              │
│  │ }                                            │              │
│  └───────────────────────┬───────────────────────┘              │
│                          │                                      │
└──────────────────────────┼──────────────────────────────────────┘

PHASE 2: CHUNKING
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ChunkText(content, strategy)                                    │
│                                                                  │
│  Strategy config:                                                │
│  • ChunkSize: 512 (default)                                      │
│  • ChunkOverlap: 64 (for context preservation)                   │
│                                                                  │
│  Output:                                                         │
│  ┌──────────────────────────────────┐                           │
│  │ Chunk 0                          │                           │
│  │ "The quick brown fox..."         │ ← overlap region →        │
│  ├──────────────────────────────────┤                           │
│  │ Chunk 1                          │                           │
│  │ "...fox jumps over..."           │ ← overlap region →        │
│  ├──────────────────────────────────┤                           │
│  │ Chunk 2                          │                           │
│  │ "...lazy dog..."                 │                           │
│  └──────────────────────────────────┘                           │
│                                                                  │
└─────────────────────┬────────────────────────────────────────────┘
                      │
PHASE 3: EMBEDDING
┌─────────────────────┴────────────────────────────────────────────┐
│                                                                  │
│  For each chunk:                                                 │
│                                                                  │
│  ┌─ Embedding Provider ──────────────────────────┐              │
│  │                                               │              │
│  │ If OpenAI API key configured:                │              │
│  │ └→ OpenAIEmbedding                           │              │
│  │    (via OpenAI API: text-embedding-3-small) │              │
│  │    Returns: 1536-dimensional vector         │              │
│  │                                               │              │
│  │ Else:                                         │              │
│  │ └→ NoopEmbedding                             │              │
│  │    (no-op, used for dev/testing)            │              │
│  │                                               │              │
│  └───────────────────────┬───────────────────────┘              │
│                          │                                      │
│              [0.1, 0.2, ..., -0.05]                             │
│                          │                                      │
└─────────────────────┬────────────────────────────────────────────┘
                      │
PHASE 4: STORAGE
┌─────────────────────┴────────────────────────────────────────────┐
│                                                                  │
│  SQLiteKnowledgeBase.saveChunk()                                │
│                                                                  │
│  Store in database:                                              │
│  {                                                              │
│    id: "chunk_src_123_0_1712234400",                           │
│    source_id: "src_123",                                        │
│    source_uri: "file:///docs/guide.md",                         │
│    source_type: "markdown",                                     │
│    content: "The quick brown fox...",                           │
│    embedding: [0.1, 0.2, ...],      ← stored in VSS            │
│    chunk_index: 0,                                              │
│    metadata: {section: "intro"},                                │
│    created_at: "2026-04-10T10:00:00Z"                           │
│  }                                                              │
│                                                                  │
│  Indexes:                                                        │
│  • VSS: Vector search index (approx nearest neighbor)           │
│  • BM25: Full-text search index                                 │
│                                                                  │
└─────────────────────┬────────────────────────────────────────────┘
                      │
PHASE 5: RETRIEVAL (PERCEIVE Phase)
┌─────────────────────┴────────────────────────────────────────────┐
│                                                                  │
│  During PERCEIVE, Perceiver queries knowledge:                  │
│                                                                  │
│  HybridRetriever.Search(query)                                  │
│  {                                                              │
│    Text: "How do I install?",                                   │
│    Limit: 5,                                                    │
│    SourceType: ""  (empty = search all)                         │
│  }                                                              │
│                                                                  │
│  ┌─ SQLiteKnowledgeBase.Search() ───────────────────┐          │
│  │                                                  │          │
│  │ 1. Vector Search                               │          │
│  │    Embed query: "How do I install?" → vec     │          │
│  │    Find nearest neighbors in VSS               │          │
│  │    Score: cosine_similarity                    │          │
│  │                                                  │          │
│  │ 2. BM25 Search                                 │          │
│  │    Full-text ranking on query words            │          │
│  │    Score: BM25 ranking function                │          │
│  │                                                  │          │
│  │ 3. Hybrid Combination                          │          │
│  │    Combined Score =                            │          │
│  │      BM25Weight * bm25_score +                │          │
│  │      VectorWeight * vector_score              │          │
│  │                                                  │          │
│  │ 4. Top-K results                               │          │
│  │    Return highest scoring chunks               │          │
│  │                                                  │          │
│  └──────────────────────┬─────────────────────────┘          │
│                         │                                    │
│           [KnowledgeResult[], ]                             │
│                         │                                    │
└─────────────────────┬────────────────────────────────────────────┘
                      │
PHASE 6: RERANKING (Optional)
┌─────────────────────┴────────────────────────────────────────────┐
│                                                                  │
│  If LLMReranker enabled:                                         │
│                                                                  │
│  LLMReranker.Rerank(query_text, results)                        │
│  {                                                              │
│    "Query": "How do I install?",                               │
│    "Results": [                                                │
│      {id: "chunk_1", score: 0.92, content: "..."},            │
│      {id: "chunk_2", score: 0.85, content: "..."},            │
│      {id: "chunk_3", score: 0.78, content: "..."}             │
│    ]                                                            │
│  }                                                              │
│                                                                  │
│  ↓                                                              │
│                                                                  │
│  LLM Call (Claude):                                             │
│  "Order these docs by relevance to 'How do I install?'"        │
│                                                                  │
│  ↓                                                              │
│                                                                  │
│  LLM Response:                                                  │
│  ["chunk_2", "chunk_1", "chunk_3"]  ← new order by relevance │
│                                                                  │
│  ↓                                                              │
│                                                                  │
│  Reranked Results:                                              │
│  [chunk_2 (0.91), chunk_1 (0.87), chunk_3 (0.75)]            │
│                                                                  │
│  Fallback: If LLM fails, return original order                 │
│                                                                  │
└─────────────────────┬────────────────────────────────────────────┘
                      │
PHASE 7: CONTEXT INJECTION
┌─────────────────────┴────────────────────────────────────────────┐
│                                                                  │
│  Add to CognitiveState during PERCEIVE:                         │
│                                                                  │
│  CognitiveState {                                               │
│    KnowledgeContext: [                                          │
│      "Installation steps: 1. Download... 2. Extract...",       │
│      "Prerequisites: Python 3.8+, git...",                     │
│      "Troubleshooting: If error X occurs, try Y..."            │
│    ]                                                            │
│  }                                                              │
│                                                                  │
│  Passed to PLAN phase for LLM reasoning                         │
│                                                                  │
└────────────────────────────────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Ingestion**: Registry pattern supports extensible extractors
- ✅ **Chunking**: Configurable size/overlap with context preservation
- ✅ **Embedding**: Optional OpenAI or no-op fallback
- ✅ **Storage**: VSS index for vector search + BM25 for text search
- ✅ **Retrieval**: Hybrid scoring combines both search methods
- ✅ **Reranking**: LLM-based optional reranking with fallback
- ✅ **Context Injection**: Results formatted and injected into cognitive state

---

## 4. SKILL CHAIN: Progressive Disclosure

```
┌────────────────────────────────────────────────────────────────┐
│                    SKILL PROGRESSIVE DISCLOSURE                 │
└────────────────────────────────────────────────────────────────┘

PHASE 1: LOADING
┌────────────────────────────────────────────────────────────────┐
│                                                                │
│  SkillManager.LoadDir() + LoadBuiltin()                        │
│                                                                │
│  Sources:                                                      │
│  1. Embedded builtin skills (in binary)                        │
│  2. File system:                                               │
│     ~/.IronClaw/skills/                                        │
│     ~/.IronClaw/skills/myskill/SKILL.md                        │
│     ~/.IronClaw/skills/anotherskill.md                         │
│                                                                │
│  For each skill file:                                          │
│  ParseSkill(path) →                                            │
│  {                                                             │
│    Name: "code_review",                                        │
│    Version: "1.0.1",                                           │
│    Description: "Review code for quality and security",        │
│    Tags: ["development", "quality-assurance"],                 │
│    Content: "<full markdown content lazy-loaded>"             │
│  }                                                             │
│                                                                │
│  Deduplication: First-loaded skill wins                        │
│                                                                │
└────────────────────────────┬─────────────────────────────────┘
                             │
PHASE 2: SELECTION & METADATA INJECTION
┌────────────────────────────┴─────────────────────────────────┐
│                                                              │
│  During PERCEIVE phase:                                      │
│                                                              │
│  SkillManager.Select(userText)                              │
│  ├─ Keyword matching: user_message contains skill name      │
│  ├─ Tag matching: skill tags match keywords                 │
│  └─ Fallback: Return all skills if no matches              │
│                                                              │
│  SkillManager.BuildPromptSection()                          │
│  (only metadata, not full content)                          │
│                                                              │
│  System Prompt Section:                                     │
│  ┌──────────────────────────────────────────────┐           │
│  │ ## Skills System                             │           │
│  │                                              │           │
│  │ You have access to a skills library:         │           │
│  │                                              │           │
│  │ - **code_review** (v1.0.1)                  │           │
│  │   Review code for quality and security      │           │
│  │   [development, quality-assurance]          │           │
│  │                                              │           │
│  │ - **documentation** (v2.1.0)                │           │
│  │   Generate technical documentation         │           │
│  │   [documentation, writing]                  │           │
│  │                                              │           │
│  │ **How to Use (Progressive Disclosure):**     │           │
│  │ 1. See skill metadata above                 │           │
│  │ 2. When a skill applies, call read_skill    │           │
│  │    with {action: "read", name: "<name>"}   │           │
│  │ 3. Follow the loaded instructions          │           │
│  └──────────────────────────────────────────────┘           │
│                                                              │
│  Injected into CognitiveState                               │
│  Available during PLAN phase                                │
│                                                              │
└────────────────────────────┬─────────────────────────────────┘
                             │
PHASE 3: LAZY LOADING ON DEMAND
┌────────────────────────────┴─────────────────────────────────┐
│                                                              │
│  During ACT phase, agent recognizes skill is needed:        │
│                                                              │
│  Agent decides: "I should use code_review skill"            │
│  Agent calls: read_skill tool                              │
│  Input: {action: "read", name: "code_review"}              │
│                                                              │
│  ┌─ SkillTool.Execute() ──────────────────────┐            │
│  │                                            │            │
│  │ manager.GetContent("code_review")         │            │
│  │                                            │            │
│  │ Loads full content from file:             │            │
│  │ ~/SKILL.md or embedded binary              │            │
│  │                                            │            │
│  │ Returns complete markdown with:            │            │
│  │ • Objectives                               │            │
│  │ • Prerequisites                            │            │
│  │ • Step-by-step workflow                    │            │
│  │ • Examples                                 │            │
│  │ • Best practices                           │            │
│  │ • Common pitfalls                          │            │
│  │                                            │            │
│  └────────────────┬───────────────────────────┘            │
│                   │                                        │
│      [Full skill markdown content]                         │
│                   │                                        │
└────────────────────┬───────────────────────────────────────┘
                     │
PHASE 4: SKILL EXECUTION
┌────────────────────┴───────────────────────────────────────┐
│                                                            │
│  Agent receives full skill instructions                   │
│  Agent follows workflow:                                  │
│                                                            │
│  Example skill "code_review" workflow:                    │
│  1. Examine file: call read_file tool                    │
│  2. Analyze for:                                          │
│     - Security issues                                    │
│     - Performance problems                               │
│     - Code style violations                              │
│     - Test coverage gaps                                │
│  3. Generate review report                                │
│  4. Return findings to user                              │
│                                                            │
│  Tool calls during execution:                             │
│  read_skill (already called)                             │
│  read_file (built-in tool)                               │
│  Other tools as needed per skill instructions             │
│                                                            │
└───────────────────────────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Loading**: Built-in + filesystem sourced skills
- ✅ **Selection**: Keyword/tag matching for relevance
- ✅ **Metadata Injection**: Only name, version, description in prompt
- ✅ **Lazy Loading**: Full content fetched only when needed
- ✅ **Execution**: Agent follows skill workflow using available tools

---

## 5. MCP CHAIN: Dynamic Tool Registration

```
┌────────────────────────────────────────────────────────┐
│              MCP (Model Context Protocol) CHAIN         │
└────────────────────────────────────────────────────────┘

STARTUP: Configuration Loading
┌────────────────────────────────────────────────────────┐
│                                                        │
│  Config file (config.yaml):                            │
│  tools:                                                │
│    mcp:                                                │
│      servers:                                          │
│        filesystem:                                     │
│          command: "mcp-server-filesystem"              │
│          args: ["--root", "/home/user"]               │
│          env: {}                                       │
│          requires_approval: true                       │
│        github:                                         │
│          command: "mcp-server-github"                  │
│          args: []                                      │
│          env:                                          │
│            GITHUB_TOKEN: "${GITHUB_TOKEN}"             │
│          requires_approval: false                      │
│                                                        │
└────────────────────────┬───────────────────────────────┘
                         │
PHASE 1: CONNECTION
┌────────────────────────┴───────────────────────────────┐
│                                                        │
│  Gateway.Start() calls:                                │
│  MCP Manager.StartServers(config, registry)           │
│                                                        │
│  For each server in config:                            │
│                                                        │
│  ┌─ Create Stdio Client ──────────────────┐           │
│  │ NewStdioMCPClient(                     │           │
│  │   command: "mcp-server-filesystem"     │           │
│  │   env: [],                             │           │
│  │   args: ["--root", "/home/user"]       │           │
│  │ )                                       │           │
│  │                                        │           │
│  │ Spawns subprocess:                     │           │
│  │ $ MCP-server-filesystem --root...      │           │
│  │   stdin: IronClaw → MCP messages       │           │
│  │   stdout: MCP messages → IronClaw      │           │
│  │                                        │           │
│  └────────────────┬─────────────────────┘           │
│                   │                                  │
│         [Subprocess running]                        │
│                   │                                  │
│  ┌─ Handshake ───┴──────────────────────┐           │
│  │ client.Initialize()                  │           │
│  │                                      │           │
│  │ Request:                             │           │
│  │ {                                    │           │
│  │   method: "initialize",              │           │
│  │   params: {                          │           │
│  │     protocolVersion: "2024-11-05",   │           │
│  │     clientInfo: {                    │           │
│  │       name: "ironclaw",              │           │
│  │       version: "1.0.0"               │           │
│  │     }                                │           │
│  │   }                                  │           │
│  │ }                                    │           │
│  │                                      │           │
│  │ Response:                            │           │
│  │ {                                    │           │
│  │   result: {                          │           │
│  │     protocolVersion: "2024-11-05",   │           │
│  │     serverInfo: {                    │           │
│  │       name: "mcp-server-filesystem", │           │
│  │       version: "1.0.0"               │           │
│  │     }                                │           │
│  │   }                                  │           │
│  │ }                                    │           │
│  │                                      │           │
│  └────────────────┬─────────────────────┘           │
│                   │                                  │
│          [Connected ✓]                              │
│                   │                                  │
└────────────────────┴───────────────────────────────┘

PHASE 2: TOOL DISCOVERY
┌──────────────────────────────────────────────────────┐
│                                                      │
│  client.ListTools()                                 │
│                                                      │
│  Request:                                           │
│  {                                                  │
│    method: "tools/list",                            │
│    params: {}                                       │
│  }                                                  │
│                                                      │
│  Response from filesystem MCP server:               │
│  {                                                  │
│    result: {                                        │
│      tools: [                                       │
│        {                                            │
│          name: "read_file",                         │
│          description: "Read contents of a file",    │
│          inputSchema: {                             │
│            type: "object",                          │
│            properties: {                            │
│              path: {                                │
│                type: "string",                      │
│                description: "File path"             │
│              }                                      │
│            }                                        │
│          }                                          │
│        },                                           │
│        {                                            │
│          name: "write_file",                        │
│          description: "Write to a file",            │
│          inputSchema: {...}                         │
│        },                                           │
│        {                                            │
│          name: "list_directory",                    │
│          description: "List files in a directory",  │
│          inputSchema: {...}                         │
│        }                                            │
│      ]                                              │
│    }                                                │
│  }                                                  │
│                                                      │
│  (similar discovery from github MCP server)         │
│                                                      │
└────────────────┬──────────────────────────────────┘
                 │
PHASE 3: ADAPTER WRAPPING & REGISTRATION
┌────────────────┴──────────────────────────────────┐
│                                                   │
│  For each tool from each server:                  │
│                                                   │
│  ┌─ NewToolAdapter(client, serverName, mcpTool) ┐│
│  │                                               ││
│  │ Creates IronClaw Tool wrapper:                ││
│  │ {                                             ││
│  │   name: "mcp_filesystem_read_file",          ││
│  │   description: "Read file (MCP adapter)",     ││
│  │   requiresApproval: true,   ← from config    ││
│  │   mcpClient: <client>,                        ││
│  │   serverName: "filesystem",                   ││
│  │   originalMCPTool: <tool_def>                ││
│  │ }                                             ││
│  │                                               ││
│  │ Implements IronClaw Tool interface:           ││
│  │ • Name(): returns tool name                   ││
│  │ • Description(): returns description          ││
│  │ • Execute(): calls MCP tool                   ││
│  │ • InputSchema(): returns schema               ││
│  │ • RequiresApproval(): respects config        ││
│  │                                               ││
│  └───────────────┬────────────────────────────┘│
│                  │                             │
│      [Adapter created]                         │
│                  │                             │
│  registry.Register(adapter)                    │
│  ↓                                             │
│  Added to tool registry with prefix:           │
│  "mcp_filesystem_read_file"                    │
│  "mcp_filesystem_write_file"                   │
│  "mcp_filesystem_list_directory"               │
│  "mcp_github_search_issues"                    │
│  "mcp_github_create_pull_request"              │
│  ... etc                                       │
│                                                │
└────────────────┬──────────────────────────────┘
                 │
PHASE 4: TOOL INVOCATION DURING ACT
┌────────────────┴──────────────────────────────┐
│                                               │
│  During ACT phase, agent calls:               │
│  mcp_filesystem_read_file                     │
│                                               │
│  Executor.Execute():                          │
│  1. Permission check (RequiresApproval)       │
│  2. Approval gate (if needed)                 │
│  3. Adapter.Execute(input) called             │
│                                               │
│  Adapter.Execute():                           │
│  ├─ Serialize input to JSON                   │
│  ├─ Call MCP client.CallTool(                 │
│  │    serverName: "filesystem",               │
│  │    toolName: "read_file",                  │
│  │    args: {...}                             │
│  │  )                                         │
│  │                                             │
│  │  MCP handshake:                            │
│  │  Request → subprocess                      │
│  │  subprocess processes request              │
│  │  Response → IronClaw                       │
│  │                                             │
│  ├─ Receive result from MCP                   │
│  ├─ Deserialize response                      │
│  └─ Return as IronClaw Result                 │
│                                               │
│  Result: {                                    │
│    Output: "File contents: ...",              │
│    Error: "",                                 │
│    Type: "text"                               │
│  }                                            │
│                                               │
│  Agent sees result in tool-use loop           │
│                                               │
└────────────────────────────────────────────┘

PHASE 5: HOT RELOAD (Background)
┌──────────────────────────────────────────────┐
│                                              │
│  Gateway.watchMCPDir()                       │
│  (background goroutine)                      │
│                                              │
│  Monitors: ~/.IronClaw/mcp/                  │
│  Polls every 30 seconds (configurable)       │
│                                              │
│  On config changes:                          │
│  • New server detected → StartServer()       │
│  • Server removed → StopServer()             │
│  • Server config changed → Restart it        │
│                                              │
│  Manager.SyncServers():                      │
│  ├─ Compare desired vs running              │
│  ├─ Stop servers not in desired             │
│  └─ Start servers in desired                │
│                                              │
│  Tools automatically added/removed           │
│  from registry                               │
│                                              │
└──────────────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Configuration**: YAML-based MCP server config
- ✅ **Connection**: Stdio-based process communication
- ✅ **Discovery**: Automatic tool listing from servers
- ✅ **Registration**: Tools wrapped as IronClaw tools with naming prefix
- ✅ **Execution**: Adapters marshal requests/responses
- ✅ **Hot Reload**: Directory monitoring for dynamic server management
- ✅ **Approval Gating**: Per-server approval requirements configurable

---

## 6. SCHEDULER CHAIN: Cron-Based Task Execution

```
┌────────────────────────────────────────────────────────┐
│              SCHEDULER CHAIN - PERIODIC TASKS           │
└────────────────────────────────────────────────────────┘

SETUP: Database
┌────────────────────────────────────────────────────────┐
│                                                        │
│  SQLite table: scheduled_tasks                         │
│  {                                                     │
│    id: "task_1",                                       │
│    name: "Daily Standup",                              │
│    cron_expr: "0 9 * * 1-5",  ← 9am weekdays         │
│    prompt: "Generate today's summary",                 │
│    channel: "slack",                                   │
│    channel_id: "#general",                             │
│    enabled: 1,                                         │
│    created_at: "2026-04-01T00:00:00Z",                │
│    last_run: "2026-04-09T14:00:00Z"                   │
│  }                                                    │
│                                                        │
└────────────────┬───────────────────────────────────────┘
                 │
PHASE 1: INITIALIZATION
┌────────────────┴───────────────────────────────────────┐
│                                                        │
│  Gateway.New() creates:                                │
│  sched = scheduler.New(db, 30*time.Second)            │
│                                                        │
│  Scheduler struct:                                     │
│  {                                                    │
│    db: *store.DB,                                     │
│    cron: cron.Cron with second-level precision,       │
│    handler: nil (set later),                          │
│    pollInterval: 30s,                                 │
│    entries: map[taskID] → cronEntryID                │
│  }                                                    │
│                                                        │
│  sched.SetHandler(func)                               │
│  ↓                                                    │
│  Handler wired in Gateway.New():                       │
│  {                                                    │
│    Creates InboundMessage from Task                   │
│    Calls Gateway.handleInbound()                      │
│  }                                                    │
│                                                        │
└────────────────┬───────────────────────────────────────┘
                 │
PHASE 2: START
┌────────────────┴───────────────────────────────────────┐
│                                                        │
│  Gateway.Start() calls:                                │
│  sched.Start(ctx)                                     │
│                                                        │
│  ┌─ Initial Sync ─────────────────────────┐          │
│  │ syncTasks(ctx):                        │          │
│  │   SELECT * FROM scheduled_tasks        │          │
│  │   WHERE enabled = 1                    │          │
│  │                                        │          │
│  │   For each task:                       │          │
│  │   registerTask(task)                   │          │
│  │   ├─ cron.AddFunc(cronExpr, func)     │          │
│  │   ├─ Capture cronEntryID              │          │
│  │   └─ Store in entries map             │          │
│  │                                        │          │
│  │   Example: "0 9 * * 1-5"               │          │
│  │   Scheduled for: 9:00 AM Mon-Fri      │          │
│  │                                        │          │
│  └────────────────┬────────────────────┘          │
│                   │                                │
│         [Tasks registered in cron]                │
│                   │                                │
│  ┌─ Start Cron ──┴──────────────────────┐         │
│  │ cron.Start()                         │         │
│  │ (starts goroutine to execute tasks)  │         │
│  └────────────────┬────────────────────┘         │
│                   │                                │
│         [Cron engine running]                     │
│                   │                                │
│  ┌─ Start Poll Loop ──────────────────┐          │
│  │ pollLoop(ctx) goroutine:            │         │
│  │ ticker := 30 second interval         │         │
│  │ ∞ loop:                              │         │
│  │   wait for tick                      │         │
│  │   syncTasks() again                  │         │
│  │   (picks up new/disabled tasks)      │         │
│  │                                      │         │
│  └────────────────┬────────────────────┘         │
│                   │                                │
│         [Polling running]                         │
│                                                        │
└────────────────────────────────────────────────────────┘

PHASE 3: WAITING FOR TRIGGER
┌────────────────────────────────────────────────────────┐
│                                                        │
│  Time passes...                                        │
│                                                        │
│  Monday, 9:00 AM arrives                              │
│  ├─ "Daily Standup" task trigger time reached         │
│  ├─ Cron fires the registered function               │
│  └─ Handler callback is invoked                       │
│                                                        │
└────────────────┬───────────────────────────────────────┘
                 │
PHASE 4: EXECUTION
┌────────────────┴───────────────────────────────────────┐
│                                                        │
│  Handler function (set in Gateway.New()):             │
│  {                                                    │
│    Create InboundMessage:                             │
│    {                                                  │
│      Channel: "slack",                                │
│      ChannelID: "#general",                           │
│      UserID: "scheduler",                             │
│      UserName: "scheduler",                           │
│      Text: "Generate today's summary"                 │
│    }                                                  │
│                                                        │
│    Call: Gateway.handleInbound(ctx, msg)              │
│  }                                                    │
│                                                        │
│  Gateway.handleInbound():                             │
│  ├─ Get channel adapter ("slack")                     │
│  ├─ Route to CognitiveAgent.HandleMessage()           │
│  └─ Process through 5-phase loop                      │
│                                                        │
│  PERCEIVE → PLAN → ACT → OBSERVE → REFLECT           │
│                                                        │
│  ↓                                                    │
│  Result sent back via Slack channel                   │
│                                                        │
│  ┌─ Update last_run ──────────────────┐              │
│  │ UPDATE scheduled_tasks             │              │
│  │ SET last_run = now()               │              │
│  │ WHERE id = 'task_1'                │              │
│  │                                    │              │
│  └────────────────────────────────────┘              │
│                                                        │
│  ⚠️ GAP: No result persistence!                       │
│     (Task output not saved to DB)                     │
│                                                        │
│  ⚠️ GAP: No error recovery!                           │
│     (Silent failure if handler crashes)              │
│                                                        │
└────────────────────────────────────────────────────────┘

PHASE 5: POLLING UPDATES
┌────────────────────────────────────────────────────────┐
│                                                        │
│  Every 30 seconds, poll loop ticks:                    │
│                                                        │
│  syncTasks():                                          │
│  ├─ Query current enabled tasks                        │
│  ├─ Build activeIDs set                               │
│  ├─ Register new tasks                                │
│  ├─ Remove tasks no longer enabled                    │
│  └─ Unregister removed tasks from cron                │
│                                                        │
│  Dynamic updates:                                      │
│  • Add task in UI → Next poll picks it up             │
│  • Disable task in UI → Next poll removes it          │
│  • Edit cron expr → Restart agent to reload          │
│                                                        │
└────────────────────────────────────────────────────────┘

PHASE 6: SHUTDOWN
┌────────────────────────────────────────────────────────┐
│                                                        │
│  Gateway.Stop():                                       │
│  ├─ sched.Stop()                                      │
│  │  ├─ cancel() context                               │
│  │  ├─ cron.Stop() (waits for running tasks)          │
│  │  └─ pollLoop goroutine exits                       │
│  │                                                    │
│  └─ All scheduled tasks stop                          │
│                                                        │
└────────────────────────────────────────────────────────┘
```

### Key Integration Points:
- ✅ **Initialization**: Cron scheduler created with DB + poll interval
- ✅ **DB Polling**: Regular sync picks up new/disabled tasks
- ✅ **Cron Execution**: Handler properly wired to convert Task → InboundMessage
- ✅ **Start/Stop**: Proper goroutine lifecycle management
- ⚠️ **Result Persistence**: Task outputs not saved (⚠️ GAP)
- ⚠️ **Error Recovery**: Failed tasks silently fail (⚠️ GAP)

---

## Summary: Integration Maturity

```
┌─────────────────────────────────────────────────────────┐
│               Integration Chain Status                  │
├─────────────────────────────────────────────────────────┤
│ Chain              Status    Completeness   Key Gaps     │
├─────────────────────────────────────────────────────────┤
│ Agent              ✅ Full   100%           None         │
│ Memory             ⚠️  95%   95%            Profiler     │
│ Knowledge Base     ✅ Full   100%           None         │
│ Skill              ✅ Full   100%           None         │
│ MCP                ✅ Full   100%           None         │
│ Scheduler          ⚠️  90%   90%            Result+Error │
│ Cognitive 5-Phase  ✅ Full   100%           None         │
└─────────────────────────────────────────────────────────┘

⚠️ = Partial/Minor Gaps
✅ = Complete/No Known Gaps
```


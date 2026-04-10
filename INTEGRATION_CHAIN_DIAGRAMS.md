# IronClaw Integration Chain Diagrams

**Visual representation of all 7 integration chains**

---

## Chain 1: AGENT CHAIN

### Message Processing Flow
```
┌─────────────┐
│   Channel   │  (Telegram, TUI, etc.)
│  Listener   │
└──────┬──────┘
       │ InboundMessage{text, channel, user}
       ↓
┌──────────────────────────────────┐
│  Gateway.handleInbound()         │
│  ├─ Route by channel name        │
│  └─ Determine agent mode         │
└──────┬───────────────────────────┘
       │
       ├────────────────────┬─────────────────────┐
       │                    │                     │
       ↓                    ↓                     ↓
   Simple Mode        Cognitive Mode        Debate Mode
   (Runtime)          (5-phase loop)        (multi-agent)
   
┌────────────────────────────────┐
│ Runtime.HandleMessage()        │
│                                │
│ for iteration < maxIter {      │
│   ├─ Build system prompt       │
│   ├─ LLM stream call           │
│   ├─ Parse tool calls          │
│   ├─ Execute tools             │
│   │  ├─ Concurrent (read)      │
│   │  └─ Sequential (write)     │
│   ├─ Store results in session  │
│   └─ Repeat if tools called    │
│ }                              │
│                                │
│ ├─ Save user message           │
│ ├─ Extract facts (background)  │
│ └─ Persist session             │
└────────┬───────────────────────┘
         │
         ↓
    ┌────────────┐
    │  Channel   │
    │   .Send()  │
    └────────────┘
```

### System Prompt Building
```
┌─ Personality (Soul.md)
│
├─ Core System Prompt
│  ├─ Agent.md (if configured)
│  └─ Config YAML system_prompt
│
├─ Persistent Rules (Memory.md)
│
├─ Retrieved Memories
│  └─ memStore.Search(userText, limit=5)
│     ├─ Session scope
│     └─ User scope
│
├─ User Profile
│  └─ Loaded from ~/.IronClaw/memory/
│
├─ Skills (Progressive Disclosure)
│  └─ skillMgr.BuildPromptSection(userText)
│     ├─ Metadata only (name, description, tags)
│     └─ Full content lazily via read_skill tool
│
└─ Available Agents
   └─ agentMgr.BuildPromptSection()
      └─ Agent metadata for delegation
```

### Tool Execution Modes
```
Concurrent Execution (Read-Only Tools):
┌────────────┐  ┌────────────┐  ┌────────────┐
│  Tool A    │  │  Tool B    │  │  Tool C    │
│ (read)     │  │ (read)     │  │ (read)     │
│ maxConcur=4│  │            │  │            │
└────┬───────┘  └────┬───────┘  └────┬───────┘
     │               │               │
     └───────────────┼───────────────┘
                     ↓
             Results merged in order

Sequential Execution (Write Tools):
┌────────────┐
│  Tool A    │
│ (write)    │
└────┬───────┘
     ↓
┌────────────┐
│  Tool B    │
│ (write)    │
└────┬───────┘
     ↓
┌────────────┐
│  Tool C    │
│ (write)    │
└────┬───────┘
     ↓
Results in sequence
```

---

## Chain 2: MEMORY CHAIN

### Lifecycle Flow
```
┌─────────────────────────────────────────────────────────┐
│                 Entry Points                            │
├─────────────────────────────────────────────────────────┤
│  • UserMessage → memStore.Save()                        │
│  • Fact Extraction (background)                         │
│  • Reflection phase (REFLECT)                           │
│  • Direct API calls                                     │
└─────────────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│         Fact Extraction (if enabled)                    │
├─────────────────────────────────────────────────────────┤
│  factExtractor.Extract(userMsg, assistantMsg)           │
│  └─ LLM → structured facts                              │
└─────────────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│      Lifecycle Management (ADD/UPDATE/DELETE/NOOP)      │
├─────────────────────────────────────────────────────────┤
│  lifecycleMgr.Process(fact, scope)                       │
│  ├─ Similarity search for existing entries              │
│  ├─ Decision: Is this new, update, or delete?           │
│  └─ Scope: Session → User → Global                      │
└─────────────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│          FileMemoryStore.Save() / Update()              │
├─────────────────────────────────────────────────────────┤
│  ├─ SQLite metadata table                               │
│  ├─ MD file entry (~/.IronClaw/memory/L0/)              │
│  └─ Vector embedding (if configured)                    │
└─────────────────────────────────────────────────────────┘
         │
         ├─────────────────────┬─────────────────────┐
         ↓                     ↓                     ↓
   ┌──────────┐        ┌───────────┐        ┌──────────┐
   │ Compactor│        │Consolidator│      │Forgetting│
   │(L0→L1→L2)│        │(sess→user) │      │ Curve    │
   └──────────┘        └───────────┘        └──────────┘
         │                     │                     │
         └─────────────────────┴─────────────────────┘
                       ↓
            ┌──────────────────────┐
            │  GraphSync (optional)│
            │ Memory→Knowledge     │
            │  Graph sync          │
            └──────────────────────┘
```

### Search & Retrieval
```
Search Query
├─ Text: "How do I X?"
├─ Limit: 5 results
├─ UserID: "user123"
└─ Scopes: [Session, User, Global]

         ↓
   ┌─────────────────────────────────┐
   │  BM25 Keyword Search            │
   │  ├─ Term frequency analysis     │
   │  ├─ Inverse doc frequency       │
   │  └─ Returns ranked results      │
   └──────────────┬──────────────────┘
                  │
   ┌──────────────┴──────────────┐
   ↓                             ↓
 ┌─────────────────┐      ┌──────────────────┐
 │ Vector Search   │      │ Merge & Dedupe   │
 │ (if embeddings) │      │ ├─ Combined      │
 │ ├─ Cosine sim   │      │ │   ranking      │
 │ ├─ HNSW index   │      │ └─ Top K results │
 │ └─ Returns      │      └──────────────────┘
 │    results      │              ↓
 └─────────────────┘      ┌──────────────────┐
                          │  Optional        │
                          │  Reranking       │
                          │  ├─ LLM rerank   │
                          │  └─ Scores       │
                          └────────┬─────────┘
                                   ↓
                          [SearchResult]
                          ├─ Entry content
                          ├─ Score
                          └─ Metadata
```

### Memory Consolidation (Session → User)
```
Session-scoped Memory
├─ Conversation-specific facts
├─ Short-term context
└─ Expires at session end

         ↓ (Every N minutes)
         
Consolidator runs:
├─ Query session-scoped entries
├─ Promote to user-scoped
├─ Update entry scope in DB
└─ Adjust retention policy

         ↓
         
User-scoped Memory
├─ Cross-session knowledge
├─ Persistent preferences
└─ Available to future sessions
```

---

## Chain 3: KNOWLEDGE BASE CHAIN

### Ingestion Pipeline
```
Documents
├─ Markdown files (.md)
├─ PDF files (.pdf)       ⚠️ STUB
├─ Web URLs (http://)
├─ Code files (.go, .py, etc.)
└─ Text files (.txt)

         ↓

┌──────────────────────────────────┐
│  Ingest Pipeline                 │
├──────────────────────────────────┤
│                                  │
│  switch ext(file) {              │
│    case ".md":                   │
│      ingestMarkdown()            │
│        └─ Parse frontmatter      │
│        └─ Split into chunks      │
│                                  │
│    case ".pdf":                  │
│      ingestPDF()        ⚠️        │
│        └─ STUB! Returns nil      │
│                                  │
│    case "http...":               │
│      ingestWeb()                 │
│        └─ Fetch + parse HTML     │
│        └─ Extract text           │
│                                  │
│    case ".go", ".py", ...:       │
│      ingestCode()                │
│        └─ Syntax-aware chunks    │
│        └─ Function/class boundary│
│                                  │
│    case ".txt":                  │
│      ingestText()                │
│        └─ Simple chunking        │
│  }                               │
└──────┬───────────────────────────┘
       │
       ↓
┌──────────────────────────────────┐
│  Chunk Metadata Extraction       │
├──────────────────────────────────┤
│ ├─ Source: <filename>            │
│ ├─ Source Type: code/doc/web     │
│ ├─ Chunk ID: <hash>              │
│ └─ Title/Summary (optional)      │
└──────┬───────────────────────────┘
       │
       ↓
┌──────────────────────────────────┐
│  Embedding Generation (if API)   │
├──────────────────────────────────┤
│ ├─ OpenAI API: text-embedding-3  │
│ ├─ Vector dim: 1536              │
│ ├─ Cache: [hash] → vector        │
│ └─ NoopEmbedding: if disabled    │
└──────┬───────────────────────────┘
       │
       ↓
┌──────────────────────────────────┐
│  Storage Layer                   │
├──────────────────────────────────┤
│ ├─ SQLite: metadata table        │
│ ├─ BM25 index: keyword           │
│ └─ Vector index: embeddings      │
└──────────────────────────────────┘
```

### Hybrid Search & Retrieval
```
Search Query: "How to deploy?"

         ↓
    ┌────────────────┐
    │ BM25 Search    │
    │ ├─ "deploy"    │
    │ ├─ "setup"     │
    │ └─ Returns top 5│
    └────────┬───────┘
             │
    ┌────────┴──────────────────┐
    │ Vector Search (optional)   │
    │ ├─ Embed query             │
    │ ├─ Cosine similarity       │
    │ └─ Returns top 5           │
    └────────┬──────────────────┘
             │
    ┌────────┴──────────────────┐
    │ Merge Results              │
    │ ├─ Deduplicate chunks      │
    │ ├─ Combine BM25 + vector   │
    │ │   scores (hybrid)        │
    │ └─ Top 5 merged results    │
    └────────┬──────────────────┘
             │
    ┌────────┴──────────────────┐
    │ Optional: LLM Reranking    │
    │ ├─ "Is this relevant?"     │
    │ ├─ LLM scores 0-1          │
    │ └─ Resort by LLM score     │
    └────────┬──────────────────┘
             │
             ↓
      [SearchResult]
      ├─ Chunk content
      ├─ Source info
      ├─ Score (BM25+vector)
      └─ Rerank score (optional)
```

### Knowledge Graph Integration
```
Entity Extraction (background):
┌─────────────────────────────────┐
│  For each chunk:                 │
│  ├─ LLM extracts entities        │
│  │  └─ People, places, concepts  │
│  ├─ Extract relationships        │
│  │  └─ "Person X works at Place Y"│
│  └─ Store in SQLiteGraph         │
└─────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────┐
│  Knowledge Graph Queries         │
│  ├─ Node lookup: "Alice"         │
│  ├─ Relationships: "Alice → ?"   │
│  └─ Path search: "Alice → X"     │
└─────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────┐
│  Memory ↔ Graph Sync             │
│  ├─ Extract entities from facts  │
│  ├─ Add to graph                 │
│  └─ Update existing nodes        │
└─────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────┐
│  Graph Decay (24h)               │
│  ├─ Age scores relationships     │
│  ├─ Remove stale edges           │
│  └─ Compress graph               │
└─────────────────────────────────┘
```

---

## Chain 4: SKILL CHAIN

### Progressive Disclosure Pattern
```
┌─────────────────────────────────────────────────────────┐
│                    Skill Manager                        │
│                                                         │
│  Loads at startup:                                      │
│  ├─ Builtin skills (embedded FS)                       │
│  ├─ User skills (~/.IronClaw/skills/)                  │
│  └─ Extra dirs (config)                                │
│                                                         │
│  Each skill: name, version, description, tags          │
│  (Full content NOT loaded into memory)                  │
└────────────────┬────────────────────────────────────────┘
                 │
                 ↓
        ┌────────────────────┐
        │ User Query: "X"    │
        └────────────┬───────┘
                     │
                     ↓
        ┌────────────────────────────────┐
        │  Skill Selection               │
        │  ├─ Match "X" against skills   │
        │  ├─ Keyword/tag matching       │
        │  └─ Return relevant skills     │
        └────────────┬───────────────────┘
                     │
                     ↓
    ┌───────────────────────────────────────┐
    │  Build Prompt Section (METADATA ONLY) │
    │                                       │
    │  "## Skills System                    │
    │   • SkillA (v1.0): description [tag] │
    │   • SkillB (v2.0): description [tag] │
    │                                       │
    │   How to use:                         │
    │   1. Recognize applicable skill       │
    │   2. Load skill: read_skill tool      │
    │   3. Follow instructions              │
    │   4. Access supporting files"         │
    └────────────────┬──────────────────────┘
                     │
                     ↓
        ┌────────────────────────────────┐
        │  System Prompt with Metadata   │
        └────────────┬───────────────────┘
                     │
                     ↓ LLM processes
        ┌────────────────────────────────┐
        │  Agent sees skill is needed:   │
        │  "read_skill" tool call        │
        │  input: {                      │
        │    "action": "read",           │
        │    "name": "SkillA"            │
        │  }                             │
        └────────────┬───────────────────┘
                     │
                     ↓
        ┌────────────────────────────────┐
        │  Skill Tool Execution          │
        │  └─ Load full skill content    │
        │  └─ Return to LLM              │
        └────────────┬───────────────────┘
                     │
                     ↓
        ┌────────────────────────────────┐
        │  Agent Has Full Instructions   │
        │  ├─ Step-by-step workflow      │
        │  ├─ Helper scripts             │
        │  └─ Best practices             │
        └────────────┬───────────────────┘
                     │
                     ↓
           Full Skill Execution
```

### Prompt Caching for Sub-Agents
```
Main Agent
├─ Calls: run_agent(subagent_name, task)
│          
└─ Runtime creates new runtime for subagent
   ├─ Build system prompt (expensive!)
   │  └─ Read skills, memories, etc.
   │
   ├─ Cache Key: "runtime:<agent_id>:<task_hash>"
   ├─ Check cache → hit? Return cached prompt
   └─ Cache miss? Build and cache
```

---

## Chain 5: MCP CHAIN

### Server Initialization & Discovery
```
┌─────────────────────────────────────┐
│  Configuration (config.yaml)        │
│                                     │
│  tools:                             │
│    mcp:                             │
│      servers:                       │
│        myserver:                    │
│          command: "python"          │
│          args: ["server.py"]        │
│          env:                       │
│            API_KEY: "..."           │
│          requires_approval: false   │
└──────────────┬──────────────────────┘
               │
               ↓
    ┌──────────────────────────────────┐
    │  Manager.StartServers()          │
    │  ├─ For each server config       │
    │  └─ Call startServer(config)     │
    └──────────────┬───────────────────┘
                   │
    ┌──────────────┴─────────────────┐
    ↓                                 ↓
┌─────────────────────┐    ┌──────────────────┐
│  Create Client      │    │  Build Env       │
│  ├─ Command         │    │  ├─ API_KEY      │
│  ├─ Args            │    │  ├─ Other vars   │
│  └─ Stdio pipes     │    │  └─ Pass to exec │
└──────────┬──────────┘    └──────────────────┘
           │
           └────────────────┬─────────────────┐
                            ↓
                  ┌──────────────────────┐
                  │  MCP Handshake       │
                  │  ├─ Send Initialize  │
                  │  ├─ Proto version    │
                  │  ├─ Client info      │
                  │  └─ Receive response │
                  └──────────┬───────────┘
                             │
                             ↓
                  ┌──────────────────────┐
                  │  ListTools()         │
                  │  ├─ Query server     │
                  │  ├─ Get tool list    │
                  │  └─ Parse responses  │
                  └──────────┬───────────┘
                             │
             ┌───────────────┤
             │               │
    ┌────────┴────────┐     ↓
    ↓                 │  ┌──────────────────────┐
 ┌────────────────┐   │  │ Store client map     │
 │ Create         │   │  │ clients["server"]->c │
 │ ToolAdapter    │   │  └──────────────────────┘
 │ for each tool  │   │
 │ ├─ Wrap tool   │   │
 │ ├─ Bind client │   │
 │ └─ Store name  │   │
 └────────┬───────┘   │
          │           │
          └───────────┼─────────────┐
                      │             │
                      ↓             ↓
            ┌──────────────────────────────────┐
            │ Registry.Register(adapter)       │
            │ for each MCP tool                │
            └──────────────────────────────────┘
```

### Tool Execution
```
Agent calls: mcp_tool_name(input)

         ↓
┌──────────────────────────────────────┐
│  ToolAdapter.Execute(input)          │
│  ├─ Get MCP client (pre-stored)      │
│  ├─ Parse input JSON                 │
│  └─ Build call request               │
└──────────┬───────────────────────────┘
           │
           ↓
┌──────────────────────────────────────┐
│  client.CallTool(                    │
│    name: "tool_name",                │
│    arguments: parsed_input            │
│  )                                   │
└──────────┬───────────────────────────┘
           │
           ↓
┌──────────────────────────────────────┐
│  MCP Server Process                  │
│  ├─ Execute tool                     │
│  ├─ Return result/error              │
│  └─ Send over stdio                  │
└──────────┬───────────────────────────┘
           │
           ↓
┌──────────────────────────────────────┐
│  Parse Response                      │
│  ├─ Extract content                  │
│  ├─ Handle errors                    │
│  └─ Return to agent                  │
└──────────────────────────────────────┘
```

### Hot-Reload Watcher
```
┌─────────────────────────────────────┐
│  Watch ~/.IronClaw/mcp/             │
│  (startup)                          │
└──────────────┬──────────────────────┘
               │
        ┌──────┴──────────┐
        ↓                 ↓
   New file added   File removed
   
   │                 │
   ├─ Parse config   ├─ Get existing
   ├─ StartServer()  │   client
   ├─ Register tools └─ Close()
   └─ Log            └─ Unregister
   
   ↓
Every N seconds: poll ~/IronClaw/mcp/
   ↓
Sync registered clients with FS state
```

---

## Chain 6: SCHEDULER CHAIN

### Task Lifecycle
```
┌─────────────────────────────────────────────────────────┐
│             Task Creation (Admin API)                   │
│                                                         │
│  POST /api/scheduled_tasks                              │
│  {                                                      │
│    name: "Daily Report",                               │
│    cron_expr: "0 9 * * *",   // 9 AM daily             │
│    channel: "telegram",                                 │
│    channel_id: "group123",                              │
│    prompt: "Generate daily report",                     │
│    enabled: true                                        │
│  }                                                      │
└──────────────────┬─────────────────────────────────────┘
                   │
                   ↓
         ┌─────────────────────┐
         │  SQLite DB storage  │
         │  scheduled_tasks    │
         │  ├─ id              │
         │  ├─ name            │
         │  ├─ cron_expr       │
         │  ├─ enabled         │
         │  ├─ last_run        │
         │  └─ channel info    │
         └────────────────────┘

Scheduler.Start()
         │
    ┌────┴──────────────────────────┐
    ↓                               ↓
Initial Sync             Poll Loop (background)
├─ Load all enabled      Every cfg.Scheduler.PollInterval
│  tasks from DB         │
├─ Register with cron   ├─ Query enabled tasks
└─ Start cron runner    ├─ Add new entries
                        ├─ Remove deleted
                        └─ Continue polling

         ↓ (When cron time matches)

┌─────────────────────┐
│  Task Triggered     │
│  ├─ Fire handler    │
│  ├─ Update last_run │
│  └─ Create message  │
└──────────┬──────────┘
           │
           ↓
  ┌────────────────────────────┐
  │ Handler callback:          │
  │ gateway.handleInbound()    │
  │ InboundMessage{           │
  │   Channel: task.Channel,   │
  │   Text: task.Prompt,       │
  │   UserID: "scheduler",     │
  │ }                          │
  └────────┬───────────────────┘
           │
           ↓
    ┌──────────────────┐
    │ Routes to agent: │
    │ Cognitive or     │
    │ Runtime          │
    └────────┬─────────┘
             │
             ↓
      Execute normally
      (full message processing)
```

---

## Chain 7: COGNITIVE AGENT (5-PHASE LOOP)

### Full Cycle
```
┌────────────────────────────────────────┐
│  CognitiveAgent.HandleMessage(msg)     │
└──────────────┬─────────────────────────┘
               │
               ├─ Session setup
               ├─ User message added
               └─ History compaction
               │
    ┌──────────┴──────────┐
    ↓                     ↓
Simple Task         Complex Task
(Complexity)        
    │                     │
    ↓                     ↓
Delegate to        Continue to PLAN
Runtime                   │
    │                     ↓
    └─────────┬───────────┤
              │           │
              └─────────────────────────────┐
                                            ↓
                        ┌──────────────────────────────┐
                        │  PERCEIVE Phase              │
                        │  (No LLM calls)              │
                        │  ├─ Parse goal               │
                        │  ├─ Assess complexity        │
                        │  ├─ Retrieve memories        │
                        │  ├─ Query knowledge base     │
                        │  ├─ Query knowledge graph    │
                        │  └─ Output: CognitiveState   │
                        └──────────┬───────────────────┘
                                   │
                                   ↓
                        ┌──────────────────────────────┐
                        │  RL Init (if enabled)        │
                        │  ├─ Build initial state      │
                        │  └─ Create episode collector │
                        └──────────┬───────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         │                                                   │
         ↓ (Up to MaxReplanAttempts)                        │
                                                            │
    ┌──────────────────────────────┐                        │
    │  PLAN Phase (attempt N)      │                        │
    │  ├─ LLM generates plan       │                        │
    │  ├─ Create TaskPlan          │                        │
    │  └─ PPO strategy adjustment  │                        │
    └──────────┬───────────────────┘                        │
               │                                            │
         ┌─────┴────────────┐                              │
         ↓                  ↓                              │
    DirectReply?      No. Continue                         │
    ├─ YES: Stream         │                              │
    │   answer, done       ↓                              │
    └─────────────┐   ┌──────────────────────────────┐   │
                  │   │  ACT Phase                   │   │
                  │   │  ├─ Topological sort         │   │
                  │   │  ├─ Parallel execution       │   │
                  │   │  ├─ Hook integration         │   │
                  │   │  ├─ Permission checks        │   │
                  │   │  └─ Output: Observations    │   │
                  │   └──────────┬───────────────────┘   │
                  │              │                        │
                  │              ↓                        │
                  │   ┌──────────────────────────────┐   │
                  │   │  OBSERVE Phase               │   │
                  │   │  ├─ Aggregate stats          │   │
                  │   │  ├─ Calculate progress       │   │
                  │   │  ├─ Detect patterns          │   │
                  │   │  └─ ObservationResult        │   │
                  │   └──────────┬───────────────────┘   │
                  │              │                        │
                  │              ↓                        │
                  │   ┌──────────────────────────────┐   │
                  │   │  REFLECT Phase               │   │
                  │   │  ├─ LLM evaluation           │   │
                  │   │  ├─ Fact extraction          │   │
                  │   │  ├─ Lifecycle decisions      │   │
                  │   │  ├─ Entity extraction        │   │
                  │   │  ├─ RL reward computation    │   │
                  │   │  └─ Replan decision?         │   │
                  │   └──────────┬───────────────────┘   │
                  │              │                        │
                  │              ├─ Replan? ─────────────┤
                  │              │                        │
                  │              └─ Done? ─┐             │
                  │                        │             │
                  └────────────────────────┴─────────────┘
                                           │
                                           ↓
                        ┌──────────────────────────────┐
                        │  Stream Final Answer         │
                        │  ├─ Format response          │
                        │  ├─ Send to channel          │
                        │  └─ Update session           │
                        └──────────┬───────────────────┘
                                   │
                                   ↓
                        ┌──────────────────────────────┐
                        │  Memory Notifications        │
                        │  ├─ Send to channel          │
                        │  ├─ Summary of facts added   │
                        │  └─ Updates made             │
                        └──────────┬───────────────────┘
                                   │
                                   ↓
                        Session Persistence
```

### Error Patterns (Detected in OBSERVE)
```
All Denied?
├─ YES → "all_denied"
└─ NO  → Check per-error

For each error:
├─ Permission? → "permission_error"
├─ Network?    → "network_error"
├─ Tool missing? → "tool_not_found"
└─ Other       → ignored
```

### RL Integration Points
```
1. PERCEIVE → RLPolicy.EvaluatePerception()
2. PLAN     → RLPolicy.SelectPlanStrategy()
3. ACT      → EpisodeCollector.RecordExecution()
4. OBSERVE  → Error patterns (feedback)
5. REFLECT  → RLPolicy.ComputeReward()
             → Trainer.Train(episode)  ⚠️ Incomplete
```

---

## Cross-Chain Integration Points

### 1. System Prompt Building (used by all)
```
Runtime (Simple Mode) + CognitiveAgent.Perceiver
└─ buildSystemPrompt()
   ├─ Personality
   ├─ Core prompt
   ├─ Rules
   ├─ Memories (from Memory Chain)
   ├─ User profile
   ├─ Skills metadata (from Skill Chain)
   └─ Agent metadata (from multi-agent)
```

### 2. Tool Execution (Agent + Executor)
```
Runtime.executeTools() + Executor.Run()
├─ Tool resolution (from registry)
├─ Permission checks (tool.PermissionEngine)
├─ Hook firing (pre/post-use)
├─ Concurrent vs sequential
└─ Result storage
```

### 3. Memory Lifecycle (Memory + Reflection)
```
Runtime.factExtractor + CognitiveAgent.reflector
└─ Extract facts
   ├─ LLMFactExtractor
   ├─ Process with LifecycleManager
   └─ Consolidate (session→user)
```

### 4. Knowledge Integration (KB + Perceiver)
```
CognitiveAgent.Perceiver.Run()
├─ Memory search (from Memory Chain)
├─ KB search (from Knowledge Chain)
├─ Graph query (from Knowledge Chain)
└─ All fed into CognitiveState
```

### 5. Tool Availability (MCP + Registry)
```
Gateway.Start()
├─ Standard tools registered
├─ MCP servers initialized
│  └─ Tools from MCP wrapped as adapters
├─ Skill tools registered
├─ Agent delegation tools
└─ All in tool.Registry for LLM calls
```

### 6. Scheduled Message Injection (Scheduler + Agent)
```
Scheduler.handler callback
└─ gateway.handleInbound()
   ├─ Create InboundMessage
   └─ Route to Cognitive Agent or Runtime
```

---

**Analysis Complete**: All 7 chains mapped with integration points identified.

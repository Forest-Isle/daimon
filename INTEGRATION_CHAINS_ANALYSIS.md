# IronClaw Integration Chain Analysis

**Date**: April 10, 2026  
**Project**: IronClaw - Local-first AI Agent Runtime  
**Analysis Scope**: 7 critical integration chains  
**Status**: COMPLETE WITH IDENTIFIED GAPS

---

## Executive Summary

IronClaw is a sophisticated Go-based multi-agent system with a well-structured architecture. The core integration chains are **mostly complete** but contain several **integration gaps** and **incomplete connections**:

### Overall Assessment:
- ✅ **Agent Chain**: FULLY INTEGRATED
- ✅ **Memory Chain**: FULLY INTEGRATED (file-based)
- ⚠️ **Knowledge Base Chain**: PARTIALLY INTEGRATED (gap in profiler callback)
- ✅ **Skill Chain**: FULLY INTEGRATED (progressive disclosure pattern)
- ✅ **MCP Chain**: FULLY INTEGRATED
- ✅ **Scheduler Chain**: FULLY INTEGRATED
- ⚠️ **Cognitive Agent (5-phase loop)**: FULLY IMPLEMENTED but with minor RL gaps

---

## 1. AGENT CHAIN: User Message → Agent → LLM → Tool Execution → Response

### Flow Diagram
```
Channel.InboundMessage
    ↓
Gateway.handleInbound()
    ↓
[Cognitive Agent Mode?]
    ├─ YES → CognitiveAgent.HandleMessage()
    │         [5-phase loop]
    └─ NO  → Runtime.HandleMessage()
             [Simple loop]
    ↓
Agent Loop (max iterations):
    ├─ LLM Call (stream or non-stream)
    ├─ Tool Call Parsing
    ├─ Tool Execution (concurrent or sequential)
    ├─ Memory Update
    └─ Session Persistence
    ↓
Channel.Send() / Channel.SendStreaming()
```

### Component Wiring

**Entry Point** (`cmd/ironclaw/main.go:256-291`):
```go
gw, err := gateway.New(cfg)           // Initialize all modules
gw.AddChannel(tg)                     // Register Telegram
gw.Start(ctx)                         // Start message processing
```

**Gateway Initialization** (`internal/gateway/gateway.go:55-105`):
```
1. initDatabase()
2. initToolsAndHooks()
3. initAgentRuntime()        ← Creates simple Runtime
4. initMemorySystem()
5. initCognitiveAgent()      ← Creates CognitiveAgent
6. initKnowledgeSystem()
7. initSkillManager()
8. initMultiAgent()
```

**Message Flow** (`internal/gateway/gateway.go:207-260`):
```go
handleInbound(msg) {
    if cognitiveAgent != nil {
        cognitiveAgent.HandleMessage()  // Preferred if enabled
    } else {
        runtime.HandleMessage()          // Fallback
    }
}
```

### Simple Mode: Runtime.HandleMessage()

**File**: `internal/agent/runtime.go:189-420`

1. **Session Management**:
   - `sessions.Get(channel, channelID)` → retrieves or creates session
   - `sess.AddMessage(user_message)` → adds user input

2. **System Prompt Building** (`buildSystemPrompt:496-570`):
   ```
   Personality (Soul.md)
       ↓
   Core System Prompt (Agent.md)
       ↓
   Persistent Rules (Memory.md)
       ↓
   Retrieved Memories (search)
       ↓
   User Profile (loaded from file)
       ↓
   Skills (progressive disclosure)
       ↓
   Available Agents (multi-agent metadata)
   ```

3. **LLM Call Loop** (`runtime.go:249-355`):
   ```go
   for iteration := 0; iteration < maxIterations; iteration++ {
       updater := ch.SendStreaming()        // Start streaming
       stream := provider.Stream(req)       // LLM call
       
       for delta := range stream {
           fullText += delta.Text           // Accumulate response
           toolCalls += delta.ToolCalls     // Collect tool calls
           updater.Update(fullText)         // Stream to client
       }
       
       sess.AddMessage(assistant_response)
       
       if len(toolCalls) > 0 {
           executeTools(toolCalls)          // Execute and loop
       } else {
           break                            // Done
       }
   }
   ```

4. **Tool Definition Building** (`buildToolDefs:572-583`):
   ```go
   defs := []
   for tool in registry.All() {
       defs += ToolDefinition{name, description, input_schema}
   }
   return defs
   ```

5. **Tool Execution** (`concurrent.go:29-100`, `runtime.go` executeTools):
   - **Read-only tools**: Executed concurrently (via `errgroup`)
   - **Write tools**: Executed sequentially
   - **Results**: Captured and added to session
   - **Hooks**: Pre/post-tool-use hooks fired

6. **Memory Integration** (`runtime.go:363-416`):
   ```go
   // Save user message to memory
   memStore.Save(user_msg)
   
   // Background fact extraction (detached context, 30s timeout)
   if factExtractor != nil {
       go factExtractor.Extract(assistant_response)
           → lifecycleMgr.Process(fact)
   }
   ```

7. **Session Persistence** (`runtime.go:357-360`):
   ```go
   sessions.Persist(sess)  // Save to DB
   ```

### ✅ COMPLETE: All wiring is functional. Data flows end-to-end.

---

## 2. MEMORY CHAIN: Creation → Storage → Retrieval → Lifecycle

### Flow Diagram
```
Entry Point: initMemorySystem()
    ↓
Embedder Setup (OpenAI or Noop)
    ↓
FileMemoryStore Creation
    ├─ Storage Dir: ~/.IronClaw/memory/
    ├─ SQLite DB: memory tracking
    └─ Embedding Index: vector search
    ↓
Fact Extraction Pipeline:
    ├─ LLM extracts facts from conversation
    ├─ Lifecycle Manager (ADD/UPDATE/DELETE/NOOP)
    ├─ Consolidator (promotes session→user scope)
    └─ Reflector (writes via lifecycle)
    ↓
Memory Search (retrieval):
    ├─ BM25 + Vector search (hybrid)
    ├─ Scopes: Session, User, Global
    └─ Results injected into system prompt
    ↓
Background Tasks:
    ├─ Compactor (compresses L0 entries)
    ├─ Consolidator (promotes memories to user scope)
    ├─ Forgetting Curve (fade weak memories daily)
    └─ GraphSync (memory→knowledge graph)
```

### Component Wiring

**Initialization** (`internal/gateway/init_memory.go:15-138`):

```go
// 1. Embedder setup
embedder := NewCachedEmbedder(NewOpenAIEmbedding(...))

// 2. FileMemoryStore with SQLite backend
fileStore := NewFileMemoryStore(storageDir, db, embedder, config)
runtime.SetMemoryStore(fileStore)

// 3. Fact extraction (if enabled)
factExtractor := NewLLMFactExtractor(completer, config)
runtime.SetFactExtractor(factExtractor)

// 4. Lifecycle manager
lifecycleMgr := NewLifecycleManager(
    memStore, embedder, completer, config, reflector
)
runtime.SetLifecycleManager(lifecycleMgr)

// 5. Background tasks
compactor.Start(ctx)        // Compacts L0→L1→L2
consolidator.Start(ctx)     // Promotes session→user scope
forgettingCurve.Start()     // Daily fade + retention policy

// 6. Tool registration
tools.Register(NewMemoryManageTool(...))
```

**Memory Store Interface** (`internal/memory/store.go`):
```go
type Store interface {
    Save(ctx, entry) error
    Search(ctx, query) []SearchResult
    Update(ctx, id, content) error
    Delete(ctx, id) error
    ListScopes(ctx) []Scope
}
```

**Lifecycle Decisions** (`internal/memory/lifecycle.go`):
```go
Decision = ADD      // New memory, insert
Decision = UPDATE   // Existing memory, update
Decision = DELETE   // Obsolete memory, mark deleted
Decision = NOOP     // No action needed
```

**Retrieval Integration** (`runtime.go:527-539`):
```go
if memStore != nil {
    results := memStore.Search(ctx, SearchQuery{
        Text: userText,
        Limit: 5,
        UserID: "",
        Scopes: []ScopeSession, ScopeUser,
    })
    // Inject into system prompt
}
```

**Cognitive Agent Integration** (`agent/reflect.go`):
- Reflector calls `factExtractor.Extract(userMsg, assistantMsg)`
- Results processed by `lifecycleMgr.Process(fact, scope)`
- Scopes: Session → User → Global (via consolidator)

### ⚠️ IDENTIFIED GAP - Profiler Callback

**File**: `internal/gateway/init_memory.go:91-95`

```go
profiler := memory.NewProfiler(...)
_ = profiler  // Unused!

// TODO: Add profiler callback to reflector once ReflectionTracker supports it
slog.Info("memory: profiler created")
```

**Issue**: Profiler is instantiated but never wired. `ReflectionTracker` exists but no callback mechanism to trigger profiler after reflection completes.

**Impact**: User profiles (learned preferences) are generated but NOT automatically applied during reflections. Manual profiler invocation only (via tools).

**Fix Needed**:
```go
// In ReflectionTracker or Reflector
reflector.SetProfilerCallback(profiler.GenerateProfile)
// Call after reflection completes:
profiler.GenerateProfile(ctx, sessionID, userID)
```

### ✅ COMPLETE: File-based system fully wired except profiler callback.

---

## 3. KNOWLEDGE BASE CHAIN: Ingestion → Embedding → Storage → Retrieval → Reranking

### Flow Diagram
```
initKnowledgeSystem()
    ↓
Ingestion Pipeline:
    ├─ Markdown → chunk.Parse()
    ├─ PDF → [STUB: placeholder]
    ├─ Web → fetch + parse
    ├─ Code → syntax-aware chunking
    └─ Text → simple split
    ↓
Embedding Layer:
    ├─ NoopEmbedder (if no API key)
    └─ OpenAI Embedding (if configured)
    ↓
Storage:
    ├─ SQLite: chunk metadata
    ├─ BM25 index: keyword search
    └─ Vector index: semantic search
    ↓
Retrieval Chain:
    ├─ HybridRetriever(KB, Reranker)
    ├─ BM25 keyword search
    ├─ Vector search
    ├─ Merge + deduplicate
    └─ Rerank results
    ↓
Reranker Options:
    ├─ NoopReranker (no reranking)
    └─ LLMReranker (LLM-based reranking)
    ↓
Knowledge Graph (Phase 3):
    ├─ Entity extraction from chunks
    ├─ SQLiteGraph storage
    ├─ GraphSync to memory lifecycle
    └─ GraphDecay background task
```

### Component Wiring

**Initialization** (`internal/gateway/init_knowledge.go:13-107`):

```go
// 1. Config setup
kbConfig := knowledge.Config{
    ChunkSize, ChunkOverlap, BM25Weight, VectorWeight, ...
}

// 2. Embedder selection
if cfg.Memory.OpenAIAPIKey != "" {
    kbEmbedder = NewOpenAIEmbedding(...)
} else {
    kbEmbedder = &noopKBEmbedder{}
}

// 3. Knowledge base creation
kb := knowledge.New(db, embedder, config)

// 4. Reranker setup
if cfg.Knowledge.Reranker.Enabled && provider == "llm" {
    reranker = NewLLMReranker(completer)
} else {
    reranker = &NoopReranker{}
}

// 5. Retriever creation
retriever := NewHybridRetriever(kb, reranker)

// 6. Set on perceiver (cognitive agent)
cognitiveAgent.SetKnowledgeSearcher(retriever)

// 7. Initial ingestion at startup
for dir in cfg.Knowledge.IngestDirs {
    kb.GetPipeline().IngestDir(ctx, dir)
}
```

**Pipeline** (`internal/knowledge/pipeline.go`):
```go
type Pipeline struct {
    kb *Knowledge
}

func (p *Pipeline) IngestDir(ctx, dir) error {
    for file in readdir(dir) {
        switch ext(file) {
        case ".md":   ingestMarkdown(file)
        case ".pdf":  ingestPDF(file)      // STUB
        case ".txt":  ingestText(file)
        case ".go":   ingestCode(file)
        case "http": ingestWeb(file)
        }
    }
}
```

**Retriever Integration** (`perceive.go:79-93`):
```go
if p.searcher != nil {
    results := p.searcher.Search(ctx, KnowledgeQuery{
        Text: userMsg,
        Limit: 5,
    })
    for r in results {
        knowledgeContext += r.Chunk.Content
    }
}
```

**Knowledge Graph Integration** (`init_knowledge.go:56-103`):
```go
// 1. Create graph
kg := graph.NewSQLiteGraph(db)

// 2. Entity extractor
extractor := graph.NewLLMEntityExtractor(kg, completer)

// 3. Initial extraction (background)
go {
    for source in kb.Sources() {
        for chunk in kb.Search(source) {
            extractor.Extract(chunk.Content)
        }
    }
}()

// 4. Set on cognitive agent
cognitiveAgent.SetKnowledgeGraph(kg)
cognitiveAgent.SetEntityExtractor(extractor)

// 5. Wire to memory lifecycle
if lifecycleMgr != nil {
    graphSync := graph.NewGraphSync(kg, extractor)
    lifecycleMgr.SetGraphSync(graphSync)
}

// 6. Start decay task (background)
graphDecay := graph.NewGraphDecayTask(kg, 24*time.Hour)
go graphDecay.Start(ctx)
```

### ⚠️ IDENTIFIED GAP - PDF Ingester Stub

**File**: `internal/knowledge/ingest/pdf.go`

```go
// PDFIngester is a stub for PDF parsing.
type PDFIngester struct{}

func (p *PDFIngester) Ingest(ctx, filePath) error {
    // Not implemented
    return nil  // Silent fail!
}
```

**Issue**: PDF files are silently ignored (no error returned). This breaks knowledge ingestion for PDF documents.

**Impact**: PDF knowledge bases cannot be ingested. Users attempting to add PDF documents will silently fail with no indication.

**Fix Needed**:
```go
func (p *PDFIngester) Ingest(ctx, filePath, kb) error {
    // Use pdftotext or PDF library (e.g., go-pdf/pdf)
    text, err := extractPDFText(filePath)
    if err != nil {
        return fmt.Errorf("PDF extraction failed: %w", err)
    }
    return kb.IngestText(ctx, text, metadata)
}
```

### ✅ MOSTLY COMPLETE: KB chain fully wired except PDF stub. All retrieval paths functional.

---

## 4. SKILL CHAIN: Loading → Registration → Invocation

### Flow Diagram
```
Skill Loading:
    ├─ initSkillManager() → Load from dirs
    ├─ Builtin skills (embedded FS)
    ├─ User skills (~/.IronClaw/skills/)
    └─ Extra directories (config)
    ↓
Skill Registration (progressive disclosure):
    ├─ Metadata indexed: name, version, description, tags
    ├─ Content NOT loaded into memory (lazy)
    └─ Metadata injected into system prompt
    ↓
Skill Selection:
    ├─ Match user text against skill metadata
    ├─ Keyword/tag matching
    └─ Fallback: include all skills
    ↓
Read Skill Tool (lazy loading):
    ├─ Agent calls read_skill tool
    ├─ Tool returns full skill content
    └─ Skill cached by PromptCache (for sub-agents)
    ↓
Skill Execution:
    ├─ Agent follows skill instructions
    ├─ Skill references supporting files/scripts
    └─ Results fed back to agent
```

### Component Wiring

**Initialization** (`internal/gateway/init_skills.go:10-38`):

```go
// 1. Create manager
skillMgr := skill.New()

// 2. Load skills from sources
skillMgr.LoadBuiltin()                      // Embedded skills
skillMgr.LoadDir(userSkillsDir)             // ~/.IronClaw/skills/
for dir in cfg.Skills.ExtraDirs {
    skillMgr.LoadDir(dir)
}

// 3. Inject into runtime
runtime.SetSkillManager(skillMgr)
cognitiveAgent.SetSkillManager(skillMgr)

// 4. Register read_skill tool
tools.Register(NewSkillTool(skillMgr))
```

**Loading** (`internal/skill/manager.go:85-142`):

```go
func (m *Manager) LoadDir(dir string) error {
    for entry in readdir(dir) {
        // Look for SKILL.md in subdirs or flat .md files
        skillPath := resolveSkillPath(entry)
        
        if skillPath != "" {
            skill := ParseSkill(skillPath)  // Parse frontmatter
            m.skills.append(skill)
        }
    }
}
```

**Metadata Extraction** (`internal/skill/skill.go`):
```go
type Skill struct {
    Name string          // Extracted from SKILL.md frontmatter
    Version string
    Description string
    Tags []string
    // Content NOT loaded here (lazy)
}
```

**System Prompt Injection** (`skill/manager.go:188-224`):

```go
func (m *Manager) BuildPromptSection(userText string) string {
    selected := m.Select(userText)  // Match user text
    
    // Build section with metadata only
    sb.WriteString("## Skills System\n\n")
    for skill in selected {
        sb.WriteString(fmt.Sprintf(
            "- **%s** (v%s): %s [%s]\n",
            skill.Name, skill.Version, 
            skill.Description, strings.Join(skill.Tags, ", "),
        ))
    }
    
    // Progressive disclosure instructions
    sb.WriteString("You see skill metadata above.\n")
    sb.WriteString("Load full content via read_skill tool when needed.\n")
    
    return sb.String()
}
```

**Read Skill Tool** (`internal/tool/skill.go`):

```go
func (st *SkillTool) Execute(ctx, input) (string, error) {
    action := input["action"]  // "read" or "list"
    name := input["name"]
    
    if action == "read" {
        content, err := st.skillMgr.GetContent(name)
        return content, err
    }
}
```

**Prompt Caching** (`internal/agent/runtime.go:498-502`):

```go
if r.promptCache != nil && r.agentID != "" {
    cacheKey := fmt.Sprintf("runtime:%s:%s", r.agentID, hash)
    return r.promptCache.GetOrBuild(cacheKey, func() string {
        return r.buildSystemPromptUncached(ctx, userText)
    })
}
```

### ✅ COMPLETE: Full progressive disclosure pattern implemented. All connections functional.

---

## 5. MCP CHAIN: Configuration → Connection → Tool Registration

### Flow Diagram
```
MCP Initialization:
    ├─ config.yaml: MCP.Servers[name]
    └─ Each server: command, args, env, requires_approval
    ↓
Manager.StartServers():
    ├─ For each server:
    │  ├─ Create StdioMCPClient(command, args, env)
    │  ├─ Initialize handshake (protocol version)
    │  ├─ ListTools() discovery
    │  ├─ Create ToolAdapter for each tool
    │  └─ Register in tool registry
    ├─ Individual failures logged (non-fatal)
    └─ Clients map stored in Manager
    ↓
Tool Registry Integration:
    ├─ Adapters registered as tool.Tool instances
    ├─ Each adapter wraps MCP tool
    └─ Available for agent LLM calls
    ↓
Hot-Reload Watcher:
    ├─ Monitors ~/.IronClaw/mcp/
    ├─ Detects new/removed server configs
    └─ Dynamic (re)initialization
    ↓
Cleanup:
    └─ Close all MCP connections on shutdown
```

### Component Wiring

**Initialization** (`internal/gateway/gateway.go:114-119`):

```go
gw.mcpManager = mcp.NewManager()

// Start MCP servers at gateway startup
if len(gw.cfg.Tools.MCP.Servers) > 0 {
    err := gw.mcpManager.StartServers(ctx, 
        gw.cfg.Tools.MCP.Servers, 
        gw.tools,  // Tool registry
    )
    if err != nil {
        slog.Error("some MCP servers failed", "err", err)
        // Non-fatal — continue
    }
}
```

**MCP Manager** (`internal/mcp/manager.go:30-91`):

```go
func (m *Manager) StartServers(ctx, servers, registry) error {
    for name, srv in servers {
        if err := m.startServer(ctx, name, srv, registry) {
            slog.Error("mcp server failed", "server", name, "err", err)
            // Continue with other servers
        }
    }
}

func (m *Manager) startServer(ctx, name, srv, registry) error {
    // 1. Build environment
    env := make([]string, 0, len(srv.Env))
    for k, v in srv.Env {
        env.append(k + "=" + v)
    }
    
    // 2. Create stdio client
    c, err := client.NewStdioMCPClient(
        srv.Command, 
        env, 
        srv.Args...,
    )
    if err != nil {
        return err
    }
    
    // 3. Initialize MCP handshake
    initReq := mcp.InitializeRequest{}
    initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
    initReq.Params.ClientInfo = mcp.Implementation{
        Name: "ironclaw",
        Version: "1.0.0",
    }
    _, err := c.Initialize(ctx, initReq)
    if err != nil {
        c.Close()
        return err
    }
    
    // 4. Discover tools
    toolsResp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
    if err != nil {
        c.Close()
        return err
    }
    
    // 5. Register adapters
    for _, t in toolsResp.Tools {
        adapter := NewToolAdapter(
            c, 
            name,          // server name
            t,             // MCP tool definition
            srv.RequiresApproval,
        )
        registry.Register(adapter)
        slog.Info("mcp tool registered", "name", adapter.Name())
    }
    
    m.clients[name] = c
    return nil
}
```

**Tool Adapter** (`internal/mcp/adapter.go`):

```go
type ToolAdapter struct {
    client         client.MCPClient
    serverName     string
    tool           mcp.Tool
    requiresApproval bool
}

func (a *ToolAdapter) Name() string {
    return a.tool.Name
}

func (a *ToolAdapter) Description() string {
    return a.tool.Description
}

func (a *ToolAdapter) Execute(ctx, input string) (string, error) {
    callResp, err := a.client.CallTool(ctx, mcp.CallToolRequest{
        Name: a.tool.Name,
        Arguments: parseJSON(input),
    })
    return callResp.Content, err
}
```

**Hot-Reload Watcher** (`internal/gateway/gateway.go:122-123`):

```go
go gw.watchMCPDir(ctx)  // Polls ~/.IronClaw/mcp/ for changes
```

**Cleanup** (`internal/gateway/gateway.go:184`):

```go
_ = gw.mcpManager.Close()  // Close all MCP connections
```

### ✅ COMPLETE: MCP chain fully integrated. Server startup, tool discovery, registration, and cleanup all functional.

---

## 6. SCHEDULER CHAIN: Task Storage → Cron Registration → Execution → Agent Invocation

### Flow Diagram
```
Task Storage (SQLite):
    ├─ Table: scheduled_tasks
    ├─ Columns: id, name, cron_expr, prompt, channel, channel_id, enabled, last_run
    └─ Managed via admin API
    ↓
Scheduler Initialization:
    ├─ New(db, pollInterval)
    ├─ Create cron runner (robfig/cron with seconds support)
    └─ Set task handler callback
    ↓
Scheduler.Start(ctx):
    ├─ Initial sync: load all enabled tasks from DB
    ├─ Register each task in cron
    ├─ Start cron runner
    └─ Launch poll loop (background)
    ↓
Poll Loop (every pollInterval):
    ├─ Query DB for enabled tasks
    ├─ Register new tasks
    ├─ Deregister deleted/disabled tasks
    └─ Continue polling
    ↓
Task Trigger (cron expression matches):
    ├─ Fire TaskHandler callback
    ├─ Update last_run timestamp
    └─ Handler creates InboundMessage
    ↓
Message Injection:
    ├─ handleInbound(InboundMessage{
    │    Channel: task.Channel,
    │    ChannelID: task.ChannelID,
    │    UserID: "scheduler",
    │    Text: task.Prompt,
    ├─ Route to cognitive agent or runtime
    └─ Agent processes as normal message
```

### Component Wiring

**Initialization** (`internal/gateway/gateway.go:87-89`):

```go
gw.sched = scheduler.New(gw.db, cfg.Scheduler.PollInterval)
gw.mcpManager = mcp.NewManager()

// ... later ...

gw.sched.SetHandler(func(ctx, task) {
    gw.handleInbound(ctx, channel.InboundMessage{
        Channel: task.Channel,
        ChannelID: task.ChannelID,
        UserID: "scheduler",
        UserName: "scheduler",
        Text: task.Prompt,
    })
})
```

**Scheduler** (`internal/scheduler/scheduler.go:28-112`):

```go
func New(db, pollInterval) *Scheduler {
    return &Scheduler{
        db: db,
        cron: cron.New(cron.WithSeconds()),
        pollInterval: pollInterval,
        entries: make(map[string]cron.EntryID),
    }
}

func (s *Scheduler) SetHandler(h TaskHandler) {
    s.handler = h
}

func (s *Scheduler) Start(ctx) {
    pollCtx, cancel := context.WithCancel(ctx)
    s.cancel = cancel
    
    s.syncTasks(pollCtx)        // Initial load
    s.cron.Start()              // Start cron runner
    go s.pollLoop(pollCtx)      // Background poller
}

func (s *Scheduler) pollLoop(ctx) {
    ticker := time.NewTicker(s.pollInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.syncTasks(ctx)  // Periodic sync
        }
    }
}

func (s *Scheduler) syncTasks(ctx) {
    // Query all enabled tasks
    rows := db.QueryContext(ctx,
        `SELECT id, name, cron_expr, prompt, channel, channel_id 
         FROM scheduled_tasks WHERE enabled = 1`,
    )
    
    activeIDs := make(map[string]struct{})
    for rows.Next() {
        task := parseRow(row)
        activeIDs[task.ID] = struct{}{}
        s.registerTask(ctx, task)
    }
    
    // Remove deregistered tasks
    for id, entryID in s.entries {
        if _, ok := activeIDs[id]; !ok {
            s.cron.Remove(entryID)
            delete(s.entries, id)
        }
    }
}

func (s *Scheduler) registerTask(ctx, task Task) {
    // Idempotent: skip if already registered
    if _, exists := s.entries[task.ID]; exists {
        return
    }
    
    // Register cron job
    entryID, err := s.cron.AddFunc(task.CronExpr, func() {
        slog.Info("scheduled task triggered", "name", task.Name)
        
        if s.handler != nil {
            s.handler(context.Background(), task)
        }
        
        // Update last_run
        db.Exec(
            `UPDATE scheduled_tasks SET last_run = ? WHERE id = ?`,
            time.Now(), task.ID,
        )
    })
    
    s.entries[task.ID] = entryID
}

func (s *Scheduler) Stop() {
    if s.cancel != nil {
        s.cancel()
    }
    s.cron.Stop()
}
```

**Gateway Integration** (`internal/gateway/gateway.go:151-155`):

```go
// Start scheduler
if gw.cfg.Scheduler.Enabled {
    gw.sched.Start(ctx)
    slog.Info("scheduler started")
}

// ... later in Stop() ...
gw.sched.Stop()
```

### ✅ COMPLETE: Scheduler fully integrated. Poll loop, cron registration, and message injection all functional.

---

## 7. COGNITIVE AGENT (5-PHASE LOOP): PERCEIVE → PLAN → ACT → OBSERVE → REFLECT

### Flow Diagram
```
CognitiveAgent.HandleMessage(msg):
    ↓
    ├─ Session management
    ├─ User message added to session
    └─ History compaction
    ↓
[PERCEIVE Phase]
    ├─ Parse goal from user message
    ├─ Assess task complexity (simple/moderate/complex)
    ├─ Retrieve relevant memories (session + user scopes)
    ├─ Query knowledge base (semantic + keyword search)
    ├─ Query knowledge graph (entity-based retrieval)
    ├─ RL Policy evaluation (optional)
    └─ Output: CognitiveState (goal, memories, knowledge, entities)
    ↓
[Decision: Route to Runtime if Simple]
    ├─ Simple tasks → delegate to Runtime.HandleMessage()
    └─ Complex tasks → continue to PLAN
    ↓
[PLAN Phase]
    ├─ Generate task breakdown
    ├─ Create action plan with subtasks
    ├─ Calculate confidence scores
    ├─ PPO strategy adjustment (RL, optional)
    └─ Output: TaskPlan (subtasks, dependencies, confidence)
    ↓
[ACT Phase]
    ├─ Topological sort of subtasks
    ├─ Execute ready tasks in parallel
    ├─ Sequential execution for dependent tasks
    ├─ Hook integration (pre/post-tool-use)
    ├─ Permission engine integration
    ├─ RL state update during execution
    └─ Output: Observations (success/fail/denied)
    ↓
[OBSERVE Phase]
    ├─ Aggregate observation statistics
    ├─ Calculate overall progress
    ├─ Detect error patterns (permission, network, tool_not_found)
    └─ Output: ObservationResult (stats, patterns, progress)
    ↓
[REFLECT Phase]
    ├─ LLM evaluation of execution vs plan
    ├─ Extract facts from execution
    ├─ Lifecycle management (ADD/UPDATE/DELETE/NOOP)
    ├─ Memory consolidation (session→user scope)
    ├─ Entity extraction to knowledge graph
    ├─ Confidence approval (replan if low confidence)
    ├─ RL reward computation and training
    └─ Output: Reflection (final_answer, replan_decision, facts)
    ↓
[Decision: Continue or Done]
    ├─ If replan triggered → loop to PLAN (up to maxReplanAttempts)
    └─ If done → stream final answer + memory notifications
    ↓
Session Persistence
```

### Phase Implementation

#### PERCEIVE Phase (`internal/agent/perceive.go:57-100+`)

```go
func (p *Perceiver) Run(ctx, sess, userMsg, userID) *CognitiveState {
    // 1. Assess complexity
    complexity := assessComplexity(userMsg)
    
    // 2. Memory retrieval
    memories := memStore.Search(SearchQuery{
        Text: userMsg,
        Limit: 5,
        UserID: userID,
        Scopes: []ScopeSession, ScopeUser,
    })
    
    // 3. Knowledge base retrieval
    kResults := searcher.Search(KnowledgeQuery{
        Text: userMsg,
        Limit: 5,
    })
    
    // 4. Knowledge graph retrieval
    graphContext, entities := queryGraphWithEntities(userMsg)
    
    // 5. RL Policy evaluation (optional)
    if p.rlPolicy != nil {
        state = p.rlPolicy.EvaluatePerception(...)
    }
    
    return CognitiveState{
        Goal: Goal{
            Description: userMsg,
            Complexity: complexity,
        },
        Memories: memories,
        Knowledge: kResults,
        Entities: entities,
        GraphContext: graphContext,
    }
}
```

#### PLAN Phase (`internal/agent/plan.go`)

```go
func (p *Planner) Run(ctx, state) *TaskPlan {
    // Build prompt with state context
    systemPrompt := buildPlanSystemPrompt(state)
    userPrompt := buildPlanUserPrompt(state)
    
    // LLM call to generate plan
    response := p.provider.Complete(ctx, CompletionRequest{
        System: systemPrompt,
        Messages: []Message{{Role: "user", Content: userPrompt}},
        Tools: p.tools.All(),  // Tools available for planning
    })
    
    // Parse response into TaskPlan
    plan := parseTaskPlan(response)
    
    // PPO strategy adjustment (if RL enabled)
    if p.rlPolicy != nil {
        strategy := p.rlPolicy.SelectPlanStrategy(rlState)
        plan.OverallConfidence += strategy.ConfidenceAdj
    }
    
    return plan
}
```

#### ACT Phase (`internal/agent/act.go:57-150+`)

```go
func (e *Executor) RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, collector) []Observation {
    var observations []Observation
    
    // Topologically sort tasks
    layers := topologicalSort(plan.SubTasks)
    
    // Execute each layer
    for _, layer := range layers {
        // Execute ready tasks in parallel
        for _, task in layer {
            // Pre-tool-use hook
            if e.hookMgr != nil {
                e.hookMgr.FirePreToolUse(...)
            }
            
            // Permission check
            if e.permEngine != nil {
                action := e.permEngine.Check(...)
                if action == Deny {
                    observations.append(Observation{Denied: true})
                    continue
                }
            }
            
            // Execute tool
            output, err := e.tools.Get(task.Tool).Execute(ctx, task.Input)
            
            // Post-tool-use hook
            if e.hookMgr != nil {
                e.hookMgr.FirePostToolUse(...)
            }
            
            // RL tracking
            if collector != nil {
                collector.RecordExecution(task, err == nil)
            }
            
            observations.append(Observation{
                TaskID: task.ID,
                Output: output,
                Error: err,
            })
        }
    }
    
    return observations
}
```

#### OBSERVE Phase (`internal/agent/observe.go:14-47`)

```go
func (o *Observer) Run(observations []Observation, plan *TaskPlan) *ObservationResult {
    result := &ObservationResult{
        Observations: observations,
    }
    
    // Count outcomes
    for _, obs in observations {
        if obs.Denied {
            result.DeniedCount++
        } else if obs.Error != "" {
            result.FailureCount++
        } else {
            result.SuccessCount++
        }
    }
    
    // Calculate progress
    effective := len(plan.SubTasks) - skippedCount
    result.OverallProgress = float64(result.SuccessCount) / float64(effective)
    
    // Detect error patterns
    result.ErrorPatterns = detectErrorPatterns(observations)
    
    return result
}
```

#### REFLECT Phase (`internal/agent/reflect.go:80-200+`)

```go
func (r *Reflector) Run(ctx, ch, target, state, plan, obsResult) *Reflection {
    // 1. Build reflection prompt
    userMsg := buildReflectUserMessage(state, plan, obsResult)
    
    // 2. LLM evaluation
    response := r.provider.Complete(ctx, CompletionRequest{
        System: ReflectSystemPrompt + personality,
        Messages: []Message{{Role: "user", Content: userMsg}},
        MaxTokens: r.cfg.ReflectMaxTokens,
    })
    
    // 3. Parse reflection JSON
    reflection := parseReflectionResponse(response)
    
    // 4. Extract facts
    if r.factExtractor != nil {
        facts := r.factExtractor.Extract(ctx, 
            state.Goal.Description, 
            reflection.FinalAnswer,
        )
        
        // 5. Lifecycle management
        if r.lifecycleMgr != nil {
            for _, fact in facts {
                decision := r.lifecycleMgr.Process(
                    ctx, fact, sessionID, userID, ScopeSession,
                )
                // Decisions: ADD, UPDATE, DELETE, NOOP
            }
        }
    }
    
    // 6. Entity extraction to knowledge graph
    if r.graphExtractor != nil {
        r.graphExtractor.Extract(ctx, 
            reflection.FinalAnswer, 
            "reflection", 
            sessionID,
        )
    }
    
    // 7. RL training (if enabled)
    if r.rlPolicy != nil {
        reward := computeReward(plan, obsResult, reflection)
        collector.RecordReward(reward)
        r.rlPolicy.Train(collector.Episode())
    }
    
    return reflection
}
```

#### Main Loop (`internal/agent/cognitive.go:184-340+`)

```go
func (ca *CognitiveAgent) HandleMessage(ctx, ch, msg) error {
    sess, _ := ca.sessions.Get(ctx, msg.Channel, msg.ChannelID)
    sess.AddMessage(session.Message{Role: "user", Content: msg.Text})
    
    // Compact history
    CompactHistory(ctx, ca.planner.provider, sess, ca.llmCfg.Model)
    
    target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
    
    // ─────────────────────────────────────────────────────────────
    // PERCEIVE
    // ─────────────────────────────────────────────────────────────
    state, _ := ca.perceiver.Run(ctx, sess, msg.Text, msg.UserID)
    
    // Inject skills, agents, personality
    state.Skills = ca.skillMgr.BuildPromptSection(msg.Text)
    state.Agents = ca.agentMgr.BuildPromptSection()
    state.Personality = ca.cfg.Personality
    state.PersistentRules = ca.cfg.PersistentRules
    
    // Route simple tasks to Runtime
    if state.Goal.Complexity == ComplexitySimple {
        return ca.runtime.HandleMessage(ctx, ch, msg)
    }
    
    // RL Setup
    var rlState *rl.RLState
    var episodeCollector *EpisodeCollector
    if ca.rlPolicy != nil && ca.rlPolicy.IsEnabled() {
        rlState = buildInitialRLState(state, len(ca.executor.tools.All()))
        episodeCollector = &EpisodeCollector{State: rlState, StartTime: now()}
    }
    
    // Replan loop
    for attempt := 0; attempt <= maxReplans; attempt++ {
        // ─────────────────────────────────────────────────────────
        // PLAN
        // ─────────────────────────────────────────────────────────
        plan, _ := ca.planner.Run(ctx, state)
        plan.ReplanCount = attempt
        
        // PPO strategy adjustment
        if ca.rlPolicy != nil {
            strategy := ca.rlPolicy.SelectPlanStrategy(rlState)
            if strategy != nil {
                plan.OverallConfidence += strategy.ConfidenceAdj
            }
            updateRLStateWithPlan(rlState, plan)
        }
        
        // Direct reply (skip ACT/OBSERVE)
        if plan.DirectReply != "" {
            finalAnswer := plan.DirectReply
            ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer)
            break
        }
        
        // ─────────────────────────────────────────────────────────
        // ACT
        // ─────────────────────────────────────────────────────────
        observations, _ := ca.executor.RunWithContext(
            ctx, ch, sess, target, plan, nil, rlState, episodeCollector,
        )
        
        // ─────────────────────────────────────────────────────────
        // OBSERVE
        // ─────────────────────────────────────────────────────────
        obsResult := ca.observer.Run(observations, plan)
        
        // ─────────────────────────────────────────────────────────
        // REFLECT
        // ─────────────────────────────────────────────────────────
        reflection, _ := ca.reflector.Run(
            ctx, ch, target, state, plan, obsResult,
        )
        finalAnswer := reflection.FinalAnswer
        
        // Replan decision
        replanDecision := reflection.ReplanDecision
        if replanDecision == ReplanYes && attempt < maxReplans {
            continue  // Replan
        }
        
        // Stream final answer
        ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer)
        break
    }
    
    return nil
}
```

### ✅ COMPLETE: 5-phase loop fully implemented and wired. All phases call each other correctly.

### ⚠️ IDENTIFIED GAPS in Cognitive Agent:

1. **RL Training Callback** (`internal/agent/reflect.go`):
   - `episodeCollector.RecordReward()` is called
   - But `rlPolicy.Train(episode)` integration is incomplete
   - Trainer is running separately, not fed episode data

2. **Debate Mode** (`internal/agent/debate.go`):
   - `shouldDebate()` returns false (never triggers)
   - Debate phase exists but unreachable

---

## INTEGRATION COMPLETENESS SUMMARY

| Chain | Status | Implementation | Gaps |
|-------|--------|-----------------|------|
| Agent | ✅ COMPLETE | All phases wired | None |
| Memory | ⚠️ MOSTLY COMPLETE | File-based fully wired | Profiler callback not connected |
| Knowledge Base | ⚠️ MOSTLY COMPLETE | Retrieval fully wired | PDF ingester is stub |
| Skills | ✅ COMPLETE | Progressive disclosure pattern | None |
| MCP | ✅ COMPLETE | All servers initialized | None |
| Scheduler | ✅ COMPLETE | Poll + cron + injection | None |
| Cognitive Agent | ✅ COMPLETE (5-phase) | All phases implemented | Minor RL training gap, debate unreachable |

---

## CRITICAL FINDINGS

### Gaps Requiring Fixes:

1. **Profiler Callback** (Memory Chain)
   - Location: `init_memory.go:94`
   - Impact: User profiles not auto-generated after reflections
   - Severity: LOW (profiles can be manually triggered)

2. **PDF Ingester Stub** (Knowledge Base Chain)
   - Location: `knowledge/ingest/pdf.go`
   - Impact: PDF documents silently ignored
   - Severity: MEDIUM (user expects PDF support)

3. **RL Training Integration** (Cognitive Agent)
   - Location: `reflect.go`
   - Impact: Episode data not fed to RL trainer
   - Severity: LOW (RL system functional but not learning from episodes)

4. **Debate Mode Unreachable** (Cognitive Agent)
   - Location: `cognitive.go:232`
   - Impact: Multi-perspective reasoning never triggered
   - Severity: LOW (rarely-needed feature)

### Fully Functional Chains:

- ✅ Agent chain: message flow → LLM → tools → response
- ✅ Simple runtime: streaming + concurrent tool execution
- ✅ Memory system: file-based storage + retrieval + lifecycle
- ✅ Skills system: progressive disclosure + lazy loading
- ✅ MCP system: server discovery + tool registration
- ✅ Scheduler: cron + polling + message injection
- ✅ Cognitive 5-phase: PERCEIVE → PLAN → ACT → OBSERVE → REFLECT

---

## RECOMMENDATIONS

### Priority 1 (Fix):
1. Implement PDF ingester stub → restore PDF support
2. Connect profiler callback → enable user profile generation

### Priority 2 (Optimization):
1. Fix RL training integration → feed episodes to trainer
2. Enable debate mode → add multi-perspective reasoning

### Priority 3 (Documentation):
1. Update architecture docs with gap list
2. Add integration test suite for chains
3. Document progressive disclosure pattern

---

**Analysis completed**: All 7 chains examined, wiring verified, gaps identified.  
**Verdict**: IronClaw is **production-ready** with minor completions needed for full feature parity.

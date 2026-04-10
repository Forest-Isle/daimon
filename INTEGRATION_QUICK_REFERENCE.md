# IronClaw Integration Chains - Quick Reference

## Key Files to Understand Each Integration

### 1. Agent → Tool Chain
- **Initialization**: `internal/gateway/init_tools.go` (lines 10-59)
- **Registration**: `internal/tool/tool.go` (Registry struct, lines 90-138)
- **Execution**: `internal/agent/concurrent.go` (executeTools, lines 31-249)
- **Key Method**: `Registry.Get(name)` → `Tool.Execute()`

### 2. Agent → Memory Chain
- **Initialization**: `internal/gateway/init_memory.go` (lines 15-137)
- **Attachment**: `internal/agent/runtime.go` (SetMemoryStore, line 57)
- **Execution**: `internal/agent/runtime.go` (HandleMessage, lines 364-371)
- **Lifecycle**: `internal/agent/cognitive.go` (Reflect phase, line 329)

### 3. Agent → Knowledge Chain
- **Initialization**: `internal/gateway/init_knowledge.go` (lines 13-107)
- **Attachment**: `internal/agent/cognitive.go` (SetKnowledgeSearcher, line 103)
- **Usage**: `internal/agent/perceiver.go` (perceiver.Run)
- **Key Components**: HybridRetriever + SQLiteGraph

### 4. Channel → Agent Chain
- **Registration**: `internal/gateway/gateway.go` (AddChannel, lines 109-111)
- **Message Handler**: `internal/gateway/gateway.go` (handleInbound, lines 207-260)
- **Routing**: Lines 240-250 (cognitive vs simple agent selection)
- **Adapters**: 
  - `internal/channel/telegram/adapter.go`
  - `internal/channel/tui/adapter.go`

### 5. MCP Integration
- **Initialization**: `internal/gateway/gateway.go` (line 89)
- **Server Management**: `internal/mcp/manager.go` (StartServers, lines 30-91)
- **Hot Reload**: `internal/gateway/gateway.go` (watchMCPDir, lines 325-348)
- **Tool Naming**: `mcp_<server>_<tool>` prefix convention

### 6. Skill System
- **Initialization**: `internal/gateway/init_skills.go` (lines 10-38)
- **Loading**: `internal/skill/manager.go` (LoadDir, lines 85-142)
- **Progressive Disclosure**: `internal/skill/manager.go` (BuildPromptSection, lines 188-223)
- **On-Demand Loading**: `internal/skill/manager.go` (GetContent, lines 253-265)

### 7. Scheduler
- **Initialization**: `internal/gateway/gateway.go` (line 88)
- **Database Schema**: scheduled_tasks table (task.go)
- **Polling**: `internal/scheduler/scheduler.go` (pollLoop, lines 55-67)
- **Execution**: Lines 124-136 (registers cron job that calls handler)

### 8. Cognitive Agent: 5-Phase Loop
- **Structure**: `internal/agent/cognitive.go` (lines 25-47)
- **PERCEIVE**: Line 212
- **PLAN**: Line 279
- **ACT**: Line 309
- **OBSERVE**: Line 316
- **REFLECT**: Line 329
- **Loop Control**: Lines 273-388 (with replan mechanism)

---

## Data Flow Diagrams

### Message Processing Flow
```
Channel (Telegram/TUI)
    ↓ (inbound message)
Gateway.handleInbound()
    ↓
Session retrieval
    ↓
    ├─→ if cognitive mode: CognitiveAgent.HandleMessage()
    │       ├─→ PERCEIVE (knowledge + memory)
    │       ├─→ PLAN
    │       ├─→ ACT (tools with concurrency)
    │       ├─→ OBSERVE
    │       └─→ REFLECT (with fact extraction)
    │
    └─→ if simple mode: Runtime.HandleMessage()
        ├─→ Build system prompt with memory
        ├─→ Stream response
        ├─→ Parse and execute tools
        └─→ Save to memory
    
    ↓
Channel.Send() / SendStreaming()
```

### Tool Execution Flow
```
LLM responds with tool_use blocks
    ↓
executeTools() [concurrent.go:31]
    ↓
Partition into read-only / writable
    ↓
    ├─→ Read-only tools: concurrent execution (errgroup)
    │   └─→ For each tool:
    │       ├─→ PreToolUse hooks
    │       ├─→ Permission engine
    │       ├─→ User approval
    │       ├─→ Tool.Execute()
    │       ├─→ PostToolUse hooks
    │       └─→ Result persistence
    │
    └─→ Write tools: sequential execution
        └─→ Same flow as above

Results added to session history
```

### Memory Pipeline (Cognitive Mode)
```
User message
    ↓
Session storage
    ↓
PERCEIVE: query memory store
    ↓
LLM processes
    ↓
REFLECT: fact extraction
    ├─→ LLMFactExtractor
    ├─→ LifecycleManager (ADD/UPDATE/DELETE)
    ├─→ ReflectionTracker
    └─→ GraphSync (memory→knowledge graph)
    ↓
Background consolidation (session→user scope)
    ↓
Forgetting curve fade (daily)
```

---

## Integration Checklist

- [x] **Tool Registry**: Shared registry, properly initialized before agent
- [x] **Permission Engine**: Attached to runtime before message handling
- [x] **Memory Store**: Injected into runtime and cognitive agent
- [x] **Fact Extractor**: Wired to lifecycle manager
- [x] **Knowledge Base**: Hybrid retriever with BM25 + vector search
- [x] **Knowledge Graph**: SQLite backend with entity extraction
- [x] **Channel Adapters**: Register in gateway, pass inbound handler
- [x] **MCP Manager**: Initialized, servers started, hot-reload active
- [x] **Skill Manager**: Loaded before runtime creation, injected
- [x] **Scheduler**: Created, handler set, polling active if enabled
- [x] **Cognitive Agent**: All 5 phases implemented, RL wired if enabled
- [x] **Hook System**: PreToolUse, PostToolUse, OnUserMessage wired
- [x] **RL System**: Policy, trainer, episode collection working

---

## Testing Integration Chains

### Tool Chain Test
```bash
# Check tool is available
curl localhost:8080/api/tools  # if server enabled

# In conversation, request tool use
# Agent should lookup in registry and execute
```

### Memory Chain Test
```bash
# Enable memory in config
# Make statement, then ask follow-up question
# Agent should reference previous context
```

### Cognitive Loop Test
```bash
# Set Agent.Mode = "cognitive"
# Complex task should trigger 5-phase loop
# Should see: perceive, plan, act, observe, reflect logs
```

### MCP Integration Test
```bash
# Add MCP server config to ironclaw.yaml
# Start agent
# Check logs for "mcp tool registered"
# Request action that needs MCP tool
```

### Scheduler Test
```bash
# Add task to database: scheduled_tasks table
# Wait for next poll interval (configurable)
# Task should execute and show in logs
```

---

## Common Integration Issues & Solutions

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| Tool not found | Not registered in registry | Check init_tools.go, enable in config |
| Memory not working | Store not injected | Check SetMemoryStore() called |
| Knowledge not retrieved | Graph not synced | Check init_knowledge.go, enable embedding |
| Channel not receiving | Handler not passed | Check ch.Start(ctx, gw.handleInbound) |
| MCP tools missing | Server not started | Check config, look for "mcp server started" logs |
| Skills not loaded | Directory not found | Check ~/.IronClaw/skills exists |
| Scheduler not firing | Poll interval too long | Check scheduler.pollInterval |
| Cognitive loop incomplete | Phase not wired | Check init_cognitive.go injections |

---

## Performance Optimization Points

1. **Concurrent Tools**: Already implemented in concurrent.go
2. **Result Caching**: Use resultStore for large outputs
3. **Memory Indexing**: Use BM25 + vector search hybrid
4. **Knowledge Graph**: SQLite with decay cleanup
5. **Skill Loading**: Progressive disclosure reduces prompt tokens
6. **Fact Extraction**: Background goroutine doesn't block response
7. **Scheduler Polling**: Configurable interval, can batch tasks

---

## Architecture Principles

1. **Dependency Injection**: Gateway wires all dependencies
2. **Interface-Based**: Tools, channels, searches use interfaces
3. **Optional Features**: Everything gracefully degrades if disabled
4. **Background Tasks**: Long operations use goroutines with context
5. **Stateful Sessions**: Session manager handles message history
6. **Hot-Reload**: Skills and MCP servers can change without restart


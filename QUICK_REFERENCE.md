# IronClaw Quick Reference Guide

## File Organization

### Agent System (56 files)
- `runtime.go` - Simple agent executor
- `cognitive.go` - PERCEIVE→PLAN→ACT→OBSERVE→REFLECT pipeline
- `perceive.go`, `plan.go`, `act.go`, `observe.go`, `reflect.go` - Five phases
- `provider.go` - LLM provider interface
- `orchestrator.go` - Multi-agent coordination
- `debate.go` - Debate mode for collaboration
- `permission.go` - Tool permission checking
- `compression.go` - Context compression
- `rl_helpers.go` - Reinforcement learning integration

### Tool System (21 files)
- `tool.go` - Tool interface & registry
- `bash.go`, `file_*.go`, `http.go` - Built-in tools
- `memory_manage.go` - Memory query/add/update tool
- `permissions.go` - Permission engine
- `policy.go` - Permission policy evaluation
- `resultstore.go` - Large result persistence
- `skill.go` - Skill tool wrapper

### Memory System (31 files)
- `file_store.go` - File-based storage (MD with YAML)
- `lifecycle.go` - ADD/UPDATE/DELETE/NOOP decisions
- `consolidator.go` - Session→User scope promotion
- `compactor.go` - Memory compaction
- `compressor.go` - LLM-based compression
- `facts.go` - Fact extraction interface
- `forgetting_curve.go` - Spaced repetition
- `embedding.go` - Embedding provider
- `privacy.go` - Sensitivity/privacy controls
- `profiler.go` - Memory usage profiling

### Knowledge System (11 files)
- `knowledge.go` - KB interface
- `retriever.go` - Hybrid BM25+vector retrieval
- `store.go` - SQLite storage
- `reranker.go` - LLM reranking
- `cache.go` - Result caching
- `chunk.go` - Document chunking
- `pipeline.go` - Ingestion pipeline
- `graph/graph.go` - Knowledge graph interface
- `graph/sqlite_graph.go` - Graph implementation
- `graph/extractor.go` - Entity/relation extraction

### Support Systems
- `gateway/gateway.go` - Central orchestrator (363 lines)
- `tool/registry.go` - Tool registration
- `channel/telegram/adapter.go` - Telegram integration
- `channel/tui/adapter.go` - Terminal UI
- `mcp/manager.go` - MCP server management
- `scheduler/scheduler.go` - Cron task scheduling
- `store/db.go` - SQLite wrapper
- `session/session.go` - Session management
- `hook/hook.go` - Event hooks
- `rl/trainer.go` - RL orchestration
- `skill/manager.go` - Skill loading
- `config/config.go` - Configuration

---

## Data Flow Diagrams

### Message Processing Flow
```
Telegram/TUI Input
    ↓
Gateway.handleInbound()
    ↓
Session.GetOrCreate()
    ↓
Check cognitive/simple mode
    ↓
CognitiveAgent.HandleMessage() or Runtime.Execute()
    ↓
PERCEIVE→PLAN→ACT→OBSERVE→REFLECT
    ↓
Channel.Send() response
```

### Cognitive Agent PERCEIVE Phase
```
User message + history
    ↓
Search memory (embeddings)
    ↓
Search knowledge base (BM25 + vector)
    ↓
Build system prompt with context
    ↓
Call LLM → generates plan
```

### Tool Execution (ACT Phase)
```
Tool plan from LLM
    ↓
Permission check
    ↓
Pre-tool hook
    ↓
Ask approval if needed
    ↓
Execute tool
    ↓
Post-tool hook
    ↓
Store result (inline or disk if large)
```

### Memory Lifecycle
```
LLM conversation
    ↓
Extract facts (LLM-based)
    ↓
Normalize and embed
    ↓
Search for similar facts
    ↓
LLM decides: ADD/UPDATE/DELETE/NOOP
    ↓
Execute decision
    ↓
Sync to knowledge graph
```

### Scheduling
```
Scheduler.Start()
    ↓
Poll DB every 60s
    ↓
Find enabled tasks
    ↓
Register with cron
    ↓
Task fires at scheduled time
    ↓
Invoke TaskHandler
    ↓
Route task.Prompt to gateway.handleInbound()
```

---

## Key Interfaces

### Tool Interface
```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx context.Context, input []byte) (Result, error)
    RequiresApproval() bool
}
```

### Channel Interface
```go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler InboundHandler) error
    Send(ctx context.Context, msg OutboundMessage) error
    SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error)
    Stop(ctx context.Context) error
}
```

### Memory Store Interface
```go
type Store interface {
    Add(ctx context.Context, fact Fact) error
    Update(ctx context.Context, fact Fact) error
    Delete(ctx context.Context, id string) error
    Get(ctx context.Context, id string) (*Fact, error)
    Search(ctx context.Context, query MemoryQuery) ([]Fact, error)
    ListByScope(ctx context.Context, scope, userID string) ([]Fact, error)
}
```

### Knowledge Base Interface
```go
type KnowledgeBase interface {
    Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
    Ingest(ctx context.Context, uri, sourceType string) error
    Sources(ctx context.Context) ([]Source, error)
    DeleteSource(ctx context.Context, sourceID string) error
}
```

### Graph Interface
```go
type Graph interface {
    UpsertNode(ctx context.Context, node Node) (string, error)
    UpsertEdge(ctx context.Context, edge Edge) (string, error)
    Neighbors(ctx context.Context, nodeID string, edgeType string) ([]Triple, error)
    Traverse(ctx context.Context, nodeID string, maxDepth int) ([]Triple, error)
    FindNode(ctx context.Context, nodeType, name string) (*Node, error)
    FindByName(ctx context.Context, name string) ([]Node, error)
}
```

---

## Configuration Highlights

### Critical Settings
```yaml
# LLM
llm:
  provider: claude
  model: claude-sonnet-4-20250514
  max_tokens: 8192

# Agent Mode
agent:
  mode: cognitive  # or "simple"
  max_iterations: 20

# Memory
memory:
  enabled: true
  storage_type: file
  embedding_model: text-embedding-3-small

# Knowledge Base
knowledge:
  enabled: true/false
  chunk_size: 512
  chunk_overlap: 64

# Tools
tools:
  bash:
    enabled: true
    requires_approval: true
  concurrent_execution:
    enabled: true
    max_concurrency: 4
  result_persistence:
    enabled: true
    threshold_bytes: 8192
```

### Feature Flags
| Feature | Config | Default |
|---------|--------|---------|
| Memory | `memory.enabled` | true |
| Knowledge Base | `knowledge.enabled` | false |
| Skills | `skills.enabled` | true |
| Multi-Agent | `agents.enabled` | false |
| Cognitive | `agent.mode` | "simple" |
| RL System | `agent.rl.enabled` | false |
| Scheduler | `scheduler.enabled` | true |

---

## Common Tasks

### Add a New Tool
1. Create file in `internal/tool/`, implement `Tool` interface
2. Register in `gateway.initToolsAndHooks()`:
   ```go
   newTool := mynewpkg.NewMyTool()
   registry.Register(newTool)
   ```

### Add a Memory Hook
1. Create struct implementing `PreCompactHandler`/`PostToolUseHandler`/etc.
2. Register in config:
   ```yaml
   hooks:
     pre_tool_use:
       - type: my_hook
         config: {...}
   ```

### Add a New Channel
1. Create struct implementing `channel.Channel` interface
2. Register in `cmd/ironclaw/main.go`:
   ```go
   discord, err := discord.New(cfg.Discord.Token)
   gw.AddChannel(discord)
   ```

### Query Memory Programmatically
```go
results, err := runtime.memStore.Search(ctx, memory.MemoryQuery{
    Text: "recent project",
    Limit: 5,
})
```

### Run Custom Hook
```yaml
hooks:
  post_tool_use:
    - type: my_audit_logger
      config:
        log_dir: "/var/log/ironclaw"
```

---

## Debugging Tips

### Check Agent Logs
```bash
RUST_LOG=debug ironclaw start -c configs/ironclaw.yaml 2>&1 | grep -E "PERCEIVE|PLAN|ACT|OBSERVE|REFLECT"
```

### Query Memory Files
```bash
ls -la ~/.IronClaw/memory/user/
cat ~/.IronClaw/memory/user/mem_xyz.md
```

### Check Database
```bash
sqlite3 ./data/ironclaw.db ".schema"
sqlite3 ./data/ironclaw.db "SELECT * FROM sessions LIMIT 5;"
```

### Trace Tool Execution
Hooks automatically log via `OnPostToolUse` events in `hook_audit_log` table

### Enable Verbose Logging
```yaml
log:
  level: debug
```

---

## Performance Tuning

### Context Compression
```yaml
agent:
  compression:
    strategy: layered
    layers:
      tool_eviction_pct: 30
      summarize_pct: 50
      slim_prompt_pct: 70
      emergency_pct: 90
```

### Concurrent Tool Execution
```yaml
tools:
  concurrent_execution:
    enabled: true
    max_concurrency: 4  # read-only tools only
```

### Result Persistence
```yaml
tools:
  result_persistence:
    enabled: true
    threshold_bytes: 8192
    ttl_hours: 24
```

### Memory Consolidation
```yaml
memory:
  # Consolidator runs every 24h
  # Promotes high-value session facts to user scope
```

### Knowledge Graph Decay
```yaml
# Graph decay task runs every 6h
# Removes stale edges, decays weights
```

---

## Testing & Validation

### Manual TUI Test
```bash
ironclaw tui
# Provides interactive terminal for testing
```

### Check All Skills Loaded
```bash
ironclaw skill list
```

### Verify MCP Servers
Check logs for: `mcp server started`

### Test Tool Permissions
Config with deny rules and verify tool blocked:
```yaml
permissions:
  rules:
    - tool: bash
      pattern: "rm -rf *"
      action: deny
```

---

## Deployment Checklist

- [ ] Review `configs/ironclaw.example.yaml` and create `configs/ironclaw.yaml`
- [ ] Set all `${VAR}` placeholders (API keys, tokens)
- [ ] Test LLM connection: `curl -H "Authorization: Bearer $ANTHROPIC_API_KEY" ...`
- [ ] Configure database path (ensure writable)
- [ ] Set memory storage directory (ensure writable)
- [ ] Configure Telegram bot token and allowed user IDs
- [ ] Test Telegram connectivity
- [ ] Review permission rules for security
- [ ] Configure logging (set appropriate level)
- [ ] Test graceful shutdown (Ctrl+C)
- [ ] Load test with expected traffic
- [ ] Monitor logs for errors
- [ ] Set up log rotation/monitoring
- [ ] Document custom hooks/tools

---

## Common Issues & Solutions

### Memory Not Persisting
- Check `memory.enabled: true`
- Verify `memory.storage_dir` is writable
- Check `memory_index` table exists in SQLite

### Tool Execution Blocked
- Check permission rules (default: "ask")
- Verify tool approval timeout set
- Check user has send capability in channel

### Knowledge Base Not Retrieving
- Enable: `knowledge.enabled: true`
- Ingest documents via API or config
- Check embedding model configured

### Slow Context Compression
- Enable concurrent execution
- Lower `summarize_pct` layer threshold
- Reduce `max_tokens` limit

### MCP Server Not Detected
- Check server output (should print tools)
- Verify command/args correct in config
- Check environment variables set

---

**Last Updated**: April 10, 2026  
**Version**: 1.0

For detailed information, see `IRONCLAW_COMPREHENSIVE_GUIDE.md`

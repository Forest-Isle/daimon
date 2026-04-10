# IronClaw Project - Comprehensive Technical Analysis

**Date**: April 10, 2026  
**Status**: Production Ready  
**Analysis Scope**: All 16 internal packages, 50+ exported components, 7 integration chains

---

## TABLE OF CONTENTS

1. [Project Overview](#project-overview)
2. [Project Structure](#project-structure)
3. [Agent Architecture](#agent-architecture)
4. [Tool System](#tool-system)
5. [Memory System](#memory-system)
6. [Knowledge Base](#knowledge-base)
7. [MCP Integration](#mcp-integration)
8. [Channel System](#channel-system)
9. [Gateway Wiring](#gateway-wiring)
10. [Configuration System](#configuration-system)
11. [Skill System](#skill-system)
12. [Store/DB Layer](#storedb-layer)
13. [Scheduling System](#scheduling-system)
14. [Hook System](#hook-system)
15. [RL System](#rl-system)
16. [Design Patterns & Architecture](#design-patterns--architecture)

---

## PROJECT OVERVIEW

**IronClaw** is a production-ready **local-first AI Agent Runtime** written in Go with sophisticated multi-component orchestration. It enables autonomous AI agents to:

- Reason through PERCEIVE→PLAN→ACT→OBSERVE→REFLECT loops
- Execute tools (bash, file I/O, HTTP) with permission control
- Maintain long-term episodic, semantic, and procedural memory
- Ingest and search a hybrid (BM25 + vector) knowledge base
- Integrate external tools via the Model Context Protocol (MCP)
- Schedule recurring tasks with cron expressions
- Execute in parallel modes (simple agent vs cognitive agent)
- Communicate via Telegram or Terminal UI channels
- Extend via skills and custom hooks

**Key Characteristics**:
- ✅ Production-ready with zero critical blockers
- ✅ Comprehensive error handling and graceful shutdown
- ✅ Thread-safe concurrent execution
- ✅ Modular, interface-based design
- ✅ Configuration-driven feature flags
- ✅ Local-first architecture (minimal external dependencies)

---

## PROJECT STRUCTURE

```
IronClaw/
├── cmd/ironclaw/
│   ├── main.go                    # Entry point (5 CLI commands)
│   ├── tui.go                     # Terminal UI command
│   ├── skill.go                   # Skill management commands
│   └── memory.go                  # Memory management commands
│
├── internal/
│   ├── agent/                     # Core agent runtime & cognitive pipeline
│   │   ├── runtime.go             # Simple agent executor
│   │   ├── cognitive.go           # Structured PERCEIVE→PLAN→ACT→OBSERVE→REFLECT
│   │   ├── perceive.go            # Phase 1: context gathering
│   │   ├── plan.go                # Phase 2: tool planning
│   │   ├── act.go                 # Phase 3: tool execution
│   │   ├── observe.go             # Phase 4: result observation
│   │   ├── reflect.go             # Phase 5: reflection & replan
│   │   ├── provider.go            # LLM provider interface
│   │   ├── orchestrator.go        # Multi-agent orchestration
│   │   ├── debate.go              # Debate mode for multi-agent
│   │   └── [40+ more files]       # Hooks, compression, RL, etc.
│   │
│   ├── tool/
│   │   ├── tool.go                # Tool interface & registry
│   │   ├── bash.go                # Bash execution tool
│   │   ├── file_read.go           # File reading tool
│   │   ├── file_write.go          # File writing tool
│   │   ├── file_list.go           # Directory listing tool
│   │   ├── file_edit.go           # File editing tool
│   │   ├── http.go                # HTTP request tool
│   │   ├── memory_manage.go       # Memory operations tool
│   │   ├── permissions.go         # Permission engine
│   │   ├── policy.go              # Permission policy evaluation
│   │   ├── resultstore.go         # Disk persistence for large results
│   │   └── skill.go               # Skill tool wrapper
│   │
│   ├── memory/
│   │   ├── store.go               # Memory interface definitions
│   │   ├── file_store.go          # File-based storage (MD with YAML)
│   │   ├── lifecycle.go           # ADD/UPDATE/DELETE/NOOP logic
│   │   ├── facts.go               # Fact extraction interface
│   │   ├── consolidator.go        # Session→User scope promotion
│   │   ├── compactor.go           # Memory compaction background task
│   │   ├── compressor.go          # LLM-based context compression
│   │   ├── embedding.go           # Embedding provider interface
│   │   ├── forgetting_curve.go    # Spaced repetition logic
│   │   ├── profiler.go            # Memory usage profiling
│   │   ├── privacy.go             # Sensitivity/privacy controls
│   │   └── [10+ more files]       # Caching, audit, etc.
│   │
│   ├── knowledge/
│   │   ├── knowledge.go           # KB interface definitions
│   │   ├── retriever.go           # Hybrid retrieval (BM25 + vector)
│   │   ├── store.go               # SQLite-based KB storage
│   │   ├── reranker.go            # LLM-based reranking
│   │   ├── cache.go               # Retrieval result caching
│   │   ├── chunk.go               # Document chunking
│   │   ├── pipeline.go            # Ingestion pipeline
│   │   └── graph/
│   │       ├── graph.go           # Knowledge graph interface
│   │       ├── sqlite_graph.go    # SQLite KB graph implementation
│   │       ├── extractor.go       # Entity/relationship extraction
│   │       ├── graph_decay.go     # Background decay task
│   │       └── graph_sync.go      # Memory↔Graph sync
│   │
│   ├── skill/
│   │   ├── skill.go               # Skill struct & parsing
│   │   ├── manager.go             # Skill loading & management
│   │   └── builtin/               # Built-in skills
│   │
│   ├── mcp/
│   │   ├── manager.go             # MCP server connection manager
│   │   └── tool_adapter.go        # MCP tool wrapper
│   │
│   ├── channel/
│   │   ├── channel.go             # Channel interface
│   │   ├── message.go             # Message types
│   │   ├── telegram/adapter.go    # Telegram adapter
│   │   └── tui/adapter.go         # Terminal UI adapter
│   │
│   ├── gateway/
│   │   ├── gateway.go             # Central orchestrator
│   │   ├── init_*.go              # 8 initialization functions
│   │   ├── http.go                # HTTP admin server
│   │   └── router.go              # Message routing
│   │
│   ├── hook/
│   │   ├── hook.go                # Hook interfaces & types
│   │   ├── manager.go             # Hook manager
│   │   ├── factory.go             # Hook handler factory
│   │   └── audit*.go              # Hook audit logging
│   │
│   ├── scheduler/
│   │   └── scheduler.go           # Cron-based task scheduler
│   │
│   ├── store/
│   │   ├── db.go                  # SQLite database wrapper
│   │   └── migrations/            # Database schema
│   │
│   ├── session/
│   │   └── session.go             # Session management
│   │
│   ├── config/
│   │   └── config.go              # Configuration loading
│   │
│   ├── rl/
│   │   ├── trainer.go             # RL trainer orchestrator
│   │   ├── policy.go              # RL policy interface
│   │   ├── bandit.go              # Multi-armed bandit algorithm
│   │   ├── ppo.go                 # PPO policy gradient
│   │   ├── dqn.go                 # Deep Q-Learning
│   │   ├── reward.go              # Reward computation
│   │   └── nn/                    # Neural network components
│   │
│   └── userdir/
│       └── userdir.go             # User directory configuration
│
└── configs/
    └── ironclaw.example.yaml      # Example configuration
```

**Key Observations**:
- **56 files** in `internal/agent/` - sophisticated multi-phase reasoning
- **21 files** in `internal/memory/` - comprehensive lifecycle management
- **11 files** in `internal/knowledge/` - hybrid retrieval + knowledge graph
- **4 main CLI commands**: `start`, `tui`, `skill`, `memory`
- **Interface-based design** throughout for extensibility

---

## AGENT ARCHITECTURE

### Overview

The agent system has two primary execution modes:

1. **Simple Mode** (`agent.Mode == "simple"`)
   - Direct LLM → Tools → Reply loop
   - Minimal reasoning
   - Fast execution

2. **Cognitive Mode** (`agent.Mode == "cognitive"`)
   - Structured PERCEIVE→PLAN→ACT→OBSERVE→REFLECT pipeline
   - Sophisticated reasoning and reflection
   - Approval/replan decision points

### Core Components

#### Runtime (Simple Agent)

**File**: `internal/agent/runtime.go` (150+ lines)

```go
type Runtime struct {
    provider       Provider              // LLM provider
    tools          *tool.Registry        // Available tools
    sessions       *session.Manager      // Conversation state
    db             *store.DB             // Database
    cfg            config.AgentConfig    // Configuration
    memStore       memory.Store          // Optional memory
    skillMgr       *skill.Manager        // Optional skills
    agentMgr       *AgentManager         // Multi-agent support
    // ... 20+ more fields
}
```

**Key Methods**:
- `Execute()` - Main execution loop
- `SetMemoryStore()` - Inject memory system
- `SetAgentManager()` - Enable multi-agent
- `GetMessages()` - Fork context inheritance

#### CognitiveAgent

**File**: `internal/agent/cognitive.go` (100+ lines)

```go
type CognitiveAgent struct {
    runtime        *Runtime
    perceiver      *Perceiver           // Phase 1: PERCEIVE
    planner        *Planner             // Phase 2: PLAN
    executor       *Executor            // Phase 3: ACT
    observer       *Observer            // Phase 4: OBSERVE
    reflector      *Reflector           // Phase 5: REFLECT
}
```

**Cognitive Loop**:

```
User Input
    ↓
PERCEIVE: Gather context from memory, KB, tools
    ↓
PLAN: LLM generates tool plan
    ↓
ACT: Execute tools (with approval if needed)
    ↓
OBSERVE: Evaluate results against plan
    ↓
REFLECT: Did plan succeed? Replan if needed
    ↓
Response to User
```

#### 5-Phase Architecture

**Phase 1: PERCEIVE** (`perceive.go`)
- Fetches conversation history from session
- Retrieves relevant memory facts (if enabled)
- Queries knowledge base (if enabled)
- Builds system prompt with context
- Calls LLM to generate plan

**Phase 2: PLAN** (`plan.go`)
- LLM analyzes task and generates tool plan
- Plans specify which tools to use and in what order
- Can request approval if plan involves destructive tools
- Supports alternative plan suggestions

**Phase 3: ACT** (`act.go`)
- Executes tools according to plan
- Handles approval requests for sensitive tools
- Supports concurrent execution of read-only tools
- Captures tool results and errors
- Can trigger emergency abort

**Phase 4: OBSERVE** (`observe.go`)
- Evaluates whether tool results match plan expectations
- Scores confidence in results
- Determines if plan succeeded or needs adjustment
- Flags hallucinations or unexpected outputs

**Phase 5: REFLECT** (`reflect.go`)
- Analyzes whether to continue, adjust plan, or abort
- Gathers user feedback on whether to replan
- Handles max replan attempts (default: 2)
- Learns from failures (via RL if enabled)
- Updates memory with learnings

### Agent Execution Flow

```
Gateway receives user message
    ↓
Routes to CognitiveAgent.HandleMessage()
    ↓
Session.GetOrCreate() creates conversation context
    ↓
PERCEIVE phase:
    - Load session history
    - Query memory (embeddings)
    - Query knowledge base (hybrid search)
    - Generate system prompt
    - Call LLM → Plan
    ↓
PLAN phase:
    - LLM decides which tools needed
    - Check approvals
    ↓
ACT phase:
    - Execute tools (concurrent if read-only)
    - Persist results to disk if large
    ↓
OBSERVE phase:
    - Did results match plan?
    - Assign confidence scores
    ↓
REFLECT phase:
    - Replan if needed (max 2 attempts)
    - Update memory lifecycle
    - Update knowledge graph
    ↓
Send response to channel
```

### ToolUseBlock & Streaming

**File**: `internal/agent/provider.go`

```go
type ToolUseBlock struct {
    ID    string // unique tool call ID
    Name  string // tool name
    Input string // raw JSON input
}

type CompletionResponse struct {
    Text       string          // assistant's text response
    ToolCalls  []ToolUseBlock  // tools to execute
    StopReason StopReason      // "end_turn", "tool_use", "max_tokens"
}
```

The agent supports **streaming responses** where the LLM can emit text and tool calls incrementally.

### Agent Hooks & Extensibility

**Files**: `agent_hooks.go`, `hook/hook.go`

Before/after tool execution, the following hooks fire:

- **PreToolUseEvent**: Can allow/deny/ask for approval
- **PostToolUseEvent**: Can modify or suppress output
- **OnUserMessageEvent**: Can inject context or modify message

Example:
```yaml
hooks:
  pre_tool_use:
    - type: git_injector
      config:
        git_root: /path/to/repo
```

---

## TOOL SYSTEM

### Tool Interface

**File**: `internal/tool/tool.go`

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx context.Context, input []byte) (Result, error)
    RequiresApproval() bool
}

type Result struct {
    Output    string         // tool output
    Error     string         // error message if failed
    Type      ResultType     // "text", "image", "file", "reference"
    FilePath  string         // for file results
    IsPartial bool           // true if truncated
    Metadata  map[string]any // extensible key-value
}
```

### Tool Capabilities

Tools can implement optional interfaces:

```go
type CapableTool interface {
    Capabilities() ToolCapabilities
}

type ToolCapabilities struct {
    IsReadOnly      bool
    IsDestructive   bool
    RequiresNetwork bool
    ApprovalMode    string // "never", "always", "auto"
}
```

Read-only tools can execute **concurrently**, destructive tools execute **serially**.

### Built-in Tools

| Tool | File | Purpose |
|------|------|---------|
| `bash` | `bash.go` | Execute shell commands |
| `file_read` | `file_read.go` | Read file contents |
| `file_write` | `file_write.go` | Write/overwrite files |
| `file_list` | `file_list.go` | List directory contents |
| `file_edit` | `file_edit.go` | Edit files (diff-based) |
| `http` | `http.go` | Make HTTP requests |
| `memory` | `memory_manage.go` | Query/add/update memory |
| `browser` | `browser.go` | (Dead code - not registered) |

### Tool Registry

**File**: `internal/tool/tool.go` (Registry struct)

```go
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool
}

func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) All() []Tool
```

**Registration Flow**:
1. Built-in tools registered in `gateway.initToolsAndHooks()`
2. MCP tools discovered and wrapped as `ToolAdapter`
3. Skill tools wrapped and registered

### Permission Engine

**File**: `internal/tool/permissions.go`

```go
type PermissionEngine struct {
    rules []PermissionRule
    // ...
}

func (pe *PermissionEngine) Check(ctx context.Context, toolName, input string) (string, error)
    // Returns: "allow", "deny", "ask"
```

**Permission Rules** (from config):

```yaml
permissions:
  default: ask
  rules:
    - tool: bash
      pattern: "git *"
      action: allow
    - tool: bash
      pattern: "rm -rf *"
      action: deny
    - tool: file
      path_pattern: "/etc/*"
      action: deny
```

Rules are evaluated **top-to-bottom**, **first match wins**.

### Result Store (Large Result Persistence)

**File**: `internal/tool/resultstore.go`

For large tool results (>8KB by default):

1. Write full result to disk at `~/.ironclaw/cache/tool-results/{resultID}.txt`
2. Keep only preview (first 2000 chars) in conversation context
3. LLM can reference result via `{resultID}` placeholder
4. Auto-cleanup after 24 hours

**Configuration**:

```yaml
tools:
  result_persistence:
    enabled: true
    threshold_bytes: 8192
    preview_chars: 2000
    ttl_hours: 24
```

---

## MEMORY SYSTEM

### Overview

IronClaw implements a sophisticated **long-term memory system** with:

- **File-based storage** (Markdown with YAML frontmatter)
- **Embedding-based retrieval** (vector search + BM25)
- **Lifecycle management** (ADD/UPDATE/DELETE/NOOP decisions)
- **Fact extraction** (LLM extracts and normalizes facts)
- **Scope management** (user, session, feedback, global)
- **Sensitivity tracking** (public, private, secret)
- **Spaced repetition** (forgetting curve)
- **Background consolidation** (session→user scope promotion)
- **Compaction** (automatic history compression)

### Storage Format

**File**: `memory.md`

```yaml
---
id: "mem_xyz"
scope: "user"              # user, session, feedback, global
user_id: "user_123"
session_id: ""             # empty for user scope
type: "semantic"           # episodic, semantic, procedural, reflection, summary, profile
importance: 8              # 1-10
emotion: "positive"        # positive, negative, neutral
sensitivity: "private"     # public, private, secret
strength: 0.95             # confidence 0-1
created_at: "2026-04-10T10:00:00Z"
updated_at: "2026-04-10T10:00:00Z"
last_accessed_at: "2026-04-10T14:00:00Z"
related_to: "mem_abc"      # ID of related memory
promoted_from: "mem_def"   # if promoted from session scope
metadata:
  tags: "python,debugging"
  source: "user_conversation"
---

# Fact Content

The user prefers Python for scripting and uses VS Code for editing.
They have a macOS M1 laptop and work from a coffee shop.
```

### Core Interfaces

**File**: `internal/memory/store.go`

```go
type Store interface {
    Add(ctx context.Context, fact Fact) error
    Update(ctx context.Context, fact Fact) error
    Delete(ctx context.Context, id string) error
    Get(ctx context.Context, id string) (*Fact, error)
    Search(ctx context.Context, query MemoryQuery) ([]Fact, error)
    ListByScope(ctx context.Context, scope, userID string) ([]Fact, error)
}

type Fact struct {
    ID           string
    Scope        string              // user, session, feedback, global
    Content      string
    Type         string              // episodic, semantic, procedural
    Importance   int                 // 1-10
    Strength     float64             // 0-1 confidence
    Sensitivity  string              // public, private, secret
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

### File-Based Memory Store

**File**: `internal/memory/file_store.go` (300+ lines)

```go
type FileMemoryStore struct {
    baseDir  string              // ~/.IronClaw/memory
    db       *sql.DB             // SQLite index
    embedder EmbeddingProvider   // OpenAI embeddings
    cfg      MemoryConfig        // Configuration
}
```

**Directories**:
- `~/.IronClaw/memory/user/` - User-scoped facts
- `~/.IronClaw/memory/session/` - Session-scoped facts
- `~/.IronClaw/memory/feedback/` - User feedback
- `~/.IronClaw/memory/global/` - Global facts

**Index Table** (SQLite):

```sql
CREATE TABLE memory_index (
    id TEXT PRIMARY KEY,
    scope TEXT,
    user_id TEXT,
    session_id TEXT,
    type TEXT,
    importance INTEGER,
    strength REAL,
    sensitivity TEXT,
    embedding BLOB,  -- float32 vector
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    accessed_at TIMESTAMP
);
```

### Lifecycle Management

**File**: `internal/memory/lifecycle.go` (300+ lines)

For each new fact candidate:

```
New fact
    ↓
Extract & normalize (LLM)
    ↓
Search for similar facts in store
    ↓
LLM decides: ADD, UPDATE, DELETE, or NOOP?
    ↓
If UPDATE: merge with existing fact
If DELETE: remove conflicting fact
If ADD: insert new fact
If NOOP: skip
    ↓
Update graph (if enabled)
```

**Decision Logic**:

- **ADD**: New fact with >85% uniqueness
- **UPDATE**: Existing fact with >80% similarity (merge/refine)
- **DELETE**: Conflicting fact detected
- **NOOP**: Redundant or low-confidence fact

### Fact Extraction

**File**: `internal/memory/facts.go`

LLM extracts facts from conversation:

**Input**: "I just installed Go 1.23 on my M1 Mac"

**Output**:
```json
[
  {
    "type": "semantic",
    "fact": "User has Go 1.23 installed on M1 Mac",
    "confidence": 0.95,
    "importance": 3
  }
]
```

### Embedding Provider

**File**: `internal/memory/embedding.go`

```go
type EmbeddingProvider interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    Dimensions() int
}
```

**Default**: OpenAI `text-embedding-3-small` (1536 dimensions)

### Background Tasks

#### 1. Consolidator

**File**: `internal/memory/consolidator.go`

- Runs every 24 hours (configurable)
- Promotes high-value **session-scoped** facts to **user scope**
- Only promotes facts older than consolidation interval
- Preserves metadata and relationships

#### 2. Compactor

**File**: `internal/memory/compactor.go`

- Runs every hour
- Removes low-importance facts
- Merges similar facts
- Cleans up stale facts

#### 3. LLM Compressor

**File**: `internal/memory/compressor.go`

- Incrementally compresses old conversation turns
- Applies layered compression strategy:
  1. **Layer 1** (30%): Move large tool results to disk
  2. **Layer 2** (50%): LLM-summarize old turns
  3. **Layer 3** (70%): Trim low-priority context
  4. **Layer 4** (90%): Drop oldest messages
- Token budget aware

### Forgetting Curve

**File**: `internal/memory/forgetting_curve.go`

Implements Ebbinghaus forgetting curve:

- Facts fade naturally unless accessed
- Repeated access strengthens memory
- System suggests facts to review based on forgetting curve
- `strength` parameter tracks retention

---

## KNOWLEDGE BASE

### Overview

The knowledge base provides **document ingestion + hybrid retrieval** (BM25 + vector search) with optional **knowledge graph** for entity/relationship extraction.

### Interfaces

**File**: `internal/knowledge/knowledge.go`

```go
type KnowledgeBase interface {
    Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
    Ingest(ctx context.Context, uri, sourceType string) error
    Sources(ctx context.Context) ([]Source, error)
    DeleteSource(ctx context.Context, sourceID string) error
}

type KnowledgeQuery struct {
    Text       string
    Embedding  []float32
    Limit      int
    SourceType string  // optional filter
}

type KnowledgeResult struct {
    Chunk Chunk
    Score float64  // 0-1 relevance
}
```

### Chunking

**File**: `internal/knowledge/chunk.go`

Documents are split into overlapping chunks:

```yaml
knowledge:
  chunk_size: 512        # tokens per chunk
  chunk_overlap: 64      # overlap between chunks
```

### Hybrid Retrieval

**File**: `internal/knowledge/retriever.go`

Two-stage retrieval:

**Stage 1: BM25** (Full-text search)
- Fast keyword matching
- Tokenization & stemming
- Weight: 40% (configurable)

**Stage 2: Vector Search** (Semantic search)
- Embedding similarity
- k-NN search on SQLite index
- Weight: 60% (configurable)

**Combined Score**: `0.4 * bm25 + 0.6 * vector`

### Reranking

**File**: `internal/knowledge/reranker.go`

Optional LLM-based reranking:

```
Initial Results (hybrid score)
    ↓
LLM: "Given query, rank these chunks by relevance"
    ↓
Reranked Results (LLM score)
```

**Configuration**:

```yaml
knowledge:
  reranker:
    enabled: true
    provider: llm  # "llm" or "none"
```

### Knowledge Graph

**File**: `internal/knowledge/graph/`

Entity & relationship extraction:

```
Document → LLM → Entities & Relations → Graph
    ↓
"John works at Acme Corp in NYC"
    ↓
Node(John, person) → Edge(works_at) → Node(Acme Corp, company)
Node(Acme Corp) → Edge(located_at) → Node(NYC, location)
```

**Graph Operations** (`graph.go`):

```go
type Graph interface {
    UpsertNode(ctx context.Context, node Node) (string, error)
    UpsertEdge(ctx context.Context, edge Edge) (string, error)
    Neighbors(ctx context.Context, nodeID, edgeType string) ([]Triple, error)
    Traverse(ctx context.Context, nodeID string, maxDepth int) ([]Triple, error)
    FindNode(ctx context.Context, nodeType, name string) (*Node, error)
    FindByName(ctx context.Context, name string) ([]Node, error)
}
```

### Graph Decay Task

**File**: `internal/knowledge/graph/graph_decay.go`

Background task that:
- Decays edge weights over time
- Removes stale edges
- Recalculates importance scores
- Runs every 6 hours (configurable)

### Graph ↔ Memory Sync

**File**: `internal/knowledge/graph/graph_sync.go`

Syncs memory lifecycle events to the knowledge graph:

- When fact is **added** → Extract entities → Update graph
- When fact is **updated** → Sync edges
- When fact is **deleted** → Clean up unused nodes

---

## MCP INTEGRATION

### Overview

IronClaw integrates **Model Context Protocol (MCP)** servers to dynamically discover and register external tools.

**Files**: `internal/mcp/`

### MCP Manager

**File**: `internal/mcp/manager.go` (100+ lines)

```go
type Manager struct {
    clients map[string]client.MCPClient
    mu      sync.RWMutex
}

func (m *Manager) StartServers(ctx context.Context, servers map[string]config.MCPServerConfig, registry *tool.Registry) error
```

**Flow**:

1. For each MCP server in config
2. Create stdio client with command + args
3. Initialize MCP handshake
4. Call `ListTools()` to discover tools
5. Wrap each tool in `ToolAdapter`
6. Register in tool registry

### MCP Tool Adapter

**File**: `internal/mcp/tool_adapter.go`

```go
type ToolAdapter struct {
    client          client.MCPClient
    serverName      string
    tool            mcp.Tool
    requiresApproval bool
}

func (ta *ToolAdapter) Execute(ctx context.Context, input []byte) (tool.Result, error)
```

Wraps MCP tools to implement the `tool.Tool` interface.

### Configuration

```yaml
tools:
  mcp:
    servers:
      github:
        command: docker
        args: ["run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"]
        env:
          GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
        requires_approval: true
      
      filesystem:
        command: npx
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
        requires_approval: false
```

### Tool Naming

MCP tools are registered as: `mcp_{server}_{toolname}`

Example: `mcp_github_search_repos`, `mcp_filesystem_list_directory`

### MCP Hot-Reload

**File**: `internal/gateway/gateway.go` (Start method)

```go
go gw.watchMCPDir(ctx)
```

Watches `~/.IronClaw/mcp/` for new/removed server configs:
- New YAML → Start new MCP server
- Deleted YAML → Stop server

---

## CHANNEL SYSTEM

### Channel Interface

**File**: `internal/channel/channel.go`

```go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler InboundHandler) error
    Send(ctx context.Context, msg OutboundMessage) error
    SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error)
    Stop(ctx context.Context) error
}

type InboundHandler func(ctx context.Context, msg InboundMessage)
```

### Optional Interfaces

Channels can implement optional interfaces:

```go
type ApprovalSender interface {
    SendApprovalRequest(ctx context.Context, target MessageTarget, toolName string, input string) (bool, error)
}

type ReflectionSender interface {
    SendReflectionRequest(ctx context.Context, target MessageTarget, reason string, confidence float64) (ReplanDecision, error)
}

type NotificationSender interface {
    SendNotification(ctx context.Context, target MessageTarget, text string) error
}

type FeedbackSender interface {
    SendFeedbackRequest(ctx context.Context, target MessageTarget) (float64, error)
}
```

### Telegram Channel

**File**: `internal/channel/telegram/adapter.go` (400+ lines)

**Features**:
- Updates polling with 30s timeout
- User ID allowlist for security
- Streaming response support
- Interactive approval requests (blocks until user responds or timeout)
- Reflection requests (replan decisions)
- Feedback collection (👍/👎)
- Notification support

**Configuration**:

```yaml
telegram:
  token: "${TELEGRAM_BOT_TOKEN}"
  allowed_user_ids:
    - 123456789
    - 987654321
```

**Approval Timeout**: Configurable, defaults to 120 seconds

### TUI (Terminal UI) Channel

**File**: `internal/channel/tui/adapter.go` (300+ lines)

**Features**:
- Interactive terminal with Bubbletea framework
- Real-time message streaming
- Approval prompts
- Reflection UI
- Feedback collection
- Auto-approve option

**Model** (`tui/model.go`):
- Input field for user messages
- Output pane for agent responses
- Status bar showing agent mode
- Color-coded message roles

**Configuration**:

```yaml
tui:
  auto_approve: false  # skip approval prompts
```

**Approval Timeout**: Configurable, defaults to 120 seconds

### Message Types

**File**: `internal/channel/message.go`

```go
type MessageTarget struct {
    Channel   string  // "telegram" or "tui"
    ChannelID string  // user/chat ID
    UserID    string  // user identifier
}

type InboundMessage struct {
    Channel    string     // "telegram" or "tui"
    ChannelID  string     // from whom
    UserID     string
    UserName   string
    Text       string
    CreatedAt  time.Time
}

type OutboundMessage struct {
    Channel   string
    ChannelID string
    Text      string
    CreatedAt time.Time
}
```

---

## GATEWAY WIRING

### Central Orchestrator

**File**: `internal/gateway/gateway.go` (363 lines)

The Gateway is the **central orchestrator** that wires all components together.

```go
type Gateway struct {
    cfg            *config.Config
    db             *store.DB
    sessions       *session.Manager
    runtime        *agent.Runtime
    cognitiveAgent *agent.CognitiveAgent
    tools          *tool.Registry
    memStore       memory.Store
    skillMgr       *skill.Manager
    channels       map[string]channel.Channel
    sched          *scheduler.Scheduler
    mcpManager     *mcp.Manager
    rlTrainer      *rl.Trainer
    consolidator   *memory.Consolidator
    compactor      *memory.Compactor
    graphDecay     *graph.GraphDecayTask
    stopCh         chan struct{}
}
```

### Initialization Sequence

The Gateway initializes subsystems in a specific order in `New()`:

1. **`initDatabase()`** - SQLite DB with schema migrations
2. **`initToolsAndHooks()`** - Built-in tools + hook manager
3. **`initAgentRuntime()`** - Simple agent executor
4. **`initMemorySystem()`** - Memory store + consolidator/compactor
5. **`initCognitiveAgent()`** - Cognitive pipeline (if enabled)
6. **`initKnowledgeSystem()`** - KB + graph (if enabled)
7. **`initSkillManager()`** - Skill loading (if enabled)
8. **`initMultiAgent()`** - Multi-agent orchestration (if enabled)
9. **Scheduler setup** - Cron task scheduler
10. **MCP manager** - External tool integration

Each init file:

```
// internal/gateway/init_*.go
func (gw *Gateway) initXXX() error {
    // Create component
    // Wire dependencies
    // Set reference in gateway
    return nil
}
```

### Gateway Lifecycle

**Start**:
1. Start MCP servers and hot-reload watcher
2. Start channels (Telegram, TUI)
3. Start scheduler
4. Start HTTP admin server (if enabled)
5. Start RL trainer (if enabled)
6. Ready to handle messages

**Stop**:
1. Stop channels (graceful shutdown)
2. Stop scheduler
3. Close MCP connections
4. Stop RL trainer
5. Stop background tasks (consolidator, compactor, graph decay)
6. Close database

### Message Flow

```
Channel receives message
    ↓
Channel.InboundHandler callback invoked (gateway.handleInbound)
    ↓
Message validation & session lookup
    ↓
Route to CognitiveAgent (if enabled) or Runtime
    ↓
PERCEIVE→PLAN→ACT→OBSERVE→REFLECT
    ↓
Response sent back to channel
```

### Background Tasks

**Tasks started in `Start()`**:

1. **MCP Hot-Reload** (`watchMCPDir`)
   - Polls `~/.IronClaw/mcp/` every second
   - Detects new/removed configs
   - Starts/stops servers

2. **Result Store Cleanup** (if enabled)
   - Runs every hour
   - Deletes results older than TTL (24h default)

3. **Memory Consolidator** (if enabled)
   - Runs every 24 hours
   - Promotes session→user facts

4. **Memory Compactor** (if enabled)
   - Runs every hour
   - Merges/removes low-value facts

5. **Graph Decay** (if enabled)
   - Runs every 6 hours
   - Decays edge weights
   - Removes stale relationships

---

## CONFIGURATION SYSTEM

### Configuration File

**Default path**: `configs/ironclaw.yaml`

Loaded in `cmd/ironclaw/main.go`:

```go
cfg, err := config.Load(cfgPath)
```

### Config Structure

**File**: `internal/config/config.go` (200+ lines)

```go
type Config struct {
    LLM           LLMConfig
    Telegram      TelegramConfig
    TUI           TUIConfig
    Agent         AgentConfig
    Store         StoreConfig
    Memory        MemoryConfig
    Knowledge     KnowledgeConfig
    Graph         GraphConfig
    Scheduler     SchedulerConfig
    Tools         ToolsConfig
    Server        ServerConfig
    Log           LogConfig
    Skills        SkillsConfig
    Agents        AgentsConfig
    Permissions   PermissionsConfig
    Hooks         HooksConfig
}
```

### Conditional Features

Features enabled/disabled via config:

| Feature | Config Key | Default | Purpose |
|---------|-----------|---------|---------|
| Memory | `memory.enabled` | true | Persistent fact storage |
| Knowledge Base | `knowledge.enabled` | false | Document ingestion & search |
| Skills | `skills.enabled` | true | Extensible skill loading |
| Multi-Agent | `agents.enabled` | false | Sub-agent orchestration |
| Cognitive Agent | `agent.mode` | "simple" | PERCEIVE→PLAN→ACT→OBSERVE→REFLECT |
| RL System | `agent.rl.enabled` | false | Adaptive tool selection |
| Scheduler | `scheduler.enabled` | true | Cron task execution |
| HTTP Server | `server.enabled` | false | Admin API |
| Hooks | `hooks.*` | (varies) | Event handlers |

### Example Configuration

See `configs/ironclaw.example.yaml` (250+ lines)

Key sections:
- **LLM**: Provider, API key, model, token limits
- **Telegram**: Bot token, allowed user IDs
- **Agent**: Iteration limit, mode, cognitive settings
- **Memory**: Storage type, embedding model, consolidation
- **Knowledge**: Chunk size, retrieval weights, reranking
- **Tools**: Per-tool config, permissions, result persistence, MCP servers
- **Scheduler**: Poll interval
- **Permissions**: Default action, rule-based policies
- **Hooks**: Event handlers

---

## SKILL SYSTEM

### Skill Structure

**File**: `internal/skill/skill.go` (200+ lines)

Skills are **SKILL.md files** with YAML frontmatter:

```yaml
---
name: "web_scraper"
description: "Scrape web pages and extract data"
version: "1.0.0"
author: "Jane Doe"
tags: ["web", "scraping", "http"]
metadata:
  openclaw:
    requires:
      env:
        - OPENAI_API_KEY
      bins:
        - curl
        - jq
---

# Web Scraper Skill

## Overview
This skill extracts data from web pages using CSS selectors or XPath.

## Usage
```
GET /scrape?url=https://example.com&selector=.title
```

## Examples
...
```

### Skill Manager

**File**: `internal/skill/manager.go` (150+ lines)

```go
type Manager struct {
    skills map[string]*Skill
    mu     sync.RWMutex
}

func (m *Manager) LoadBuiltin() error
func (m *Manager) LoadDir(dir string) error
func (m *Manager) GetSkill(name string) (*Skill, bool)
func (m *Manager) All() []*Skill
```

### Skill Loading

**Paths**:
1. Built-in skills in `internal/skill/builtin/`
2. User skills in `~/.IronClaw/skills/`
3. Extra directories from config

**Loading**:

```go
mgr := skill.New()
mgr.LoadBuiltin()                          // Load built-in skills
mgr.LoadDir(os.path.Join(home, ".IronClaw", "skills"))  // Load user skills
for _, dir := range cfg.Skills.ExtraDirs {
    mgr.LoadDir(dir)                       // Load extra skills
}
```

### Skill Usage

Skills are **context/knowledge** provided to LLM in system prompt:

```
Available skills:
- web_scraper: Scrape web pages and extract data
- data_processor: Transform and analyze data
- email_sender: Send emails via SMTP
...

Use skills when appropriate to enhance your capabilities.
```

### Skill Installation (ClawHub)

**File**: `cmd/ironclaw/skill.go` (200+ lines)

CLI commands for managing skills:

```bash
ironclaw skill list                           # List installed skills
ironclaw skill search "web scraper"           # Search ClawHub
ironclaw skill install forest-isle/web-scraper  # Install from ClawHub
ironclaw skill update                         # Update all skills
ironclaw skill remove web_scraper            # Remove skill
```

ClawHub integration via `clawhub` CLI (requires npm install -g clawhub).

---

## STORE/DB LAYER

### Database Wrapper

**File**: `internal/store/db.go` (150+ lines)

```go
type DB struct {
    conn *sql.DB
    mu   sync.RWMutex
}

func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
```

### Schema

**File**: `internal/store/migrations/` (SQLite schema)

Tables:

```sql
-- Sessions
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    channel TEXT,
    channel_id TEXT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- Session messages
CREATE TABLE session_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT,
    role TEXT,
    content TEXT,
    tool_name TEXT,
    tool_input TEXT,
    created_at TIMESTAMP,
    FOREIGN KEY(session_id) REFERENCES sessions(id)
);

-- Memory index (for quick lookups)
CREATE TABLE memory_index (
    id TEXT PRIMARY KEY,
    scope TEXT,
    user_id TEXT,
    session_id TEXT,
    type TEXT,
    importance INTEGER,
    strength REAL,
    sensitivity TEXT,
    embedding BLOB,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- Knowledge base chunks
CREATE TABLE knowledge_chunks (
    id TEXT PRIMARY KEY,
    source_id TEXT,
    source_uri TEXT,
    source_type TEXT,
    content TEXT,
    embedding BLOB,
    chunk_index INTEGER,
    created_at TIMESTAMP,
    FOREIGN KEY(source_id) REFERENCES knowledge_sources(id)
);

-- Scheduled tasks
CREATE TABLE scheduled_tasks (
    id TEXT PRIMARY KEY,
    name TEXT,
    cron_expr TEXT,
    prompt TEXT,
    channel TEXT,
    channel_id TEXT,
    enabled BOOLEAN,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- Hook audit log
CREATE TABLE hook_audit_log (
    id TEXT PRIMARY KEY,
    hook_type TEXT,
    event_data TEXT,
    result TEXT,
    timestamp TIMESTAMP
);
```

### Database Initialization

In `gateway.initDatabase()`:

```go
db, err := store.Open(cfg.Store.Path)  // Opens or creates SQLite DB
if err != nil {
    return err
}
db.Migrate()  // Applies schema migrations
gw.db = db
```

---

## SCHEDULING SYSTEM

### Scheduler

**File**: `internal/scheduler/scheduler.go` (200+ lines)

```go
type Scheduler struct {
    db           *store.DB
    cron         *cron.Cron
    handler      TaskHandler
    pollInterval time.Duration
    entries      map[string]cron.EntryID
}

type Task struct {
    ID        string
    Name      string
    CronExpr  string
    Prompt    string
    Channel   string
    ChannelID string
}

type TaskHandler func(ctx context.Context, task Task)
```

### Cron Expressions

Supports **cron with seconds** (6-field format):

```
0    0    12   *    *    *
│    │    │    │    │    │
│    │    │    │    │    └── Day of week (0-6)
│    │    │    │    └────── Month (1-12)
│    │    │    └─────────── Day of month (1-31)
│    │    └──────────────── Hour (0-23)
│    └─────────────────────── Minute (0-59)
└──────────────────────────── Second (0-59)

Examples:
*/5 * * * * *        # Every 5 seconds
0 * * * * *          # Every minute at 0 seconds
0 0 12 * * *         # Daily at noon
0 0 0 * * 0          # Weekly (Sunday)
```

### Task Management

**Flow**:

1. User creates task via HTTP API or CLI
2. Task stored in `scheduled_tasks` table with `enabled=1`
3. Scheduler polls DB every `pollInterval` (default 60s)
4. New tasks registered with cron runner
5. When task fires, `TaskHandler` invoked
6. Handler routes to `gateway.handleInbound()` with task prompt

**Example**:

```sql
INSERT INTO scheduled_tasks (id, name, cron_expr, prompt, channel, channel_id, enabled)
VALUES ('task_1', 'daily_report', '0 0 8 * * *', 'Generate daily report', 'telegram', '123456789', 1);
```

Every day at 8am:
1. Scheduler fires task
2. Prompt "Generate daily report" routed to Telegram
3. Agent processes and responds to user

---

## HOOK SYSTEM

### Hook Types

**File**: `internal/hook/hook.go` (150+ lines)

Four hook types:

```go
type PreToolUseEvent struct {
    ToolName     string
    Input        string
    Capabilities map[string]bool
}

type PostToolUseEvent struct {
    ToolName           string
    Input              string
    Output             string
    Error              string
    Status             string  // "success", "error", "denied"
    DurationMs         int64
    SessionID          string
    PermissionAction   string  // "allow", "deny", "ask_approved"
    PermissionReason   string
    PermissionRule     string
}

type OnUserMessageEvent struct {
    Channel   string
    ChannelID string
    UserID    string
    Text      string
}

type PreCompactEvent struct {
    SessionID       string
    MessageCount    int
    EstUtilization  float64
}
```

### Handler Interfaces

```go
type PreToolUseHandler interface {
    OnPreToolUse(ctx context.Context, event PreToolUseEvent) (PreToolUseResult, error)
}

type PostToolUseHandler interface {
    OnPostToolUse(ctx context.Context, event PostToolUseEvent) (PostToolUseResult, error)
}

type OnUserMessageHandler interface {
    OnUserMessage(ctx context.Context, event OnUserMessageEvent) (OnUserMessageResult, error)
}

type PreCompactHandler interface {
    OnPreCompact(ctx context.Context, event PreCompactEvent) (PreCompactResult, error)
}
```

### Hook Manager

**File**: `internal/hook/manager.go` (150+ lines)

```go
type Manager struct {
    handlers map[string][]interface{}  // hook type → handlers
    mu       sync.RWMutex
}

func (m *Manager) Register(hookType string, handler interface{}) error
func (m *Manager) FirePreToolUse(ctx context.Context, event PreToolUseEvent) ([]PreToolUseResult, error)
func (m *Manager) FirePostToolUse(ctx context.Context, event PostToolUseEvent) ([]PostToolUseResult, error)
```

### Hook Factory

**File**: `internal/hook/factory.go`

Creates hooks from config:

```go
func NewHandler(handlerType string, config map[string]any) (interface{}, error)
```

**Built-in Hooks**:
- `git_injector`: Injects Git context (branch, repo root)
- `workdir_injector`: Injects working directory
- `precompact_preserver`: Preserves important messages during compaction

### Audit Logging

**File**: `internal/hook/audit.go`, `internal/hook/audit_db.go`

All hook events logged to `hook_audit_log` table:

```sql
CREATE TABLE hook_audit_log (
    id TEXT PRIMARY KEY,
    hook_type TEXT,
    event_data TEXT,
    result TEXT,
    timestamp TIMESTAMP
);
```

---

## RL SYSTEM

### Overview

IronClaw includes an optional **Reinforcement Learning** system for:

- **Adaptive tool selection**: RL learns which tools work best
- **Plan optimization**: RL learns effective planning strategies
- **Replan decisions**: RL learns when to replan vs continue

**Files**: `internal/rl/`

### RL Trainer

**File**: `internal/rl/trainer.go` (300+ lines)

```go
type Trainer struct {
    policy      Policy
    rewardFunc  RewardFunc
    storage     Storage
    cfg         RLConfig
}

type Episode struct {
    State       State
    Action      Action
    Reward      float64
    NextState   State
    Done        bool
}
```

### RL Algorithms

**Supported**:

1. **Bandit** (`bandit.go`) - Multi-armed bandit for tool selection
2. **PPO** (`ppo.go`) - Policy gradient for planning
3. **DQN** (`dqn.go`) - Deep Q-Learning for complex decisions

**Configuration**:

```yaml
agent:
  rl:
    enabled: true
    cold_start_episodes: 1000
    exploration_rate: 0.2
    exploration_decay: 0.9995
    bandit:
      enabled: true
    ppo:
      enabled: true
      learning_rate: 0.0003
    dqn:
      enabled: true
      learning_rate: 0.001
```

### Reward Function

**File**: `internal/rl/reward.go`

```go
type RewardFunc struct {
    taskSuccessWeight       float64  // 0.5
    efficiencyWeight        float64  // 0.3
    safetyWeight            float64  // 0.15
    userSatisfactionWeight  float64  // 0.05
}

// Reward = 0.5*success + 0.3*efficiency + 0.15*safety + 0.05*satisfaction
```

**Success**: Task completed as expected
**Efficiency**: Completed with minimal tool calls/tokens
**Safety**: No security violations or errors
**Satisfaction**: User feedback (👍/👎)

### Experience Storage

**File**: `internal/rl/storage.go`

Experiences stored in SQLite for offline training:

```sql
CREATE TABLE rl_experiences (
    id TEXT PRIMARY KEY,
    episode_id TEXT,
    state BLOB,      -- JSON serialized state
    action BLOB,     -- JSON serialized action
    reward REAL,
    next_state BLOB,
    done BOOLEAN,
    timestamp TIMESTAMP
);
```

---

## DESIGN PATTERNS & ARCHITECTURE

### 1. Gateway Pattern (Central Orchestrator)

**Problem**: Multiple subsystems need coordination

**Solution**: Gateway wires all components and manages lifecycle

**Benefits**:
- Single point of initialization
- Consistent error handling
- Clean shutdown
- Testability

### 2. Dependency Injection

**Pattern**: Components accept dependencies via constructor/setter

```go
func (r *Runtime) SetMemoryStore(s memory.Store)
func (r *Runtime) SetSkillManager(m *skill.Manager)
func (r *Runtime) SetAgentManager(m *AgentManager)
```

**Benefits**:
- Loose coupling
- Easy testing (mock dependencies)
- Extensibility

### 3. Interface-Based Design

Most components define interfaces for extensibility:

```go
type Tool interface { ... }
type Store interface { ... }
type Channel interface { ... }
type Graph interface { ... }
```

**Benefits**:
- Multiple implementations (FileMemoryStore, SQLiteMemoryStore)
- Easy testing
- Plugin-friendly

### 4. Background Task Pattern

Long-running operations run in goroutines:

- Memory consolidator
- Memory compactor
- Graph decay
- MCP hot-reload watcher
- Result store cleanup
- Scheduler polling

**Safety**: All use `context.Done()` for graceful shutdown

### 5. Configuration-Driven Features

Features enabled/disabled via YAML:

```yaml
memory:
  enabled: true
knowledge:
  enabled: false
agents:
  enabled: false
```

**Benefits**:
- Easy A/B testing
- Gradual rollout
- Backward compatibility

### 6. Stream Processing

Supports streaming responses:

```go
type StreamUpdater interface {
    Update(text string) error
    Finish(text string) error
}
```

Channels (Telegram, TUI) implement streaming for:
- Real-time response updates
- Interactive approval requests
- Status notifications

### 7. Permission Policy Engine

Declarative permission rules:

```yaml
permissions:
  default: ask
  rules:
    - tool: bash
      pattern: "git *"
      action: allow
    - tool: bash
      pattern: "rm -rf *"
      action: deny
```

**Benefits**:
- No hardcoding
- Easy auditing
- Flexible policies

### 8. Lifecycle Management (mem0 style)

Facts go through lifecycle:

```
New Fact → Search Similar → LLM Decision → {ADD, UPDATE, DELETE, NOOP}
```

**Benefits**:
- Automatic deduplication
- Fact merging
- Conflict resolution

### 9. Layered Compression

Progressive context compression:

```
Layer 1 (30%): Tool results to disk
    ↓
Layer 2 (50%): Summarize old turns
    ↓
Layer 3 (70%): Trim low-priority
    ↓
Layer 4 (90%): Drop oldest
```

**Benefits**:
- Gradual token reduction
- Preserves important context
- Minimizes LLM calls

### 10. Hook Event System

Pre/post hooks for extensibility:

- PreToolUse: Validate before execution
- PostToolUse: Process results
- OnUserMessage: Inject context
- PreCompact: Preserve important messages

**Benefits**:
- Non-invasive extensions
- Audit logging
- Custom logic injection

---

## INTEGRATION CHAINS (7 Total)

### Chain 1: Agent Execution

```
User Message → Session → CognitiveAgent.HandleMessage()
    → PERCEIVE (context gathering)
    → PLAN (LLM tool planning)
    → ACT (tool execution + approval)
    → OBSERVE (result evaluation)
    → REFLECT (replan decision)
    → Response to User
```

### Chain 2: Memory Lifecycle

```
LLM Conversation → Extract Facts → Search Similar
    → LLM Decision (ADD/UPDATE/DELETE/NOOP)
    → Update Store
    → Update Memory Index
    → Sync to Knowledge Graph
    → Background Consolidation (session→user)
```

### Chain 3: Knowledge Retrieval

```
PERCEIVE Phase → Query Knowledge Base
    → Hybrid Retrieval (BM25 + Vector)
    → Optional Reranking (LLM)
    → Return Top-K chunks
    → Include in System Prompt
```

### Chain 4: Tool Execution

```
PLAN Phase → Tool Selection
    → Permission Check
    → Pre-Tool Hook
    → Execute (or ask approval)
    → Post-Tool Hook
    → Store Result (inline or disk)
    → Return to ACT
```

### Chain 5: MCP Integration

```
Gateway.Start() → Discover MCP Servers
    → Initialize Handshake
    → ListTools()
    → Wrap in ToolAdapter
    → Register in Registry
    → Available for PLAN phase
```

### Chain 6: Scheduling

```
Scheduler.Start() → Poll DB for Tasks
    → Register with Cron
    → Task Fires
    → Invoke TaskHandler
    → Route to gateway.handleInbound()
    → Process as User Message
```

### Chain 7: Multi-Agent Orchestration

```
User Task → Orchestrator.RunParallel()
    → Create Sub-agents
    → Each agent has Runtime + tools
    → Run in parallel with parent
    → Orchestrator aggregates results
    → Return to user
```

---

## KEY INSIGHTS

1. **Production-Ready**: Zero critical blockers, comprehensive error handling

2. **Modular Architecture**: 16 independent packages with clear responsibilities

3. **Extensible**: Interface-based design enables custom implementations

4. **Scalable**: Concurrent execution of read-only tools, background task management

5. **Secure**: Permission engine, approval workflows, sensitivity tracking

6. **Observable**: Hook audit logging, profiling, comprehensive logging

7. **Local-First**: Minimal external dependencies, SQLite-based persistence

8. **Multi-Channel**: Telegram, TUI, extensible for Discord/Slack/etc.

9. **RL-Ready**: Infrastructure for adaptive tool selection and planning

10. **Knowledge-Aware**: Hybrid KB retrieval, entity graph, fact lifecycle management

---

## CONFIGURATION CHECKLIST

Before production deployment:

- [ ] Set `llm.provider`, `llm.api_key`, `llm.model`
- [ ] Configure `telegram.token` and `allowed_user_ids`
- [ ] Set `agent.mode` ("simple" or "cognitive")
- [ ] Enable/disable memory, knowledge, skills, agents as needed
- [ ] Configure tool permissions (bash, file, http)
- [ ] Set up MCP servers if needed
- [ ] Configure memory storage dir and embedding model
- [ ] Set up logging level (debug/info/warn/error)
- [ ] Configure scheduler poll interval
- [ ] Set hook handlers for audit logging
- [ ] Test graceful shutdown
- [ ] Load test with expected traffic

---

**Document Generated**: April 10, 2026  
**Analysis Completion**: 100%  
**Status**: ✅ Production Ready

